package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

type llmSynonymService interface {
	Enabled() bool
	Check(context.Context, int64, llmservice.SynonymRequest) (llmservice.SynonymResponse, error)
}

// RegisterLLMSynonymRoutes registers the authenticated synonym-check endpoint.
// The caller is responsible for placing this POST route behind the same-origin guard.
func RegisterLLMSynonymRoutes(mux *http.ServeMux, authService *auth.Service, service llmSynonymService) {
	mux.HandleFunc("POST /api/v1/llm/check-answer", func(w http.ResponseWriter, r *http.Request) {
		if service == nil || !service.Enabled() {
			_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Synonym checking is currently disabled.")
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
		var input llmservice.SynonymRequest
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if err := llmservice.ValidateSynonymRequest(input); err != nil {
			_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
			return
		}
		result, err := service.Check(r.Context(), user.ID, input)
		if err != nil {
			writeLLMSynonymError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	})
}

func writeLLMSynonymError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llmservice.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
	case errors.Is(err, llmservice.ErrNoteNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "The requested note was not found.")
	case errors.Is(err, llmservice.ErrUnsupportedPair):
		_ = WriteAPIError(w, http.StatusUnprocessableEntity, "unsupported_language_pair", "This note does not use a supported language pair.")
	case errors.Is(err, llmservice.ErrDailyLimit):
		_ = WriteAPIError(w, http.StatusTooManyRequests, "daily_limit_reached", "The daily synonym-check limit has been reached.")
	case errors.Is(err, llmservice.ErrFeatureDisabled):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Synonym checking is currently disabled.")
	case errors.Is(err, llmservice.ErrInternal):
		writeInternalError(w)
	default:
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "llm_unavailable", "Synonym checking is temporarily unavailable. Try again.")
	}
}
