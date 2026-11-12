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

type routeMnemonicFake struct {
	enabled  bool
	response llmservice.MnemonicResponse
	err      error
	calls    int
	userID   int64
	request  llmservice.MnemonicRequest
}

func (f *routeMnemonicFake) Enabled() bool { return f.enabled }

func (f *routeMnemonicFake) Mnemonic(_ context.Context, userID int64, request llmservice.MnemonicRequest) (llmservice.MnemonicResponse, error) {
	f.calls++
	f.userID, f.request = userID, request
	return f.response, f.err
}

type llmMnemonicRouteFixture struct {
	auth   *auth.Service
	cookie *http.Cookie
	userID int64
}

func newLLMMnemonicRouteFixture(t *testing.T) *llmMnemonicRouteFixture {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "llm-mnemonic-routes.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	service := auth.NewService(database, clock.Real{})
	if _, err := service.CreateAdmin(context.Background(), "llm-mnemonic@example.test", "LLM Mnemonic", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := service.Login(context.Background(), auth.LoginInput{
		Email: "llm-mnemonic@example.test", Password: "correct horse battery staple",
	}, "llm-mnemonic-route-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return &llmMnemonicRouteFixture{
		auth: service, cookie: &http.Cookie{Name: sessionCookieName, Value: login.Token}, userID: login.User.ID,
	}
}

func (f *llmMnemonicRouteFixture) handler(service llmMnemonicService) http.Handler {
	mux := http.NewServeMux()
	RegisterLLMMnemonicRoutes(mux, f.auth, service)
	return mux
}

func llmMnemonicRouteRequest(handler http.Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/llm/mnemonic", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLLMMnemonicRouteContractAndAuthentication(t *testing.T) {
	fixture := newLLMMnemonicRouteFixture(t)
	service := &routeMnemonicFake{enabled: true, response: llmservice.MnemonicResponse{Mnemonics: []string{"idea one", "idea two"}}}
	handler := fixture.handler(service)

	unauthenticated := llmMnemonicRouteRequest(handler, `{"note_id":42}`, nil)
	if unauthenticated.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("unauthenticated POST = %d %s, calls=%d; want 401 without service call", unauthenticated.Code, unauthenticated.Body, service.calls)
	}
	response := llmMnemonicRouteRequest(handler, `{"note_id":42}`, fixture.cookie)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"mnemonics":["idea one","idea two"]}` {
		t.Fatalf("POST mnemonic = %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" || service.calls != 1 ||
		service.userID != fixture.userID || service.request.NoteID != 42 {
		t.Errorf("response headers=%v service calls=%d user=%d request=%+v", response.Header(), service.calls, service.userID, service.request)
	}
}

func TestLLMMnemonicRouteStrictRequestAndServiceErrors(t *testing.T) {
	fixture := newLLMMnemonicRouteFixture(t)
	service := &routeMnemonicFake{enabled: true}
	handler := fixture.handler(service)
	for _, test := range []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "unknown field", body: `{"note_id":1,"model":"client"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
		{name: "wrong type", body: `{"note_id":"1"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
		{name: "fractional ID", body: `{"note_id":1.5}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_json"},
		{name: "missing ID", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "null ID", body: `{"note_id":null}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "non-positive ID", body: `{"note_id":0}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := llmMnemonicRouteRequest(handler, test.body, fixture.cookie)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("POST body %s = %d %s, want %d %s", test.body, response.Code, response.Body, test.wantStatus, test.wantCode)
			}
		})
	}
	if service.calls != 0 {
		t.Errorf("invalid requests reached service %d times", service.calls)
	}

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantHidden string
	}{
		{name: "owner scoped not found", err: llmservice.ErrNoteNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "unsupported pair", err: llmservice.ErrUnsupportedPair, wantStatus: http.StatusUnprocessableEntity, wantCode: "unsupported_language_pair"},
		{name: "quota", err: llmservice.ErrDailyLimit, wantStatus: http.StatusTooManyRequests, wantCode: "daily_limit_reached"},
		{name: "provider failure sanitized", err: errors.New("secret-token provider body"), wantStatus: http.StatusServiceUnavailable, wantCode: "llm_unavailable", wantHidden: "secret-token"},
		{name: "internal failure sanitized", err: errors.Join(llmservice.ErrInternal, errors.New("database secret")), wantStatus: http.StatusInternalServerError, wantCode: "internal_error", wantHidden: "database secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service.err = test.err
			response := llmMnemonicRouteRequest(handler, `{"note_id":42}`, fixture.cookie)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("service error response = %d %s, want %d/code %s", response.Code, response.Body, test.wantStatus, test.wantCode)
			}
			if test.wantHidden != "" && strings.Contains(response.Body.String(), test.wantHidden) {
				t.Errorf("error body leaked %q: %s", test.wantHidden, response.Body)
			}
		})
	}
}

func TestLLMMnemonicRouteDisabledFeature(t *testing.T) {
	fixture := newLLMMnemonicRouteFixture(t)
	response := llmMnemonicRouteRequest(fixture.handler(&routeMnemonicFake{}), `{"note_id":42}`, nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"feature_disabled"`) {
		t.Fatalf("disabled route response = %d %s", response.Code, response.Body)
	}
}
