package httpapi

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const testPublicOrigin = "http://localhost:5173"

type fakePinger struct {
	err error
}

func (p fakePinger) PingContext(context.Context) error {
	return p.err
}

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "healthy", wantStatus: http.StatusOK, wantBody: "ok\n"},
		{name: "database error", err: errors.New("secret database details"), wantStatus: http.StatusServiceUnavailable, wantBody: "service unavailable\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(testPublicOrigin, fakePinger{err: tt.err}, nil, frontendFiles())
			response := httptest.NewRecorder()
			request := newServerRequest(http.MethodGet, "/healthz")
			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if response.Body.String() != tt.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), tt.wantBody)
			}
			if strings.Contains(response.Body.String(), "secret database details") {
				t.Fatal("health response leaked database error details")
			}
			if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
				t.Errorf("Content-Type = %q, want text/plain", got)
			}
		})
	}
}

func TestStaticHandler(t *testing.T) {
	handler := NewHandler(testPublicOrigin, fakePinger{}, nil, frontendFiles())
	tests := []struct {
		name          string
		path          string
		wantStatus    int
		wantBody      string
		wantCache     string
		wantContentTy string
	}{
		{name: "root document", path: "/", wantStatus: http.StatusOK, wantBody: "<main>Retain</main>", wantCache: "no-cache", wantContentTy: "text/html; charset=utf-8"},
		{name: "explicit document", path: "/index.html", wantStatus: http.StatusOK, wantBody: "<main>Retain</main>", wantCache: "no-cache", wantContentTy: "text/html; charset=utf-8"},
		{name: "hashed asset", path: "/assets/app-a1b2c3d4.js", wantStatus: http.StatusOK, wantBody: "export {}", wantCache: "public, max-age=31536000, immutable"},
		{name: "unhashed asset", path: "/assets/logo.svg", wantStatus: http.StatusOK, wantBody: "<svg />", wantCache: "no-cache"},
		{name: "health path does not fall back", path: "/healthz", wantStatus: http.StatusOK, wantBody: "ok\n"},
		{name: "unknown path does not fall back", path: "/missing", wantStatus: http.StatusNotFound},
		{name: "sentinel is not public", path: "/.gitkeep", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := newServerRequest(http.MethodGet, tt.path)
			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && response.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), tt.wantBody)
			}
			if tt.wantCache != "" && response.Header().Get("Cache-Control") != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", response.Header().Get("Cache-Control"), tt.wantCache)
			}
			if tt.wantContentTy != "" && response.Header().Get("Content-Type") != tt.wantContentTy {
				t.Errorf("Content-Type = %q, want %q", response.Header().Get("Content-Type"), tt.wantContentTy)
			}
		})
	}
}

func TestNewHandlerServesInviteSignupShellAndNotFoundForUnknownPaths(t *testing.T) {
	handler := NewHandler(testPublicOrigin, fakePinger{}, nil, frontendFiles())
	tests := []struct {
		name          string
		path          string
		wantStatus    int
		wantBody      string
		wantContentTy string
	}{
		{
			name:          "invite signup shell",
			path:          "/signup?invite=invite-code",
			wantStatus:    http.StatusOK,
			wantBody:      "<main>Retain</main>",
			wantContentTy: "text/html; charset=utf-8",
		},
		{name: "unknown study route", path: "/study", wantStatus: http.StatusNotFound},
		{name: "missing asset", path: "/assets/missing-a1b2c3d4.js", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := newServerRequest(http.MethodGet, tt.path)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", response.Code, tt.wantStatus, response.Body.String())
			}
			if tt.wantBody != "" && response.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), tt.wantBody)
			}
			if tt.wantContentTy != "" && response.Header().Get("Content-Type") != tt.wantContentTy {
				t.Errorf("Content-Type = %q, want %q", response.Header().Get("Content-Type"), tt.wantContentTy)
			}
			if tt.name == "invite signup shell" && response.Header().Get("Location") != "" {
				t.Errorf("signup response redirected to %q; want query-preserving shell response", response.Header().Get("Location"))
			}
		})
	}
}

func TestHandlerRequiresSameOriginForStateChangingRequests(t *testing.T) {
	tests := []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "missing Origin", wantStatus: http.StatusForbidden},
		{name: "spoofed Origin", origin: "http://attacker.example", wantStatus: http.StatusForbidden},
		{name: "matching Origin on Vite port passes guard", origin: testPublicOrigin, wantStatus: http.StatusMethodNotAllowed},
		{name: "backend port in Origin does not match Vite port", origin: "http://localhost:8080", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(testPublicOrigin, fakePinger{}, nil, frontendFiles())
			request := newServerRequest(http.MethodPost, "/future-state-change")
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", response.Code, tt.wantStatus, response.Body.String())
			}
		})
	}
}

func TestHandlerRejectsWrongHostEvenForSafeRequests(t *testing.T) {
	handler := NewHandler(testPublicOrigin, fakePinger{}, nil, frontendFiles())
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestNewHandlerRegistersAccountRoutesBehindCanonicalOriginGuard(t *testing.T) {
	service, database, _ := newAccountHandlerTest(t)
	adminID, err := service.CreateAdmin(context.Background(), "admin@example.test", "Admin", handlerTestPassword)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	invite, err := service.CreateInvite(context.Background(), adminID)
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	handler := NewHandler(testPublicOrigin, database.SQL, service, frontendFiles())
	registerBody := `{"invite":"` + invite.Code + `","email":"learner@example.test","name":"Learner","password":"` + handlerTestPassword + `"}`

	newRequest := func(method, target, body, requestOrigin string) *httptest.ResponseRecorder {
		t.Helper()
		request := newServerRequest(method, target)
		if body != "" {
			request.Body = io.NopCloser(strings.NewReader(body))
			request.ContentLength = int64(len(body))
			request.Header.Set("Content-Type", "application/json")
		}
		if requestOrigin != "" {
			request.Header.Set("Origin", requestOrigin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	blocked := newRequest(http.MethodPost, "/api/v1/auth/register", registerBody, "")
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("registration without Origin status = %d, want %d (body %q)", blocked.Code, http.StatusForbidden, blocked.Body.String())
	}

	registered := newRequest(http.MethodPost, "/api/v1/auth/register", registerBody, testPublicOrigin)
	if registered.Code != http.StatusCreated {
		t.Fatalf("registration with canonical Origin/Host status = %d, want %d (body %q)", registered.Code, http.StatusCreated, registered.Body.String())
	}

	loginBody := `{"email":"learner@example.test","password":"` + handlerTestPassword + `"}`
	login := newRequest(http.MethodPost, "/api/v1/auth/login", loginBody, testPublicOrigin)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, want %d (body %q)", login.Code, http.StatusNoContent, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want one", len(cookies))
	}

	meRequest := newServerRequest(http.MethodGet, "/api/v1/me")
	meRequest.AddCookie(cookies[0])
	me := httptest.NewRecorder()
	handler.ServeHTTP(me, meRequest)
	if me.Code != http.StatusOK {
		t.Fatalf("authenticated /me status = %d, want %d (body %q)", me.Code, http.StatusOK, me.Body.String())
	}
}

func newServerRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, "http://localhost:5173"+path, nil)
	request.Host = "localhost:5173"
	return request
}

func frontendFiles() fs.FS {
	return fstest.MapFS{
		"generated/index.html":                 &fstest.MapFile{Data: []byte("<main>Retain</main>")},
		"generated/assets/app-a1b2c3d4.js":     &fstest.MapFile{Data: []byte("export {}")},
		"generated/assets/logo.svg":            &fstest.MapFile{Data: []byte("<svg />")},
		"generated/.gitkeep":                   &fstest.MapFile{Data: []byte("sentinel")},
		"generated/another/deeply/nested/file": &fstest.MapFile{Data: []byte("not served")},
	}
}
