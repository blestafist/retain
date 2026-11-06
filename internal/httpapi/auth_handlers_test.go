package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
)

const handlerTestPassword = "correct horse battery staple"

func newAccountHandlerTest(t *testing.T) (*auth.Service, *db.DB, http.Handler) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "retain.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	service := auth.NewService(database, clock.Real{})
	mux := http.NewServeMux()
	RegisterAccountRoutes(mux, service)
	return service, database, mux
}

func accountRequest(handler http.Handler, method, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestAccountRoutesRegistrationLoginMeSettingsAndLogout(t *testing.T) {
	service, _, handler := newAccountHandlerTest(t)
	adminID, err := service.CreateAdmin(context.Background(), "admin@example.com", "Admin", handlerTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite, err := service.CreateInvite(context.Background(), adminID)
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}

	register := accountRequest(handler, http.MethodPost, "/api/v1/auth/register", `{"invite":"`+invite.Code+`","email":"user@example.com","name":"Learner","password":"`+handlerTestPassword+`"}`)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body %s", register.Code, register.Body)
	}
	if len(register.Result().Cookies()) != 0 || register.Header().Get("Set-Cookie") != "" {
		t.Fatalf("registration unexpectedly set cookie: %q", register.Header().Get("Set-Cookie"))
	}
	var registered auth.User
	if err := json.Unmarshal(register.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if registered.ID == 0 || registered.Email != "user@example.com" || strings.Contains(register.Body.String(), "password") {
		t.Errorf("unexpected public registration response: %s", register.Body)
	}

	login := accountRequest(handler, http.MethodPost, "/api/v1/auth/login", `{"email":"user@example.com","password":"`+handlerTestPassword+`"}`)
	if login.Code != http.StatusNoContent || login.Body.Len() != 0 {
		t.Fatalf("login status/body = %d/%q", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %v, want one", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Value == "" || cookie.Path != sessionCookiePath ||
		!cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != int((30*24*time.Hour).Seconds()) {
		t.Errorf("login cookie flags = %+v", cookie)
	}
	if got := cookie.Expires.Sub(time.Now()); got < 29*24*time.Hour || got > 31*24*time.Hour {
		t.Errorf("cookie expiry is not about 30 days away: %v", got)
	}
	if strings.Contains(login.Body.String(), cookie.Value) || strings.Contains(login.Body.String(), `"token"`) {
		t.Errorf("login exposed token in body: %s", login.Body)
	}

	unauthorized := accountRequest(handler, http.MethodGet, "/api/v1/me", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /me status = %d", unauthorized.Code)
	}
	me := accountRequest(handler, http.MethodGet, "/api/v1/me", "", cookie)
	if me.Code != http.StatusOK {
		t.Fatalf("authenticated /me status = %d, body %s", me.Code, me.Body)
	}
	var account struct {
		User     auth.User     `json:"user"`
		Settings auth.Settings `json:"settings"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode /me response: %v", err)
	}
	if account.User.ID != registered.ID || account.Settings.UserID != registered.ID {
		t.Errorf("/me response is not the expected account: %+v", account)
	}

	patch := accountRequest(handler, http.MethodPatch, "/api/v1/me/settings", `{"new_per_day":31,"timezone":"Europe/Warsaw"}`, cookie)
	if patch.Code != http.StatusOK {
		t.Fatalf("settings PATCH status = %d, body %s", patch.Code, patch.Body)
	}
	var settings auth.Settings
	if err := json.Unmarshal(patch.Body.Bytes(), &settings); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if settings.NewPerDay != 31 || settings.Timezone != "Europe/Warsaw" || settings.UserID != registered.ID {
		t.Errorf("settings PATCH did not return updated full settings: %+v", settings)
	}

	adminLogin := accountRequest(handler, http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.com","password":"`+handlerTestPassword+`"}`)
	if adminLogin.Code != http.StatusNoContent {
		t.Fatalf("admin login status = %d, body %s", adminLogin.Code, adminLogin.Body)
	}
	adminCookie := adminLogin.Result().Cookies()[0]
	createdInvite := accountRequest(handler, http.MethodPost, "/api/v1/admin/invites", "", adminCookie)
	if createdInvite.Code != http.StatusCreated {
		t.Fatalf("admin invite status = %d, body %s", createdInvite.Code, createdInvite.Body)
	}
	var inviteResponse struct {
		Code string `json:"code"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(createdInvite.Body.Bytes(), &inviteResponse); err != nil {
		t.Fatalf("decode invite response: %v", err)
	}
	if inviteResponse.Code == "" || inviteResponse.URL != "/signup?invite="+inviteResponse.Code {
		t.Errorf("unexpected invite response: %+v", inviteResponse)
	}

	logout := accountRequest(handler, http.MethodPost, "/api/v1/auth/logout", "", cookie)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, body %s", logout.Code, logout.Body)
	}
	cleared := logout.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != sessionCookieName || cleared[0].MaxAge != -1 || cleared[0].Path != sessionCookiePath {
		t.Errorf("logout cookie not cleared correctly: %+v", cleared)
	}
	if afterLogout := accountRequest(handler, http.MethodGet, "/api/v1/me", "", cookie); afterLogout.Code != http.StatusUnauthorized {
		t.Errorf("/me after logout status = %d", afterLogout.Code)
	}
	if repeated := accountRequest(handler, http.MethodPost, "/api/v1/auth/logout", ""); repeated.Code != http.StatusNoContent {
		t.Errorf("cookie-less logout status = %d, want 204", repeated.Code)
	}
}

func TestAccountRoutesRegularUserCannotCreateInvite(t *testing.T) {
	service, _, handler := newAccountHandlerTest(t)
	adminID, err := service.CreateAdmin(context.Background(), "admin@example.com", "Admin", handlerTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite, err := service.CreateInvite(context.Background(), adminID)
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	register := accountRequest(handler, http.MethodPost, "/api/v1/auth/register", `{"invite":"`+invite.Code+`","email":"user@example.com","name":"Learner","password":"`+handlerTestPassword+`"}`)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body %s", register.Code, register.Body)
	}
	login := accountRequest(handler, http.MethodPost, "/api/v1/auth/login", `{"email":"user@example.com","password":"`+handlerTestPassword+`"}`)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body %s", login.Code, login.Body)
	}
	response := accountRequest(handler, http.MethodPost, "/api/v1/admin/invites", "", login.Result().Cookies()[0])
	if response.Code != http.StatusForbidden {
		t.Errorf("regular-user invite status = %d, body %s", response.Code, response.Body)
	}
}

func TestSettingsPatchRequiresJSONObject(t *testing.T) {
	service, _, handler := newAccountHandlerTest(t)
	adminID, err := service.CreateAdmin(context.Background(), "admin@example.com", "Admin", handlerTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login := accountRequest(handler, http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.com","password":"`+handlerTestPassword+`"}`)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body %s", login.Code, login.Body)
	}
	cookie := login.Result().Cookies()[0]

	emptyPatch := accountRequest(handler, http.MethodPatch, "/api/v1/me/settings", `{}`, cookie)
	if emptyPatch.Code != http.StatusOK {
		t.Fatalf("empty settings PATCH status = %d, body %s; want %d", emptyPatch.Code, emptyPatch.Body, http.StatusOK)
	}
	var settings auth.Settings
	if err := json.Unmarshal(emptyPatch.Body.Bytes(), &settings); err != nil {
		t.Fatalf("decode empty settings PATCH response: %v", err)
	}
	if settings.UserID != adminID {
		t.Errorf("empty settings PATCH user ID = %d, want %d", settings.UserID, adminID)
	}

	for _, body := range []string{`null`, `[]`, `"string"`, `123`, `true`} {
		t.Run(body, func(t *testing.T) {
			response := accountRequest(handler, http.MethodPatch, "/api/v1/me/settings", body, cookie)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("settings PATCH status = %d, body %s; want %d", response.Code, response.Body, http.StatusBadRequest)
			}
			if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode API error response: %v", err)
			}
			if payload.Error.Code != "invalid_json" || payload.Error.Message == "" {
				t.Errorf("API error = %+v, want safe invalid_json error", payload.Error)
			}
		})
	}
}

func TestAccountRoutesSanitizeAndMapErrors(t *testing.T) {
	_, _, handler := newAccountHandlerTest(t)
	tooLarge := accountRequest(handler, http.MethodPost, "/api/v1/auth/login", strings.Repeat(" ", MaxJSONBodyBytes+1))
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body status = %d, body %s", tooLarge.Code, tooLarge.Body)
	}
	tests := []struct {
		name, method, target, body, contentType string
		wantStatus                              int
		wantCode                                string
	}{
		{"malformed JSON", http.MethodPost, "/api/v1/auth/login", "{", "application/json", http.StatusBadRequest, "invalid_json"},
		{"unsupported media type", http.MethodPost, "/api/v1/auth/login", `{}`, "text/plain", http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"invalid invite", http.MethodPost, "/api/v1/auth/register", `{"invite":"missing","email":"user@example.com","name":"User","password":"` + handlerTestPassword + `"}`, "application/json", http.StatusBadRequest, "invalid_invite"},
		{"invalid credentials", http.MethodPost, "/api/v1/auth/login", `{"email":"none@example.com","password":"` + handlerTestPassword + `"}`, "application/json", http.StatusUnauthorized, "unauthenticated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body %s; want %d", response.Code, response.Body, test.wantStatus)
			}
			var payload struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if payload.Error.Code != test.wantCode || payload.Error.Message == "" {
				t.Errorf("error payload = %+v", payload.Error)
			}
			if strings.Contains(response.Body.String(), "database") || strings.Contains(response.Body.String(), "password") {
				t.Errorf("error response leaked operational/input details: %s", response.Body)
			}
		})
	}
}
