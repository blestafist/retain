package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSameOrigin(t *testing.T) {
	tests := []struct {
		name          string
		publicOrigin  string
		method        string
		requestHost   string
		origins       []string
		forwardedHost string
		wantStatus    int
		wantReached   bool
	}{
		{name: "GET without origin", publicOrigin: "https://app.example.test", method: http.MethodGet, requestHost: "app.example.test", wantStatus: http.StatusNoContent, wantReached: true},
		{name: "HEAD with cross-origin origin", publicOrigin: "https://app.example.test", method: http.MethodHead, requestHost: "app.example.test", origins: []string{"https://other.example.test"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "OPTIONS with invalid origin", publicOrigin: "https://app.example.test", method: http.MethodOptions, requestHost: "app.example.test", origins: []string{"null"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "POST matching origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "scheme and hostname case are normalized", publicOrigin: "https://app.example.test", method: http.MethodPatch, requestHost: "APP.EXAMPLE.TEST", origins: []string{"HTTPS://App.Example.Test"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "explicit default port is equivalent", publicOrigin: "https://app.example.test", method: http.MethodPut, requestHost: "APP.EXAMPLE.TEST:443", origins: []string{"https://APP.EXAMPLE.TEST:443"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "HTTP default port is equivalent", publicOrigin: "http://localhost", method: http.MethodPost, requestHost: "localhost:80", origins: []string{"http://LOCALHOST:80"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "IPv6 hostname normalization", publicOrigin: "https://[2001:db8::1]:8080", method: http.MethodDelete, requestHost: "[2001:0DB8:0:0:0:0:0:1]:8080", origins: []string{"https://[2001:DB8::1]:8080"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "https versus http mismatch", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"http://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "non-default port mismatch", publicOrigin: "https://app.example.test:8443", method: http.MethodPost, requestHost: "app.example.test:8443", origins: []string{"https://app.example.test:9443"}, wantStatus: http.StatusForbidden},
		{name: "missing origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", wantStatus: http.StatusForbidden},
		{name: "empty origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{""}, wantStatus: http.StatusForbidden},
		{name: "null origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"null"}, wantStatus: http.StatusForbidden},
		{name: "relative origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"/path"}, wantStatus: http.StatusForbidden},
		{name: "malformed origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://"}, wantStatus: http.StatusForbidden},
		{name: "unsupported scheme", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"ftp://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "userinfo", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://user@app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "path", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test/path"}, wantStatus: http.StatusForbidden},
		{name: "trailing slash path", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test/"}, wantStatus: http.StatusForbidden},
		{name: "query", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test?x=1"}, wantStatus: http.StatusForbidden},
		{name: "empty query", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test?"}, wantStatus: http.StatusForbidden},
		{name: "fragment", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test#section"}, wantStatus: http.StatusForbidden},
		{name: "empty fragment", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test#"}, wantStatus: http.StatusForbidden},
		{name: "opaque URL", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https:app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "cross origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://other.example.test"}, wantStatus: http.StatusForbidden},
		{name: "safe methods still require the configured Host", publicOrigin: "https://app.example.test", method: http.MethodGet, requestHost: "other.example.test", wantStatus: http.StatusForbidden},
		{name: "wrong request Host", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "other.example.test", origins: []string{"https://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "wrong request Host port", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test:8443", origins: []string{"https://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "empty request Host", publicOrigin: "https://app.example.test", method: http.MethodPost, origins: []string{"https://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "empty request Host port", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test:", origins: []string{"https://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "multiple origin headers", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test", "https://other.example.test"}, wantStatus: http.StatusForbidden},
		{name: "comma separated origins", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test, https://other.example.test"}, wantStatus: http.StatusForbidden},
		{name: "forwarded host cannot authorize mismatched Host", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "spoofed.example.test", origins: []string{"https://app.example.test"}, forwardedHost: "app.example.test", wantStatus: http.StatusForbidden},
		{name: "forwarded host cannot authorize mismatched Origin", publicOrigin: "https://app.example.test", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://spoofed.example.test"}, forwardedHost: "spoofed.example.test", wantStatus: http.StatusForbidden},
		{name: "unknown method fails closed", publicOrigin: "https://app.example.test", method: "PROPFIND", requestHost: "app.example.test", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			})
			handler := RequireSameOrigin(tt.publicOrigin, next)
			request := httptest.NewRequest(tt.method, "https://app.example.test/resource", nil)
			request.Host = tt.requestHost
			for _, requestOrigin := range tt.origins {
				request.Header.Add("Origin", requestOrigin)
			}
			if tt.forwardedHost != "" {
				request.Header.Set("X-Forwarded-Host", tt.forwardedHost)
				request.Header.Set("X-Forwarded-Proto", "https")
			}

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", response.Code, tt.wantStatus, response.Body.String())
			}
			if reached != tt.wantReached {
				t.Errorf("next handler reached = %t, want %t", reached, tt.wantReached)
			}
			if !tt.wantReached {
				if response.Body.String() != sameOriginForbiddenResponse {
					t.Errorf("rejection body = %q, want neutral forbidden response", response.Body.String())
				}
				if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
			}
		})
	}
}

func TestRequireSameOriginRejectsInvalidConfiguration(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			reached := false
			handler := RequireSameOrigin("/not-an-origin", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))
			request := httptest.NewRequest(method, "http://localhost:8080/", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}
			if reached {
				t.Error("next handler was reached with invalid configured origin")
			}
		})
	}
}
