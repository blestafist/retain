package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSameOrigin(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		requestHost   string
		origins       []string
		forwardedHost string
		wantStatus    int
		wantReached   bool
	}{
		{name: "GET without origin", method: http.MethodGet, requestHost: "app.example.test", wantStatus: http.StatusNoContent, wantReached: true},
		{name: "HEAD with cross-origin origin", method: http.MethodHead, requestHost: "app.example.test", origins: []string{"https://other.example.test"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "OPTIONS with invalid origin", method: http.MethodOptions, requestHost: "app.example.test", origins: []string{"null"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "POST matching origin", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "PATCH hostname is case insensitive", method: http.MethodPatch, requestHost: "APP.EXAMPLE.TEST", origins: []string{"https://app.example.test"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "PUT matching host and port", method: http.MethodPut, requestHost: "app.example.test:8443", origins: []string{"http://APP.EXAMPLE.TEST:8443"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "DELETE matching IPv6 host", method: http.MethodDelete, requestHost: "[2001:db8::1]:8080", origins: []string{"https://[2001:DB8::1]:8080"}, wantStatus: http.StatusNoContent, wantReached: true},
		{name: "missing origin", method: http.MethodPost, requestHost: "app.example.test", wantStatus: http.StatusForbidden},
		{name: "empty origin", method: http.MethodPost, requestHost: "app.example.test", origins: []string{""}, wantStatus: http.StatusForbidden},
		{name: "null origin", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"null"}, wantStatus: http.StatusForbidden},
		{name: "relative origin", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"/path"}, wantStatus: http.StatusForbidden},
		{name: "malformed origin", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://"}, wantStatus: http.StatusForbidden},
		{name: "unsupported scheme", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"ftp://app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "userinfo", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://user@app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "path", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test/path"}, wantStatus: http.StatusForbidden},
		{name: "trailing slash path", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test/"}, wantStatus: http.StatusForbidden},
		{name: "query", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test?x=1"}, wantStatus: http.StatusForbidden},
		{name: "empty query", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test?"}, wantStatus: http.StatusForbidden},
		{name: "fragment", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test#section"}, wantStatus: http.StatusForbidden},
		{name: "empty fragment", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test#"}, wantStatus: http.StatusForbidden},
		{name: "opaque URL", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https:app.example.test"}, wantStatus: http.StatusForbidden},
		{name: "cross origin", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://other.example.test"}, wantStatus: http.StatusForbidden},
		{name: "different port", method: http.MethodPost, requestHost: "app.example.test:8443", origins: []string{"https://app.example.test:9443"}, wantStatus: http.StatusForbidden},
		{name: "empty port", method: http.MethodPost, requestHost: "app.example.test:", origins: []string{"https://app.example.test:"}, wantStatus: http.StatusForbidden},
		{name: "multiple origin headers", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test", "https://other.example.test"}, wantStatus: http.StatusForbidden},
		{name: "comma separated origins", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://app.example.test, https://other.example.test"}, wantStatus: http.StatusForbidden},
		{name: "does not trust forwarded host", method: http.MethodPost, requestHost: "app.example.test", origins: []string{"https://proxy.example.test"}, forwardedHost: "proxy.example.test", wantStatus: http.StatusForbidden},
		{name: "unknown method fails closed", method: "PROPFIND", requestHost: "app.example.test", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			})
			handler := RequireSameOrigin(next)
			request := httptest.NewRequest(tt.method, "https://app.example.test/resource", nil)
			request.Host = tt.requestHost
			for _, origin := range tt.origins {
				request.Header.Add("Origin", origin)
			}
			if tt.forwardedHost != "" {
				request.Header.Set("X-Forwarded-Host", tt.forwardedHost)
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
