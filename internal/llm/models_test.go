package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListModelsReturnsSortedUniqueIDsAndUsesServerKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer server-test-key" {
			t.Errorf("Authorization = %q, want server-side key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"z-model","object":"model","owned_by":"first"},{"id":"a-model","object":"model","owned_by":"provider"},{"id":"z-model","object":"model","owned_by":"duplicate"},{"id":"","object":"model","owned_by":"provider"},{"id":"   ","object":"model","owned_by":"provider"}]}`)
	}))
	defer server.Close()

	got, err := newTestClient(t, server.URL+"/v1").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	want := []string{"a-model", "z-model"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListModels() = %#v, want %#v", got, want)
	}
}

func TestListModelsUsesConfiguredOverallTimeoutAndBreaker(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.Timeout = 30 * time.Millisecond
		config.CircuitThreshold = 1
	})

	_, err := client.ListModels(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListModels() error = %v, want configured deadline", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	if _, err := client.ListModels(context.Background()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("ListModels() after timeout = %v, want open circuit", err)
	}
	if calls.Load() != 1 {
		t.Errorf("provider calls = %d, want 1", calls.Load())
	}
}

func TestListModelsRetries429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"rate_limit_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"available"}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.MaxRetries = 1
	})

	got, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"available"}) {
		t.Errorf("models = %v, want [available]", got)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want 2", calls.Load())
	}
}

func TestListModelsCallerDeadlineCancelsRetryBackoffWithoutBreakerFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"rate_limit_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"available"}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.MaxRetries = 2
		config.InitialBackoff = time.Second
		config.MaxBackoff = time.Second
		config.CircuitThreshold = 1
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.ListModels(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListModels() with caller deadline error = %v, want context.DeadlineExceeded", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls before retry backoff deadline = %d, want 1", calls.Load())
	}

	if got, err := client.ListModels(context.Background()); err != nil || !reflect.DeepEqual(got, []string{"available"}) {
		t.Fatalf("ListModels() after caller deadline = %v, %v; want [available], nil", got, err)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want 2", calls.Load())
	}
}

func TestListModelsExhausted429And5xxOpenBreaker(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"provider secret","type":"server_error"}}`)
			}))
			defer server.Close()
			client := newTestClient(t, server.URL+"/v1", func(config *Config) {
				config.MaxRetries = 2
				config.CircuitThreshold = 1
			})

			_, err := client.ListModels(context.Background())
			var upstream *UpstreamError
			if !errors.As(err, &upstream) || upstream.StatusCode != status {
				t.Fatalf("ListModels() error = %v, want sanitized HTTP %d", err, status)
			}
			if strings.Contains(err.Error(), "provider secret") {
				t.Errorf("provider error exposed response body: %v", err)
			}
			if calls.Load() != 3 {
				t.Fatalf("provider calls = %d, want initial attempt plus 2 retries", calls.Load())
			}
			if _, err := client.ListModels(context.Background()); !errors.Is(err, ErrCircuitOpen) {
				t.Errorf("ListModels() after exhausted retries = %v, want open circuit", err)
			}
			if calls.Load() != 3 {
				t.Errorf("provider calls after open circuit = %d, want 3", calls.Load())
			}
		})
	}
}

func TestListModelsNonRetryable4xxDoesNotFailBreaker(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"provider secret","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"available"}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
	})

	_, err := client.ListModels(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusBadRequest {
		t.Fatalf("ListModels() error = %v, want sanitized HTTP 400", err)
	}
	if strings.Contains(err.Error(), "provider secret") {
		t.Errorf("provider error exposed response body: %v", err)
	}
	if got, err := client.ListModels(context.Background()); err != nil || !reflect.DeepEqual(got, []string{"available"}) {
		t.Fatalf("ListModels() after HTTP 400 = %v, %v; want [available], nil", got, err)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want 2", calls.Load())
	}
}

func TestListModelsSanitizesTransportErrorAndOpensBreaker(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("HTTP server does not support connection hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack provider connection: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
	})

	_, err := client.ListModels(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != 0 {
		t.Fatalf("ListModels() error = %v, want sanitized transport error", err)
	}
	if err.Error() != "llm request failed" {
		t.Errorf("transport error = %q, want sanitized message", err)
	}
	if _, err := client.ListModels(context.Background()); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("ListModels() after transport failure = %v, want open circuit", err)
	}
	if calls.Load() != 1 {
		t.Errorf("provider calls = %d, want 1", calls.Load())
	}
}

func TestListModelsCallerCancellationDoesNotCountAsProviderFailure(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"available"}]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.ListModels(ctx)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ListModels() error = %v, want context.Canceled", err)
	}

	// The next call gets through the breaker, confirming caller cancellation was not
	// recorded as a provider failure.
	got, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() after caller cancellation = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"available"}) {
		t.Errorf("models = %v, want [available]", got)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want 2", calls.Load())
	}
}

func TestListModelsCancellationReleasesHalfOpenLease(t *testing.T) {
	var calls atomic.Int32
	probeStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("HTTP server does not support connection hijacking")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack provider connection: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		close(probeStarted)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
		config.CircuitCooldown = time.Minute
	})
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("initial ListModels() error = nil, want provider failure")
	}
	client.breaker.mu.Lock()
	client.breaker.openUntil = time.Now().Add(-time.Second)
	client.breaker.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.ListModels(ctx)
		done <- err
	}()
	select {
	case <-probeStarted:
	case <-time.After(time.Second):
		t.Fatal("half-open model-list probe did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled half-open ListModels() error = %v, want context.Canceled", err)
	}
	client.breaker.mu.Lock()
	state := client.breaker.state
	client.breaker.mu.Unlock()
	if state != breakerOpen {
		t.Fatalf("breaker state after canceled half-open probe = %v, want open", state)
	}
	if _, err := client.ListModels(context.Background()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("ListModels() after canceled half-open probe = %v, want open circuit", err)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want 2", calls.Load())
	}
}

func TestListModelsSanitizesProviderErrorsAndOpensBreaker(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"server-test-key and request body secret","type":"server_error"}}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1", func(config *Config) {
		config.CircuitThreshold = 1
	})

	_, err := client.ListModels(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ListModels() error = %v, want sanitized 503", err)
	}
	if strings.Contains(err.Error(), "server-test-key") || strings.Contains(err.Error(), "request body secret") {
		t.Errorf("provider error exposed sensitive data: %v", err)
	}
	if _, err := client.ListModels(context.Background()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("ListModels() after provider failure = %v, want open circuit", err)
	}
	if calls.Load() != 2 {
		t.Errorf("provider calls = %d, want 2", calls.Load())
	}
}
