package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/llmservice"
)

type routeAskFake struct {
	enabled bool
	ask     func(context.Context, int64, llmservice.AskRequest, func(context.Context, string) error) error
}

func (f *routeAskFake) Enabled() bool { return f.enabled }

func (f *routeAskFake) Ask(ctx context.Context, userID int64, request llmservice.AskRequest, onDelta func(context.Context, string) error) error {
	if f.ask == nil {
		return nil
	}
	return f.ask(ctx, userID, request, onDelta)
}

type llmAskRouteFixture struct {
	database *db.DB
	auth     *auth.Service
	cookie   *http.Cookie
	userID   int64
}

func newLLMAskRouteFixture(t *testing.T) *llmAskRouteFixture {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "llm-ask-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	service := auth.NewService(database, clock.Real{})
	if _, err := service.CreateAdmin(context.Background(), "llm-ask@example.test", "LLM Ask", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := service.Login(context.Background(), auth.LoginInput{
		Email: "llm-ask@example.test", Password: "correct horse battery staple",
	}, "llm-ask-route-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return &llmAskRouteFixture{
		database: database, auth: service,
		cookie: &http.Cookie{Name: sessionCookieName, Value: login.Token}, userID: login.User.ID,
	}
}

func (f *llmAskRouteFixture) handler(service llmAskService) http.Handler {
	mux := http.NewServeMux()
	RegisterLLMAskRoutes(mux, f.auth, service)
	return mux
}

func newLLMAskRequest(ctx context.Context, cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/llm/ask", strings.NewReader(`{"note_id":7,"question":"Why is this used?"}`))
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

func TestLLMAskRouteStreamsJSONTokenEventsAndDone(t *testing.T) {
	fixture := newLLMAskRouteFixture(t)
	var calls atomic.Int32
	service := &routeAskFake{enabled: true, ask: func(ctx context.Context, userID int64, request llmservice.AskRequest, onDelta func(context.Context, string) error) error {
		calls.Add(1)
		if userID != fixture.userID || request.NoteID != 7 || request.Question != "Why is this used?" {
			t.Errorf("Ask() arguments = user %d, request %+v", userID, request)
		}
		for _, token := range []string{"First", " line\n", "終わり"} {
			if err := onDelta(ctx, token); err != nil {
				return err
			}
		}
		return nil
	}}
	response := httptest.NewRecorder()
	fixture.handler(service).ServeHTTP(response, newLLMAskRequest(context.Background(), fixture.cookie))

	if response.Code != http.StatusOK {
		t.Fatalf("ask response status = %d, body %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		response.Header().Get("Cache-Control") != "no-cache, no-transform" ||
		response.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("SSE headers = %v", response.Header())
	}
	body := response.Body.String()
	for _, expected := range []string{
		"event: token\ndata: {\"text\":\"First\"}\n\n",
		"event: token\ndata: {\"text\":\" line\\n\"}\n\n",
		"event: token\ndata: {\"text\":\"終わり\"}\n\n",
		"event: done\ndata: {}\n\n",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("SSE body %q missing frame %q", body, expected)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("Ask() calls = %d, want 1", calls.Load())
	}
}

func TestLLMAskRouteMapsAskErrorsToSafeEventsAndHeartbeats(t *testing.T) {
	t.Run("ask errors are mapped without exposing details", func(t *testing.T) {
		tests := []struct {
			name       string
			err        error
			wantEvent  string
			wantHidden string
		}{
			{
				name:       "unexpected provider error",
				err:        errors.New("provider response included private data and API key"),
				wantEvent:  "event: error\ndata: {\"error\":{\"code\":\"llm_unavailable\",\"message\":\"The assistant could not answer right now. Try again.\"}}\n\n",
				wantHidden: "private data",
			},
			{
				name:      "daily limit",
				err:       llmservice.ErrDailyLimit,
				wantEvent: "event: error\ndata: {\"error\":{\"code\":\"daily_limit_reached\",\"message\":\"The daily assistant usage limit has been reached.\"}}\n\n",
			},
			{
				name:      "note not found",
				err:       llmservice.ErrNoteNotFound,
				wantEvent: "event: error\ndata: {\"error\":{\"code\":\"not_found\",\"message\":\"The requested note was not found.\"}}\n\n",
			},
			{
				name:       "internal error",
				err:        errors.Join(llmservice.ErrInternal, errors.New("database path and private details")),
				wantEvent:  "event: error\ndata: {\"error\":{\"code\":\"internal_error\",\"message\":\"An internal error occurred.\"}}\n\n",
				wantHidden: "database path",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				fixture := newLLMAskRouteFixture(t)
				service := &routeAskFake{enabled: true, ask: func(context.Context, int64, llmservice.AskRequest, func(context.Context, string) error) error {
					return tt.err
				}}
				response := httptest.NewRecorder()
				fixture.handler(service).ServeHTTP(response, newLLMAskRequest(context.Background(), fixture.cookie))
				body := response.Body.String()
				if !strings.Contains(body, tt.wantEvent) {
					t.Fatalf("error event body = %q, want frame %q", body, tt.wantEvent)
				}
				if tt.wantHidden != "" && strings.Contains(body, tt.wantHidden) {
					t.Errorf("SSE error exposed private details %q: %q", tt.wantHidden, body)
				}
			})
		}
	})

	t.Run("heartbeat while provider waits", func(t *testing.T) {
		fixture := newLLMAskRouteFixture(t)
		started := make(chan struct{})
		release := make(chan struct{})
		service := &routeAskFake{enabled: true, ask: func(ctx context.Context, _ int64, _ llmservice.AskRequest, _ func(context.Context, string) error) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			serveLLMAsk(response, newLLMAskRequest(context.Background(), fixture.cookie), fixture.auth, service, time.Millisecond)
			close(done)
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("Ask() did not start")
		}
		// Let the short test heartbeat ticker fire while the fake provider is blocked.
		time.Sleep(10 * time.Millisecond)
		close(release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("ask handler did not finish")
		}
		if !strings.Contains(response.Body.String(), ": keep-alive\n\n") {
			t.Fatal("no SSE heartbeat while provider was waiting")
		}
	})
}

func TestLLMAskRouteCancellationAndAuthentication(t *testing.T) {
	t.Run("invalid request is rejected before SSE starts", func(t *testing.T) {
		fixture := newLLMAskRouteFixture(t)
		var calls atomic.Int32
		service := &routeAskFake{enabled: true, ask: func(context.Context, int64, llmservice.AskRequest, func(context.Context, string) error) error {
			calls.Add(1)
			return nil
		}}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/llm/ask", strings.NewReader(`{"note_id":0,"question":""}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(fixture.cookie)
		response := httptest.NewRecorder()
		fixture.handler(service).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_input"`) {
			t.Fatalf("invalid ask response = %d/%s, want 400 invalid_input", response.Code, response.Body)
		}
		if response.Header().Get("Content-Type") == "text/event-stream; charset=utf-8" || calls.Load() != 0 {
			t.Errorf("invalid request started stream: headers=%v calls=%d", response.Header(), calls.Load())
		}
	})

	t.Run("client cancellation stops handler", func(t *testing.T) {
		fixture := newLLMAskRouteFixture(t)
		started := make(chan struct{})
		service := &routeAskFake{enabled: true, ask: func(ctx context.Context, _ int64, _ llmservice.AskRequest, onDelta func(context.Context, string) error) error {
			if err := onDelta(ctx, "before disconnect"); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}
		ctx, cancel := context.WithCancel(context.Background())
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			fixture.handler(service).ServeHTTP(response, newLLMAskRequest(ctx, fixture.cookie))
			close(done)
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("Ask() did not start")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handler did not stop after client cancellation")
		}
		if response.Code != http.StatusOK {
			t.Errorf("canceled stream status = %d, want 200 SSE", response.Code)
		}
	})

	t.Run("authentication required", func(t *testing.T) {
		fixture := newLLMAskRouteFixture(t)
		var calls atomic.Int32
		service := &routeAskFake{enabled: true, ask: func(context.Context, int64, llmservice.AskRequest, func(context.Context, string) error) error {
			calls.Add(1)
			return nil
		}}
		response := httptest.NewRecorder()
		fixture.handler(service).ServeHTTP(response, newLLMAskRequest(context.Background(), nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated ask status = %d, body %s", response.Code, response.Body)
		}
		if calls.Load() != 0 {
			t.Errorf("Ask() called %d times without authentication", calls.Load())
		}
	})
}
