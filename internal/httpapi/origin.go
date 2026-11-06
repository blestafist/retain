package httpapi

import (
	"net/http"

	"github.com/pestit/retain/internal/origin"
)

const sameOriginForbiddenResponse = `{"error":{"code":"forbidden","message":"Forbidden"}}` + "\n"

// RequireSameOrigin rejects requests whose Host does not match publicOrigin,
// and requests that may change state unless they carry one Origin matching
// publicOrigin. It intentionally does not use forwarded headers.
//
// GET, HEAD, and OPTIONS are allowed without an Origin, but must still use the
// configured Host. Other methods are checked so newly introduced
// state-changing methods fail closed. An invalid publicOrigin fails closed
// with an internal server error for all methods.
func RequireSameOrigin(publicOrigin string, next http.Handler) http.Handler {
	canonicalOrigin, configErr := origin.Parse(publicOrigin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if configErr != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !canonicalOrigin.MatchesHost(r.Host) {
			writeSameOriginForbidden(w)
			return
		}
		if isSafeMethod(r.Method) || hasSameOrigin(canonicalOrigin, r) {
			next.ServeHTTP(w, r)
			return
		}

		writeSameOriginForbidden(w)
	})
}

func writeSameOriginForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(sameOriginForbiddenResponse))
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func hasSameOrigin(publicOrigin origin.Origin, r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}

	return publicOrigin.Matches(origins[0])
}
