package httpapi

import (
	"errors"
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/study"
	syncservice "github.com/pestit/retain/internal/sync"
)

// RegisterSyncRoutes registers the authenticated offline synchronization endpoint.
func RegisterSyncRoutes(mux *http.ServeMux, authService *auth.Service, service *syncservice.Service) {
	mux.HandleFunc("POST /api/v1/sync", syncHandler(authService, service))
}

func syncHandler(authService *auth.Service, service *syncservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		var input syncservice.Request
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Sync(r.Context(), user.ID, input)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, syncservice.ErrInvalidCursor):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_cursor", "The sync cursor is invalid.")
	case errors.Is(err, syncservice.ErrInvalidInput), errors.Is(err, study.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The sync request contains invalid input.")
	case errors.Is(err, study.ErrNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, study.ErrConflict):
		_ = WriteAPIError(w, http.StatusConflict, "conflict", "The review conflicts with an existing review.")
	default:
		writeInternalError(w)
	}
}
