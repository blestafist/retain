package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

const sameOriginForbiddenResponse = `{"error":{"code":"forbidden","message":"Forbidden"}}` + "\n"

// RequireSameOrigin rejects requests that may change state unless they carry
// one valid Origin matching the request Host. It intentionally does not use
// forwarded headers, which are not authoritative here.
//
// GET, HEAD, and OPTIONS are allowed without an Origin. Other methods are
// checked so that newly introduced state-changing methods fail closed.
func RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || hasSameOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(sameOriginForbiddenResponse))
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func hasSameOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || r.Host == "" {
		return false
	}

	rawOrigin := origins[0]
	if rawOrigin == "" || strings.EqualFold(rawOrigin, "null") || strings.Contains(rawOrigin, ",") {
		return false
	}

	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.Opaque != "" || origin.User != nil || origin.Host == "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	if origin.Path != "" || origin.RawPath != "" || origin.RawQuery != "" || origin.ForceQuery ||
		origin.Fragment != "" || origin.RawFragment != "" || strings.Contains(rawOrigin, "#") {
		return false
	}
	if origin.Hostname() == "" || strings.HasSuffix(origin.Host, ":") {
		return false
	}

	return strings.EqualFold(origin.Host, r.Host)
}
