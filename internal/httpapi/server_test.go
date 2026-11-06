package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

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
			handler := NewHandler(fakePinger{err: tt.err}, frontendFiles())
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
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
	handler := NewHandler(fakePinger{}, frontendFiles())
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
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
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

func frontendFiles() fs.FS {
	return fstest.MapFS{
		"generated/index.html":                 &fstest.MapFile{Data: []byte("<main>Retain</main>")},
		"generated/assets/app-a1b2c3d4.js":     &fstest.MapFile{Data: []byte("export {}")},
		"generated/assets/logo.svg":            &fstest.MapFile{Data: []byte("<svg />")},
		"generated/.gitkeep":                   &fstest.MapFile{Data: []byte("sentinel")},
		"generated/another/deeply/nested/file": &fstest.MapFile{Data: []byte("not served")},
	}
}
