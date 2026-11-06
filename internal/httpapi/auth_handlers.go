package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/pestit/retain/internal/auth"
)

const (
	sessionCookieName = "retain_session"
	sessionCookiePath = "/api/v1"
	sessionLifetime   = 30 * 24 * time.Hour
)

// RegisterAccountRoutes registers account and session endpoints on mux.
func RegisterAccountRoutes(mux *http.ServeMux, service *auth.Service) {
	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var input auth.RegisterInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		user, err := service.Register(r.Context(), input)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusCreated, user); err != nil {
			writeInternalError(w)
		}
	})

	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var input auth.LoginInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Login(r.Context(), input, r.UserAgent())
		if err != nil {
			writeAuthError(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    result.Token,
			Path:     sessionCookiePath,
			Expires:  result.ExpiresAt,
			MaxAge:   int(sessionLifetime.Seconds()),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if err := service.Logout(r.Context(), cookie.Value); err != nil {
				writeAuthError(w, err)
				return
			}
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     sessionCookiePath,
			Expires:  time.Unix(1, 0).UTC(),
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, service)
		if !ok {
			return
		}
		account, err := service.GetAccount(r.Context(), user.ID)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, account); err != nil {
			writeInternalError(w)
		}
	})

	mux.HandleFunc("PATCH /api/v1/me/settings", func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, service)
		if !ok {
			return
		}
		var patch map[string]json.RawMessage
		if err := DecodeJSON(r, &patch); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if patch == nil {
			writeJSONDecodeError(w, ErrInvalidJSON)
			return
		}
		settings, err := service.UpdateSettings(r.Context(), user.ID, patch)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, settings); err != nil {
			writeInternalError(w)
		}
	})

	mux.HandleFunc("POST /api/v1/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, service)
		if !ok {
			return
		}
		invite, err := service.CreateInvite(r.Context(), user.ID)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		response := struct {
			Code string `json:"code"`
			URL  string `json:"url"`
		}{Code: invite.Code, URL: "/signup?invite=" + url.QueryEscape(invite.Code)}
		if err := WriteJSON(w, http.StatusCreated, response); err != nil {
			writeInternalError(w)
		}
	})
}

func authenticatedUser(w http.ResponseWriter, r *http.Request, service *auth.Service) (auth.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeAuthError(w, auth.ErrUnauthenticated)
		return auth.User{}, false
	}
	user, err := service.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeAuthError(w, err)
		return auth.User{}, false
	}
	return user, true
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	status := JSONErrorStatus(err)
	if status == 0 {
		writeInternalError(w)
		return
	}
	code, message := "invalid_json", "Request body must be valid JSON."
	if errors.Is(err, ErrUnsupportedMediaType) {
		code, message = "unsupported_media_type", "Content-Type must be application/json."
	} else if errors.Is(err, ErrJSONBodyTooLarge) {
		code, message = "request_too_large", "Request body is too large."
	}
	_ = WriteAPIError(w, status, code, message)
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
	case errors.Is(err, auth.ErrInvalidInvite), errors.Is(err, auth.ErrInviteExpired), errors.Is(err, auth.ErrInviteUsed):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_invite", "The invite is invalid or no longer available.")
	case errors.Is(err, auth.ErrEmailExists):
		_ = WriteAPIError(w, http.StatusConflict, "email_exists", "An account with this email already exists.")
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUnauthenticated):
		_ = WriteAPIError(w, http.StatusUnauthorized, "unauthenticated", "Authentication is required or the credentials are invalid.")
	case errors.Is(err, auth.ErrForbidden):
		_ = WriteAPIError(w, http.StatusForbidden, "forbidden", "You are not allowed to perform this action.")
	case errors.Is(err, auth.ErrPasswordHashBusy):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "auth_busy", "Authentication is temporarily unavailable. Try again.")
	default:
		writeInternalError(w)
	}
}

func writeInternalError(w http.ResponseWriter) {
	_ = WriteAPIError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
}
