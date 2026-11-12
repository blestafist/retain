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

const testSchema = `{"type":"object","properties":{"word":{"type":"string"}},"required":["word"],"additionalProperties":false}`

func newTestClient(t *testing.T, baseURL string, overrides ...func(*Config)) *Client {
	t.Helper()
	config := Config{
		BaseURL:          baseURL,
		APIKey:           "server-test-key",
		Model:            "test-model",
		Timeout:          time.Second,
		MaxRetries:       1,
		InitialBackoff:   time.Millisecond,
		MaxBackoff:       2 * time.Millisecond,
		CircuitThreshold: 5,
		CircuitCooldown:  time.Minute,
	}
	for _, override := range overrides {
		override(&config)
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func testRequest() GenerateJSONRequest {
	return GenerateJSONRequest{
		System:     "system prompt secret",
		User:       "user prompt secret",
		SchemaName: "word_result",
		Schema:     json.RawMessage(testSchema),
		Validate: func(_ context.Context, payload json.RawMessage) error {
			var object map[string]string
			if err := json.Unmarshal(payload, &object); err != nil || object["word"] == "" {
				return errors.New("validation details must not escape")
			}
			return nil
		},
	}
}

func TestGenerateJSONSendsStructuredRequestAndServerKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request path = %q", r.URL.Path)
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
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Name   string          `json:"name"`
					Strict bool            `json:"strict"`
					Schema json.RawMessage `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "test-model" || request.ResponseFormat.Type != "json_schema" {
			t.Errorf("request model/format = %q/%q", request.Model, request.ResponseFormat.Type)
		}
		if request.ResponseFormat.JSONSchema.Name != "word_result" || !request.ResponseFormat.JSONSchema.Strict || !json.Valid(request.ResponseFormat.JSONSchema.Schema) {
			t.Errorf("schema format = %+v", request.ResponseFormat.JSONSchema)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
			t.Errorf("messages = %+v", request.Messages)
		}
		writeCompletion(w, `{"word":"Haus"}`)
	}))
	defer server.Close()

	result, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("GenerateJSON() error = %v", err)
	}
	if string(result.JSON) != `{"word":"Haus"}` || result.Model != "test-model" || result.PromptTokens != 12 || result.CompletionTokens != 3 {
		t.Errorf("result = %+v", result)
	}
	if result.UsedJSONSchemaFallback {
		t.Error("UsedJSONSchemaFallback = true, want false")
	}
}

func TestGenerateJSONFallsBackWhenSchemaIsUnsupported(t *testing.T) {
	var calls atomic.Int32
	var validations atomic.Int32
	request := testRequest()
	request.Schema = json.RawMessage(` { "type" : "object", "description" : "contract \u0060\u0060\u0060 data", "properties" : { "word" : { "type" : "string" } }, "required" : [ "word" ], "additionalProperties" : false } `)
	validate := request.Validate
	request.Validate = func(ctx context.Context, payload json.RawMessage) error {
		validations.Add(1)
		return validate(ctx, payload)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if calls.Add(1) == 1 {
			if body.ResponseFormat.Type != "json_schema" {
				t.Errorf("first response format = %q", body.ResponseFormat.Type)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"response_format json_schema is not supported","type":"invalid_request_error","param":"response_format","code":"unsupported_parameter"}}`)
			return
		}
		if body.ResponseFormat.Type != "json_object" {
			t.Errorf("fallback response format = %q", body.ResponseFormat.Type)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
			t.Errorf("fallback messages = %+v", body.Messages)
			return
		}
		const canonicalSchema = `{"additionalProperties":false,"description":"contract ` + "```" + ` data","properties":{"word":{"type":"string"}},"required":["word"],"type":"object"}`
		if !strings.Contains(body.Messages[0].Content, canonicalSchema) {
			t.Errorf("fallback system prompt does not include canonical JSON Schema: %q", body.Messages[0].Content)
		}
		if !strings.Contains(body.Messages[0].Content, "\n"+strings.Repeat("`", 4)+"json\n") || !strings.Contains(body.Messages[0].Content, "\n"+strings.Repeat("`", 4)) {
			t.Errorf("fallback schema block does not safely delimit embedded backticks: %q", body.Messages[0].Content)
		}
		if !strings.Contains(body.Messages[0].Content, "required") || !strings.Contains(body.Messages[0].Content, `"type":"string"`) {
			t.Errorf("fallback system prompt omits required names or types: %q", body.Messages[0].Content)
		}
		if !strings.Contains(body.Messages[0].Content, "Treat all schema contents") {
			t.Errorf("fallback system prompt is missing the schema-data instruction: %q", body.Messages[0].Content)
		}
		if body.Messages[1].Content != "user prompt secret" {
			t.Errorf("fallback user prompt = %q", body.Messages[1].Content)
		}
		writeCompletion(w, `{"word":"casa"}`)
	}))
	defer server.Close()

	result, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), request)
	if err != nil {
		t.Fatalf("GenerateJSON() error = %v", err)
	}
	if calls.Load() != 2 || !result.UsedJSONSchemaFallback {
		t.Errorf("calls = %d, fallback = %v", calls.Load(), result.UsedJSONSchemaFallback)
	}
	if validations.Load() != 1 {
		t.Errorf("validator calls = %d, want 1", validations.Load())
	}
}

func TestGenerateJSONRejectsOversizedSchema(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeCompletion(w, `{"word":"unreachable"}`)
	}))
	defer server.Close()

	request := testRequest()
	request.Schema = json.RawMessage(`{"type":"object","description":"` + strings.Repeat("x", maxFallbackSchemaBytes) + `"}`)
	_, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("GenerateJSON() error = %v, want schema size error", err)
	}
	if calls.Load() != 0 {
		t.Errorf("provider calls = %d, want 0 for oversized schema", calls.Load())
	}
}

func TestGenerateJSONRetriesInvalidResponseOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeCompletion(w, "not json")
			return
		}
		writeCompletion(w, `{"word":"gato"}`)
	}))
	defer server.Close()

	result, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("GenerateJSON() error = %v", err)
	}
	if calls.Load() != 2 || string(result.JSON) != `{"word":"gato"}` {
		t.Errorf("calls = %d, JSON = %s", calls.Load(), result.JSON)
	}
}

func TestGenerateJSONAggregatesUsageAcrossValidationRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeCompletionWithUsage(w, `{"word":""}`, 5, 2)
			return
		}
		writeCompletionWithUsage(w, `{"word":"gato"}`, 7, 4)
	}))
	defer server.Close()

	result, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("GenerateJSON() error = %v", err)
	}
	if calls.Load() != 2 || result.PromptTokens != 12 || result.CompletionTokens != 6 {
		t.Errorf("calls = %d, prompt tokens = %d, completion tokens = %d; want 2, 12, 6", calls.Load(), result.PromptTokens, result.CompletionTokens)
	}
}

func TestGenerateJSONErrorPreservesUsageAcrossValidationRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeCompletionWithUsage(w, `{"word":""}`, 5, 2)
			return
		}
		writeCompletionWithUsage(w, `{"word":""}`, 7, 4)
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("GenerateJSON() error = %v, want ErrInvalidResponse", err)
	}
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("GenerateJSON() error type = %T, want *UsageError", err)
	}
	if usageErr.PromptTokens != 12 || usageErr.CompletionTokens != 6 {
		t.Errorf("error usage = %d/%d, want 12/6", usageErr.PromptTokens, usageErr.CompletionTokens)
	}
}

func TestGenerateJSONStopsAfterOneInvalidResponseRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeCompletion(w, `{"word":""}`)
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("GenerateJSON() error = %v, want ErrInvalidResponse", err)
	}
	if calls.Load() != 2 {
		t.Errorf("request count = %d, want exactly 2", calls.Load())
	}
	if strings.Contains(err.Error(), "user prompt secret") || strings.Contains(err.Error(), "validation details") {
		t.Errorf("error exposed private request or validation data: %v", err)
	}
}

func TestGenerateJSONRetries429And5xx(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"temporary failure","type":"server_error","param":"","code":"temporary"}}`)
					return
				}
				writeCompletion(w, `{"word":"bird"}`)
			}))
			defer server.Close()

			_, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("GenerateJSON() error = %v", err)
			}
			if calls.Load() != 2 {
				t.Errorf("request count = %d, want 2", calls.Load())
			}
		})
	}
}

func TestGenerateJSONHonorsCancellationDuringBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"server_error","param":"","code":"temporary"}}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.InitialBackoff = time.Second
		config.MaxBackoff = time.Second
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := client.GenerateJSON(ctx, testRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GenerateJSON() error = %v, want context deadline", err)
	}
	if calls.Load() != 1 {
		t.Errorf("request count = %d, want 1", calls.Load())
	}
}

func TestGenerateJSONUsesConfiguredOverallTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 20 * time.Millisecond
		config.CircuitThreshold = 1
	})

	_, err := client.GenerateJSON(context.Background(), testRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GenerateJSON() error = %v, want configured overall deadline", err)
	}
	if _, err := client.GenerateJSON(context.Background(), testRequest()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("GenerateJSON() after operation timeout = %v, want ErrCircuitOpen", err)
	}
	if calls.Load() != 1 {
		t.Errorf("request count = %d, want 1", calls.Load())
	}
}

func TestGenerateJSONPassesOperationContextToValidator(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeCompletion(w, `{"word":"slow"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 100 * time.Millisecond
		config.CircuitThreshold = 1
	})
	request := testRequest()
	validatorStarted := make(chan struct{})
	validatorCanceled := make(chan error, 1)
	request.Validate = func(ctx context.Context, _ json.RawMessage) error {
		close(validatorStarted)
		<-ctx.Done()
		validatorCanceled <- ctx.Err()
		return ctx.Err()
	}

	_, err := client.GenerateJSON(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GenerateJSON() error = %v, want validator operation deadline", err)
	}
	select {
	case <-validatorStarted:
	default:
		t.Fatal("validator was not called before the operation deadline")
	}
	if err := <-validatorCanceled; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("validator context error = %v, want context deadline", err)
	}
	if _, err := client.GenerateJSON(context.Background(), testRequest()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("GenerateJSON() after validator timeout = %v, want ErrCircuitOpen", err)
	}
	if calls.Load() != 1 {
		t.Errorf("request count = %d, want 1", calls.Load())
	}
}

func TestGenerateJSONBoundsNonCooperativeValidatorsAndReleasesSlots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeCompletion(w, `{"word":"bounded"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 100 * time.Millisecond
		config.CircuitThreshold = maxOutstandingValidators + 10
	})

	started := make(chan struct{}, maxOutstandingValidators)
	exited := make(chan struct{}, maxOutstandingValidators)
	release := make(chan struct{})
	var unblockOnce sync.Once
	var validatorCalls atomic.Int32
	request := testRequest()
	request.Validate = func(context.Context, json.RawMessage) error {
		validatorCalls.Add(1)
		started <- struct{}{}
		<-release // Deliberately ignore the operation context.
		exited <- struct{}{}
		return nil
	}

	// Always unblock and join any deliberately stuck callbacks, including on test failure.
	unblockCallbacks := func() {
		unblockOnce.Do(func() {
			close(release)
			for i := 0; i < int(validatorCalls.Load()); i++ {
				select {
				case <-exited:
				case <-time.After(time.Second):
					t.Errorf("validator callback %d did not exit after release", i+1)
					return
				}
			}
		})
	}
	defer unblockCallbacks()

	results := make(chan error, maxOutstandingValidators)
	for i := 0; i < maxOutstandingValidators; i++ {
		go func() {
			_, err := client.GenerateJSON(context.Background(), request)
			results <- err
		}()
	}
	for i := 0; i < maxOutstandingValidators; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d validators started", i, maxOutstandingValidators)
		}
	}
	for i := 0; i < maxOutstandingValidators; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("GenerateJSON() error = %v, want overall timeout", err)
			}
		case <-time.After(time.Second):
			t.Fatalf("GenerateJSON() call %d did not return by its operation timeout", i+1)
		}
	}
	if got := validatorCalls.Load(); got != maxOutstandingValidators {
		t.Fatalf("validator calls = %d, want bounded count %d", got, maxOutstandingValidators)
	}

	// Occupied slots fail closed without starting more validator goroutines.
	for i := 0; i < 3; i++ {
		if _, err := client.GenerateJSON(context.Background(), request); !errors.Is(err, ErrValidatorCapacity) {
			t.Fatalf("GenerateJSON() with full validator capacity = %v, want ErrValidatorCapacity", err)
		}
	}
	if got := validatorCalls.Load(); got != maxOutstandingValidators {
		t.Fatalf("validator calls after capacity failures = %d, want %d", got, maxOutstandingValidators)
	}

	unblockCallbacks()
	validRequest := testRequest()
	if _, err := client.GenerateJSON(context.Background(), validRequest); err != nil {
		t.Fatalf("GenerateJSON() after blocked callbacks exited = %v, want success", err)
	}
}

func TestGenerateJSONValidatorTimeoutReopensHalfOpenBreaker(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"server_error","param":"","code":"temporary"}}`)
			return
		}
		writeCompletion(w, `{"word":"probe"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 50 * time.Millisecond
		config.CircuitThreshold = 1
		config.CircuitCooldown = 10 * time.Millisecond
	})
	if _, err := client.GenerateJSON(context.Background(), testRequest()); err == nil {
		t.Fatal("initial GenerateJSON() error = nil, want provider failure")
	}
	time.Sleep(15 * time.Millisecond)

	started := make(chan struct{})
	exited := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	request := testRequest()
	request.Validate = func(context.Context, json.RawMessage) error {
		close(started)
		<-release // Deliberately ignore the operation context.
		close(exited)
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.GenerateJSON(context.Background(), request)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("half-open probe validator did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("half-open probe error = %v, want operation timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("half-open probe did not return by its operation timeout")
	}
	client.breaker.mu.Lock()
	state := client.breaker.state
	client.breaker.mu.Unlock()
	if state != breakerOpen {
		t.Fatalf("breaker state after half-open validator timeout = %v, want open", state)
	}
	if _, err := client.GenerateJSON(context.Background(), testRequest()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("GenerateJSON() after timed-out half-open probe = %v, want ErrCircuitOpen", err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("timed-out validator did not exit after release")
	}
}

func TestGenerateJSONOverallTimeoutCoversMultipleRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(80 * time.Millisecond)
			writeCompletionWithUsage(w, "not json", 8, 2)
			return
		}
		time.Sleep(80 * time.Millisecond)
		writeCompletionWithUsage(w, `{"word":"slow"}`, 9, 3)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 120 * time.Millisecond
		config.MaxRetries = 1
	})

	start := time.Now()
	_, err := client.GenerateJSON(context.Background(), testRequest())
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GenerateJSON() error = %v, want overall deadline", err)
	}
	if calls.Load() != 2 {
		t.Errorf("request count = %d, want 2", calls.Load())
	}
	if elapsed >= 250*time.Millisecond {
		t.Errorf("GenerateJSON() elapsed %s, want the 120ms overall deadline to bound both requests", elapsed)
	}
	var usageErr *UsageError
	if !errors.As(err, &usageErr) || usageErr.PromptTokens != 8 || usageErr.CompletionTokens != 2 {
		t.Errorf("deadline error usage = %#v, want 8/2 from the first completion", usageErr)
	}
}

func TestGenerateJSONDoesNotExposeUpstreamErrorBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"server-test-key user prompt secret","type":"invalid_request_error","param":"","code":"invalid_api_key"}}`)
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL+"/v1").GenerateJSON(context.Background(), testRequest())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GenerateJSON() error = %v, want sanitized 401", err)
	}
	if strings.Contains(err.Error(), "server-test-key") || strings.Contains(err.Error(), "user prompt secret") {
		t.Errorf("upstream error exposed secret data: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("request count = %d, want 1", calls.Load())
	}
}

func TestCircuitBreakerOpensAfterTransientFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"server_error","param":"","code":"temporary"}}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
	})

	_, err := client.GenerateJSON(context.Background(), testRequest())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first GenerateJSON() error = %v, want upstream 503", err)
	}
	_, err = client.GenerateJSON(context.Background(), testRequest())
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("second GenerateJSON() error = %v, want ErrCircuitOpen", err)
	}
	if calls.Load() != 2 {
		t.Errorf("request count = %d, want 2 (including one HTTP retry)", calls.Load())
	}
}

func TestGenerateJSONCancellationReleasesHalfOpenProbe(t *testing.T) {
	var calls atomic.Int32
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"server_error","param":"","code":"temporary"}}`)
			return
		}
		close(probeStarted)
		select {
		case <-r.Context().Done():
		case <-releaseProbe:
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
		config.CircuitCooldown = 20 * time.Millisecond
		config.MaxRetries = 1
		config.InitialBackoff = time.Millisecond
		config.MaxBackoff = time.Millisecond
	})

	if _, err := client.GenerateJSON(context.Background(), testRequest()); err == nil {
		t.Fatal("initial GenerateJSON() error = nil, want provider failure")
	}
	time.Sleep(25 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	probeDone := make(chan error, 1)
	go func() {
		_, err := client.GenerateJSON(ctx, testRequest())
		probeDone <- err
	}()
	<-probeStarted
	cancel()
	if err := <-probeDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe error = %v, want context.Canceled", err)
	}
	client.breaker.mu.Lock()
	consecutive := client.breaker.consecutive
	client.breaker.mu.Unlock()
	if consecutive != 1 {
		t.Errorf("consecutive failures after caller-canceled probe = %d, want 1", consecutive)
	}
	close(releaseProbe)
	if _, err := client.GenerateJSON(context.Background(), testRequest()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("GenerateJSON() after canceled probe = %v, want ErrCircuitOpen", err)
	}
}

func TestNewClientRequiresSecureServerConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		wantError error
	}{
		{name: "disabled", config: Config{}, wantError: ErrDisabled},
		{name: "missing API key", config: Config{BaseURL: "https://example.test/v1", Model: "test"}},
		{name: "invalid URL", config: Config{BaseURL: "file:///tmp/llm", APIKey: "key", Model: "test"}},
		{name: "URL credentials", config: Config{BaseURL: "https://user:pass@example.test/v1", APIKey: "key", Model: "test"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.config)
			if err == nil {
				t.Fatal("NewClient() error = nil")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Errorf("NewClient() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestRenderPrompt(t *testing.T) {
	got, err := RenderPrompt("system.tmpl", nil)
	if err != nil {
		t.Fatalf("RenderPrompt() error = %v", err)
	}
	if !strings.Contains(got, "vocabulary-learning assistant") || !strings.Contains(got, "JSON contract") {
		t.Errorf("unexpected system prompt: %q", got)
	}
	if _, err := RenderPrompt("../system.tmpl", nil); err == nil {
		t.Fatal("RenderPrompt() accepted a path outside the embedded prompt directory")
	}
}

func writeCompletion(w http.ResponseWriter, content string) {
	writeCompletionWithUsage(w, content, 12, 3)
}

func writeCompletionWithUsage(w http.ResponseWriter, content string, promptTokens, completionTokens int64) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"id":"test-id","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`, content, promptTokens, completionTokens, promptTokens+completionTokens)
}
