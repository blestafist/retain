package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
)

const (
	maxOutstandingStreamCallbacks = 8
	maxStreamLineBytes            = 1 << 20
	maxStreamEventBytes           = 4 << 20
)

var (
	// ErrStreamCallbackCapacity reports that all bounded callback goroutines are occupied.
	ErrStreamCallbackCapacity = errors.New("llm stream callback capacity is exhausted")
	errStreamCallbackPanic    = errors.New("llm stream callback panicked")
	streamCallbackSlots       = make(chan struct{}, maxOutstandingStreamCallbacks)
)

// StreamRequest describes a text-only streamed chat completion.
type StreamRequest struct {
	Model  string
	System string
	User   string
}

// Usage contains provider-reported token usage for a streamed completion.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
}

// StreamText streams text deltas from an OpenAI-compatible chat completion endpoint.
// onDelta receives the operation context and non-empty text; callback errors stop the stream.
func (c *Client) StreamText(ctx context.Context, request StreamRequest, onDelta func(context.Context, string) error) (Usage, error) {
	if ctx == nil {
		return Usage{}, errors.New("llm context is required")
	}
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}
	if request.User == "" {
		return Usage{}, errors.New("llm user prompt is required")
	}
	if onDelta == nil {
		return Usage{}, errors.New("llm delta callback is required")
	}

	callerCtx := ctx
	operationCtx, cancel := context.WithTimeout(callerCtx, c.config.Timeout)
	defer cancel()

	lease, err := c.breaker.acquire(time.Now())
	if err != nil {
		return Usage{}, err
	}
	defer func() { c.breaker.release(lease, time.Now()) }()

	contextError := func() error {
		err, operationTimeout := operationContextError(callerCtx, operationCtx)
		if operationTimeout {
			c.breaker.failure(lease, time.Now())
		}
		return err
	}
	providerFailure := func(err error) error {
		if contextErr := contextError(); contextErr != nil {
			return contextErr
		}
		c.breaker.failure(lease, time.Now())
		return err
	}

	model := request.Model
	if model == "" {
		model = c.config.Model
	}
	type streamMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	requestBody := struct {
		Model         string          `json:"model"`
		Messages      []streamMessage `json:"messages"`
		Stream        bool            `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}{
		Model: model,
		Messages: func() []streamMessage {
			messages := make([]streamMessage, 0, 2)
			if request.System != "" {
				messages = append(messages, streamMessage{Role: "system", Content: request.System})
			}
			return append(messages, streamMessage{Role: "user", Content: request.User})
		}(),
		Stream: true,
	}
	requestBody.StreamOptions.IncludeUsage = true
	body, err := json.Marshal(requestBody)
	if err != nil {
		return Usage{}, err
	}
	endpoint := strings.TrimRight(c.config.BaseURL, "/") + "/chat/completions"
	httpClient := &http.Client{}

	var usage Usage
	for attempt := 0; ; attempt++ {
		if err := contextError(); err != nil {
			return usage, err
		}
		req, err := http.NewRequestWithContext(operationCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return usage, err
		}
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		response, requestErr := httpClient.Do(req)
		if requestErr != nil {
			if contextErr := contextError(); contextErr != nil {
				return usage, contextErr
			}
			return usage, providerFailure(&UpstreamError{})
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			status := response.StatusCode
			retryable := status == http.StatusTooManyRequests || status >= 500 && status < 600
			if retryable && attempt < c.config.MaxRetries {
				delay := retryDelay(&openai.Error{StatusCode: status, Response: response}, attempt, c.config)
				_ = response.Body.Close()
				if err := waitBackoff(operationCtx, delay); err != nil {
					if contextErr := contextError(); contextErr != nil {
						return usage, contextErr
					}
					return usage, err
				}
				continue
			}
			_ = response.Body.Close()
			if retryable {
				return usage, providerFailure(&UpstreamError{StatusCode: status})
			}
			c.breaker.success(lease)
			return usage, &UpstreamError{StatusCode: status}
		}

		streamErr, callbackErr, done := consumeTextEventStream(operationCtx, response.Body, onDelta, &usage)
		_ = response.Body.Close()
		if callbackErr != nil {
			// The provider delivered a delta; a blocked or canceled consumer is not
			// evidence of provider failure. Complete a half-open probe as successful.
			c.breaker.success(lease)
			if err := callerCtx.Err(); err != nil {
				return usage, err
			}
			return usage, callbackErr
		}
		if err := contextError(); err != nil {
			return usage, err
		}
		if streamErr != nil {
			return usage, providerFailure(&UpstreamError{})
		}
		if !done {
			return usage, providerFailure(&UpstreamError{})
		}
		c.breaker.success(lease)
		return usage, nil
	}
}

type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// consumeTextEventStream stops reading as soon as the provider's [DONE] event arrives.
// Scanner's finite token limit bounds memory use for individual SSE lines.
func consumeTextEventStream(ctx context.Context, body io.Reader, onDelta func(context.Context, string) error, usage *Usage) (providerErr, callbackErr error, done bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maxStreamLineBytes)
	var eventData strings.Builder
	hasData := false
	dataBytes := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err, nil, false
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if !hasData {
				continue
			}
			data := eventData.String()
			eventData.Reset()
			hasData = false
			dataBytes = 0
			finished, providerErr, callbackErr := consumeStreamEvent(ctx, data, onDelta, usage)
			if providerErr != nil || callbackErr != nil {
				return providerErr, callbackErr, false
			}
			if finished {
				return nil, nil, true
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, hasColon := strings.Cut(line, ":")
		if !hasColon {
			value = ""
		} else if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		if field == "data" {
			separatorBytes := 0
			if hasData {
				separatorBytes = 1
			}
			dataBytes += len(value) + separatorBytes
			if dataBytes > maxStreamEventBytes {
				return errors.New("provider stream event exceeds size limit"), nil, false
			}
			if hasData {
				eventData.WriteByte('\n')
			}
			eventData.WriteString(value)
			hasData = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err, nil, false
	}
	return nil, nil, false
}

func consumeStreamEvent(ctx context.Context, data string, onDelta func(context.Context, string) error, usage *Usage) (done bool, providerErr, callbackErr error) {
	if data == "[DONE]" {
		return true, nil, nil
	}
	var chunk streamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false, err, nil
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return false, errors.New("provider sent a stream error event"), nil
	}
	if chunk.Usage != nil {
		*usage = Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
		}
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 || choice.Delta.Content == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err, nil
		}
		callbackErr, interrupted := invokeStreamCallback(ctx, onDelta, choice.Delta.Content)
		if interrupted {
			if callbackErr == nil {
				callbackErr = ctx.Err()
			}
			return false, nil, callbackErr
		}
		if callbackErr != nil {
			return false, nil, callbackErr
		}
		if err := ctx.Err(); err != nil {
			return false, err, nil
		}
	}
	return false, nil, nil
}

func invokeStreamCallback(ctx context.Context, onDelta func(context.Context, string) error, delta string) (callbackErr error, interrupted bool) {
	select {
	case streamCallbackSlots <- struct{}{}:
	default:
		return ErrStreamCallbackCapacity, false
	}

	type callbackResult struct {
		err         error
		interrupted bool
	}
	result := make(chan callbackResult, 1)
	go func() {
		var err error
		defer func() {
			panicked := recover() != nil
			<-streamCallbackSlots
			if panicked {
				err = errStreamCallbackPanic
			}
			result <- callbackResult{err: err, interrupted: ctx.Err() != nil}
		}()
		err = onDelta(ctx, delta)
	}()

	select {
	case outcome := <-result:
		// Recheck after receiving the callback result as cancellation and the
		// buffered result can become ready together. Keep that provenance so
		// consumeStreamEvent won't report a callback interruption as provider I/O.
		if ctx.Err() != nil {
			outcome.interrupted = true
		}
		if outcome.interrupted && outcome.err == nil {
			outcome.err = ctx.Err()
		}
		return outcome.err, outcome.interrupted
	case <-ctx.Done():
		return ctx.Err(), true
	}
}
