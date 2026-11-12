package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamTextStreamsDeltasAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s, want POST /v1/chat/completions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer server-test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream        bool `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode stream request: %v", err)
		}
		if request.Model != "override-model" || !request.Stream || !request.StreamOptions.IncludeUsage {
			t.Errorf("stream request model/stream/options = %q/%v/%+v", request.Model, request.Stream, request.StreamOptions)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[0].Content != "system prompt" || request.Messages[1].Role != "user" || request.Messages[1].Content != "user prompt" {
			t.Errorf("messages = %+v", request.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"override-model","choices":[{"index":0,"delta":{"content":"Hello "},"finish_reason":null}]}`)
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"override-model","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`)
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"override-model","choices":[],"usage":{"prompt_tokens":17,"completion_tokens":4,"total_tokens":21}}`)
		writeStreamChunk(w, "[DONE]")
	}))
	defer server.Close()

	var answer strings.Builder
	usage, err := newTestClient(t, server.URL+"/v1").StreamText(context.Background(), StreamRequest{
		Model:  "override-model",
		System: "system prompt",
		User:   "user prompt",
	}, func(_ context.Context, delta string) error {
		answer.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamText() error = %v", err)
	}
	if answer.String() != "Hello world" {
		t.Errorf("answer = %q", answer.String())
	}
	if usage != (Usage{PromptTokens: 17, CompletionTokens: 4}) {
		t.Errorf("usage = %+v", usage)
	}
}

func TestStreamTextUsesConfiguredModelWhenRequestModelIsOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode stream request: %v", err)
		}
		if request.Model != "test-model" {
			t.Errorf("request model = %q, want configured default", request.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, "[DONE]")
	}))
	defer server.Close()

	if _, err := newTestClient(t, server.URL+"/v1").StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error { return nil }); err != nil {
		t.Fatalf("StreamText() error = %v", err)
	}
}

func TestStreamTextCallbackErrorStopsStream(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}`)
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"second"},"finish_reason":null}]}`)
		writeStreamChunk(w, "[DONE]")
	}))
	defer server.Close()

	wantErr := errors.New("consumer stopped")
	var deltas []string
	_, err := newTestClient(t, server.URL+"/v1").StreamText(context.Background(), StreamRequest{User: "question"}, func(_ context.Context, delta string) error {
		deltas = append(deltas, delta)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("StreamText() error = %v, want callback error", err)
	}
	if len(deltas) != 1 || deltas[0] != "first" {
		t.Errorf("callback deltas = %v", deltas)
	}
	if calls.Load() != 1 {
		t.Errorf("provider calls = %d, want 1", calls.Load())
	}
}

func TestStreamTextRetriesOnlyBeforeOutputAndSanitizesHTTPError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"sensitive server-test-key details"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.MaxRetries = 1
		config.CircuitThreshold = 1
	})
	_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error { return nil })
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) || upstreamErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StreamText() error = %v, want sanitized HTTP 503", err)
	}
	if strings.Contains(err.Error(), "server-test-key") || strings.Contains(err.Error(), "sensitive") {
		t.Errorf("upstream error leaked response details: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want initial attempt plus one retry", calls.Load())
	}
	client.breaker.mu.Lock()
	state := client.breaker.state
	client.breaker.mu.Unlock()
	if state != breakerOpen {
		t.Errorf("breaker state = %v, want open after exhausted 503 retries", state)
	}
}

func TestStreamTextDoesNotRetryAfterDeliveringOutput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"visible"},"finish_reason":null}]}`)
		writeStreamChunk(w, `{"error":{"message":"secret server-test-key detail"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", func(config *Config) { config.MaxRetries = 2 })
	var answer strings.Builder
	_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(_ context.Context, delta string) error {
		answer.WriteString(delta)
		return nil
	})
	if err == nil || strings.Contains(err.Error(), "server-test-key") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("StreamText() error = %v, want sanitized stream error", err)
	}
	if answer.String() != "visible" || calls.Load() != 1 {
		t.Errorf("answer/calls = %q/%d, want visible/1", answer.String(), calls.Load())
	}
}

func TestStreamTextCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"stop"},"finish_reason":null}]}`)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	_, err := newTestClient(t, server.URL+"/v1").StreamText(ctx, StreamRequest{User: "question"}, func(context.Context, string) error {
		calls++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StreamText() error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("callback count = %d, want 1", calls)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("server did not start stream")
	}
}

func TestStreamTextWholeOperationTimeout(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 100 * time.Millisecond
		config.CircuitThreshold = 1
	})
	done := make(chan error, 1)
	go func() {
		_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error { return nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("server did not receive streaming request")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("StreamText() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StreamText() did not honor its operation timeout")
	}
	client.breaker.mu.Lock()
	state := client.breaker.state
	client.breaker.mu.Unlock()
	if state != breakerOpen {
		t.Errorf("breaker state = %v, want open after operation timeout", state)
	}
}

func TestStreamTextEOFBeforeDoneIsTruncated(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantVisible string
	}{
		{name: "before output", content: `{"id":"stream-1","choices":[]}`},
		{name: "after partial text", content: `{"id":"stream-1","choices":[{"index":0,"delta":{"content":"partial"}}]}`, wantVisible: "partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamChunk(w, tt.content)
			}))
			defer server.Close()

			var visible strings.Builder
			_, err := newTestClient(t, server.URL+"/v1").StreamText(context.Background(), StreamRequest{User: "question"}, func(_ context.Context, delta string) error {
				visible.WriteString(delta)
				return nil
			})
			var upstreamErr *UpstreamError
			if !errors.As(err, &upstreamErr) || upstreamErr.StatusCode != 0 {
				t.Fatalf("StreamText() error = %v, want sanitized truncated-provider failure", err)
			}
			if visible.String() != tt.wantVisible {
				t.Errorf("visible text = %q, want %q", visible.String(), tt.wantVisible)
			}
		})
	}
}

func TestStreamTextReturnsAtDoneWhileBodyRemainsOpen(t *testing.T) {
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, "[DONE]")
		<-r.Context().Done()
		close(handlerDone)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", func(config *Config) { config.Timeout = 5 * time.Second })
	result := make(chan error, 1)
	go func() {
		_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error { return nil })
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("StreamText() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("StreamText() waited for the open response body after [DONE]")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("closing the response body did not cancel the provider request")
	}
}

func TestStreamTextBlockedCallbackHonorsOperationTimeout(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	callbackEntered := make(chan context.Context, 1)
	callbackFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"choices":[{"index":0,"delta":{"content":"blocked"}}]}`)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 100 * time.Millisecond
		config.CircuitThreshold = 1
	})
	result := make(chan error, 1)
	go func() {
		_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(ctx context.Context, _ string) error {
			callbackEntered <- ctx
			<-release
			close(callbackFinished)
			return nil
		})
		result <- err
	}()
	var callbackCtx context.Context
	select {
	case callbackCtx = <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("StreamText() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked callback prevented operation timeout")
	}
	client.breaker.mu.Lock()
	state, consecutive := client.breaker.state, client.breaker.consecutive
	client.breaker.mu.Unlock()
	if state != breakerClosed || consecutive != 0 {
		t.Errorf("breaker state/consecutive after blocked callback timeout = %v/%d, want closed/0", state, consecutive)
	}
	select {
	case <-callbackCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("callback context was not canceled on operation timeout")
	}
	unblock()
	select {
	case <-callbackFinished:
	case <-time.After(time.Second):
		t.Fatal("callback did not finish after release")
	}
}

func TestStreamTextBlockedCallbackTimeoutCompletesHalfOpenProbe(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	callbackEntered := make(chan struct{})
	callbackFinished := make(chan struct{})
	var callbackCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"choices":[{"index":0,"delta":{"content":"delivered"}}]}`)
		writeStreamChunk(w, "[DONE]")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 100 * time.Millisecond
		config.CircuitThreshold = 1
	})
	client.breaker.mu.Lock()
	client.breaker.state = breakerOpen
	client.breaker.consecutive = 1
	client.breaker.openUntil = time.Now().Add(-time.Second)
	client.breaker.mu.Unlock()

	result := make(chan error, 1)
	go func() {
		_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error {
			if callbackCalls.Add(1) == 1 {
				close(callbackEntered)
				<-release
				close(callbackFinished)
			}
			return nil
		})
		result <- err
	}()
	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("half-open probe callback did not start")
	}
	client.breaker.mu.Lock()
	state := client.breaker.state
	client.breaker.mu.Unlock()
	if state != breakerHalfOpen {
		t.Fatalf("breaker state during probe = %v, want half-open", state)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("StreamText() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked half-open callback prevented operation timeout")
	}
	client.breaker.mu.Lock()
	state, consecutive := client.breaker.state, client.breaker.consecutive
	client.breaker.mu.Unlock()
	if state != breakerClosed || consecutive != 0 {
		t.Fatalf("breaker state/consecutive after callback timeout = %v/%d, want closed/0", state, consecutive)
	}
	unblock()
	select {
	case <-callbackFinished:
	case <-time.After(time.Second):
		t.Fatal("callback did not finish after release")
	}
	if _, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error { return nil }); err != nil {
		t.Fatalf("StreamText() after timed-out probe = %v, want success", err)
	}
}

func TestStreamTextCallbackReturningAfterContextCancellationIsNotProviderFailure(t *testing.T) {
	for _, halfOpen := range []bool{false, true} {
		name := "closed"
		if halfOpen {
			name = "half-open probe"
		}
		t.Run(name, func(t *testing.T) {
			var providerCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if providerCalls.Add(1) == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					writeStreamChunk(w, `{"choices":[{"index":0,"delta":{"content":"cancel"}}]}`)
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamChunk(w, "[DONE]")
			}))
			defer server.Close()

			client := newTestClient(t, server.URL+"/v1", func(config *Config) {
				config.Timeout = 500 * time.Millisecond
				config.CircuitThreshold = 2
			})
			client.breaker.mu.Lock()
			if halfOpen {
				client.breaker.state = breakerOpen
				client.breaker.consecutive = 1
				client.breaker.openUntil = time.Now().Add(-time.Second)
			} else {
				// A timeout misclassified as a provider failure would reach the
				// threshold and open this otherwise closed circuit.
				client.breaker.consecutive = 1
			}
			client.breaker.mu.Unlock()

			callbackEntered := make(chan struct{}, 1)
			callbackReturned := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				_, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(ctx context.Context, _ string) error {
					callbackEntered <- struct{}{}
					<-ctx.Done()
					close(callbackReturned)
					return nil
				})
				result <- err
			}()
			select {
			case <-callbackEntered:
			case <-time.After(time.Second):
				t.Fatal("callback did not start")
			}
			if halfOpen {
				client.breaker.mu.Lock()
				state := client.breaker.state
				client.breaker.mu.Unlock()
				if state != breakerHalfOpen {
					t.Fatalf("breaker state while callback waits = %v, want half-open", state)
				}
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("StreamText() error = %v, want context.DeadlineExceeded", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("callback waiting for cancellation prevented operation timeout")
			}
			select {
			case <-callbackReturned:
			case <-time.After(time.Second):
				t.Fatal("callback did not return after context cancellation")
			}

			client.breaker.mu.Lock()
			state, consecutive := client.breaker.state, client.breaker.consecutive
			client.breaker.mu.Unlock()
			if state != breakerClosed || consecutive != 0 {
				t.Fatalf("breaker state/consecutive after callback cancellation = %v/%d, want closed/0", state, consecutive)
			}
			if _, err := client.StreamText(context.Background(), StreamRequest{User: "question"}, func(context.Context, string) error { return nil }); err != nil {
				t.Fatalf("StreamText() after callback cancellation = %v, want success", err)
			}
		})
	}
}

func TestStreamTextBlockedCallbackHonorsCallerCancellation(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	callbackEntered := make(chan context.Context, 1)
	callbackFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, `{"choices":[{"index":0,"delta":{"content":"blocked"}}]}`)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := newTestClient(t, server.URL+"/v1").StreamText(ctx, StreamRequest{User: "question"}, func(callbackCtx context.Context, _ string) error {
			callbackEntered <- callbackCtx
			<-release
			close(callbackFinished)
			return nil
		})
		result <- err
	}()
	var callbackCtx context.Context
	select {
	case callbackCtx = <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StreamText() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked callback prevented caller cancellation")
	}
	select {
	case <-callbackCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("callback context was not canceled with caller context")
	}
	unblock()
	select {
	case <-callbackFinished:
	case <-time.After(time.Second):
		t.Fatal("callback did not finish after release")
	}
}

func TestStreamCallbackGoroutinesAreBounded(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	started := make(chan struct{}, maxOutstandingStreamCallbacks)
	results := make(chan error, maxOutstandingStreamCallbacks)
	var callbacks sync.WaitGroup
	for i := 0; i < maxOutstandingStreamCallbacks; i++ {
		callbacks.Add(1)
		go func() {
			defer callbacks.Done()
			err, _ := invokeStreamCallback(context.Background(), func(context.Context, string) error {
				started <- struct{}{}
				<-release
				return nil
			}, "blocked")
			results <- err
		}()
	}
	for i := 0; i < maxOutstandingStreamCallbacks; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			unblock()
			callbacks.Wait()
			t.Fatal("callback slot was not started")
		}
	}
	if err, _ := invokeStreamCallback(context.Background(), func(context.Context, string) error { return nil }, "overflow"); !errors.Is(err, ErrStreamCallbackCapacity) {
		unblock()
		callbacks.Wait()
		t.Fatalf("overflow callback error = %v, want ErrStreamCallbackCapacity", err)
	}
	unblock()
	callbacks.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("callback error = %v", err)
		}
	}
}

func writeStreamChunk(w http.ResponseWriter, data string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
