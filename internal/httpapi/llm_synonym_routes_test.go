package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/llmservice"
)

type routeSynonymFake struct {
	enabled  bool
	response llmservice.SynonymResponse
	err      error
	calls    int
	userID   int64
	request  llmservice.SynonymRequest
}

func (f *routeSynonymFake) Enabled() bool { return f.enabled }

func (f *routeSynonymFake) Check(_ context.Context, userID int64, request llmservice.SynonymRequest) (llmservice.SynonymResponse, error) {
	f.calls++
	f.userID, f.request = userID, request
	return f.response, f.err
}

type llmSynonymRouteFixture struct {
	auth   *auth.Service
	cookie *http.Cookie
	userID int64
}

func newLLMSynonymRouteFixture(t *testing.T) *llmSynonymRouteFixture {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "llm-synonym-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	service := auth.NewService(database, clock.Real{})
	if _, err := service.CreateAdmin(context.Background(), "llm-synonym@example.test", "LLM Synonym", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := service.Login(context.Background(), auth.LoginInput{
		Email: "llm-synonym@example.test", Password: "correct horse battery staple",
	}, "llm-synonym-route-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return &llmSynonymRouteFixture{
		auth: service, cookie: &http.Cookie{Name: sessionCookieName, Value: login.Token}, userID: login.User.ID,
	}
}

func (f *llmSynonymRouteFixture) handler(service llmSynonymService) http.Handler {
	mux := http.NewServeMux()
	RegisterLLMSynonymRoutes(mux, f.auth, service)
	return mux
}

func llmSynonymRouteRequest(handler http.Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/llm/check-answer", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLLMSynonymRouteContractAndAuthentication(t *testing.T) {
	fixture := newLLMSynonymRouteFixture(t)
	service := &routeSynonymFake{enabled: true, response: llmservice.SynonymResponse{
		IsSynonym: true, Explanation: "Tak, to naturalny odpowiednik.",
	}}
	handler := fixture.handler(service)

	unauthenticated := llmSynonymRouteRequest(handler, `{"card_id":42,"answer":"kocur"}`, nil)
	if unauthenticated.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("unauthenticated POST = %d %s, calls=%d; want 401 without service call", unauthenticated.Code, unauthenticated.Body, service.calls)
	}
	response := llmSynonymRouteRequest(handler, `{"card_id":42,"answer":"kocur"}`, fixture.cookie)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"is_synonym":true,"explanation":"Tak, to naturalny odpowiednik."}` {
		t.Fatalf("POST synonym-check = %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" || service.calls != 1 ||
		service.userID != fixture.userID || service.request.CardID != 42 || service.request.Answer != "kocur" {
		t.Errorf("response headers=%v service calls=%d user=%d request=%+v", response.Header(), service.calls, service.userID, service.request)
	}
}

func TestLLMSynonymRouteStrictRequestAndOwnerScopedErrors(t *testing.T) {
	fixture := newLLMSynonymRouteFixture(t)
	service := &routeSynonymFake{enabled: true}
	handler := fixture.handler(service)
	for _, test := range []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "unknown field", body: `{"card_id":1,"answer":"x","model":"client"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
		{name: "client direction rejected", body: `{"card_id":1,"direction":"reverse","answer":"x"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
		{name: "wrong type", body: `{"card_id":"1","answer":"x"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
		{name: "missing card id", body: `{"answer":"x"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "blank answer", body: `{"card_id":1,"answer":"  "}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := llmSynonymRouteRequest(handler, test.body, fixture.cookie)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("POST body %s = %d %s, want %d/code %s", test.body, response.Code, response.Body, test.wantStatus, test.wantCode)
			}
		})
	}
	if service.calls != 0 {
		t.Errorf("invalid requests reached service %d times", service.calls)
	}

	service.err = llmservice.ErrNoteNotFound
	response := llmSynonymRouteRequest(handler, `{"card_id":42,"answer":"Katze"}`, fixture.cookie)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) || service.request.CardID != 42 {
		t.Fatalf("owner-scoped not-found response = %d %s, request=%+v", response.Code, response.Body, service.request)
	}
	service.err = errors.New("secret provider response")
	response = llmSynonymRouteRequest(handler, `{"card_id":42,"answer":"Katze"}`, fixture.cookie)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"llm_unavailable"`) || strings.Contains(response.Body.String(), "secret provider") {
		t.Errorf("provider error was not sanitized: %d %s", response.Code, response.Body)
	}
}

func TestLLMSynonymRouteDisabledFeature(t *testing.T) {
	fixture := newLLMSynonymRouteFixture(t)
	response := llmSynonymRouteRequest(fixture.handler(&routeSynonymFake{}), `{"card_id":42,"answer":"x"}`, nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"feature_disabled"`) {
		t.Fatalf("disabled route response = %d %s", response.Code, response.Body)
	}
}
