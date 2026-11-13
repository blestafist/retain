package httpapi

import (
	"errors"
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/push"
)

// RegisterPushRoutes registers authenticated Web Push subscription endpoints.
func RegisterPushRoutes(mux *http.ServeMux, authentication *auth.Service, service *push.Service) {
	mux.HandleFunc("GET /api/v1/push/vapid-key", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, authentication); !ok {
			return
		}
		key, err := service.PublicKey()
		if err != nil {
			writePushError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, map[string]string{"public_key": key}); err != nil {
			writeInternalError(w)
		}
	})
	mux.HandleFunc("POST /api/v1/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		var input push.SubscriptionInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if err := service.Subscribe(r.Context(), user.ID, input); err != nil {
			writePushError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		var input struct {
			Endpoint string `json:"endpoint"`
		}
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if err := service.Unsubscribe(r.Context(), user.ID, input.Endpoint); err != nil {
			writePushError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func writePushError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, push.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The push subscription is invalid.")
	case errors.Is(err, push.ErrUnavailable):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "push_unavailable", "Push reminders are not configured on this server.")
	case errors.Is(err, push.ErrConflict):
		_ = WriteAPIError(w, http.StatusConflict, "push_subscription_conflict", "This browser subscription is already linked to another account.")
	default:
		writeInternalError(w)
	}
}
