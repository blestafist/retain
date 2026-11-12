package httpapi

import (
	"errors"
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

// RegisterLLMRoutes registers the public feature-status endpoint and the
// authenticated owner-scoped enrichment endpoint. The caller is responsible
// for placing the POST route behind the same-origin guard.
func RegisterLLMRoutes(mux *http.ServeMux, authService *auth.Service, service *llmservice.Service) {
	mux.HandleFunc("GET /api/v1/llm/status", func(w http.ResponseWriter, _ *http.Request) {
		enabled := service != nil && service.Enabled()
		if err := WriteJSON(w, http.StatusOK, struct {
			Enabled bool `json:"enabled"`
		}{Enabled: enabled}); err != nil {
			writeInternalError(w)
		}
	})

	mux.HandleFunc("POST /api/v1/llm/enrich", func(w http.ResponseWriter, r *http.Request) {
		if service == nil || !service.Enabled() {
			_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Word suggestions are currently disabled.")
			return
		}
		if authService == nil {
			writeInternalError(w)
			return
		}
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		var input llmservice.Request
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Enrich(r.Context(), user.ID, input)
		if err != nil {
			writeLLMServiceError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	})
}

func writeLLMServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llmservice.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
	case errors.Is(err, llmservice.ErrDeckNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, llmservice.ErrUnsupportedPair):
		_ = WriteAPIError(w, http.StatusUnprocessableEntity, "unsupported_language_pair", "This deck does not use a supported language pair.")
	case errors.Is(err, llmservice.ErrDailyLimit):
		_ = WriteAPIError(w, http.StatusTooManyRequests, "daily_limit_reached", "The daily word-suggestion limit has been reached.")
	case errors.Is(err, llmservice.ErrFeatureDisabled):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Word suggestions are currently disabled.")
	case errors.Is(err, llmservice.ErrUnavailable):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "llm_unavailable", "Word suggestions are temporarily unavailable. Try again.")
	default:
		writeInternalError(w)
	}
}
