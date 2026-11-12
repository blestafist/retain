package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

type llmMnemonicService interface {
	Enabled() bool
	Mnemonic(context.Context, int64, llmservice.MnemonicRequest) (llmservice.MnemonicResponse, error)
}

// RegisterLLMMnemonicRoutes registers the authenticated mnemonic endpoint.
// The caller is responsible for placing this state-changing route behind the
// same-origin guard.
func RegisterLLMMnemonicRoutes(mux *http.ServeMux, authService *auth.Service, service llmMnemonicService) {
	mux.HandleFunc("POST /api/v1/llm/mnemonic", func(w http.ResponseWriter, r *http.Request) {
		if service == nil || !service.Enabled() {
			_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Mnemonic suggestions are currently disabled.")
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
		var input llmservice.MnemonicRequest
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if err := llmservice.ValidateMnemonicRequest(input); err != nil {
			_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
			return
		}
		result, err := service.Mnemonic(r.Context(), user.ID, input)
		if err != nil {
			writeLLMMnemonicError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	})
}

func writeLLMMnemonicError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llmservice.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
	case errors.Is(err, llmservice.ErrNoteNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "The requested note was not found.")
	case errors.Is(err, llmservice.ErrUnsupportedPair):
		_ = WriteAPIError(w, http.StatusUnprocessableEntity, "unsupported_language_pair", "This note does not use a supported language pair.")
	case errors.Is(err, llmservice.ErrDailyLimit):
		_ = WriteAPIError(w, http.StatusTooManyRequests, "daily_limit_reached", "The daily mnemonic-suggestion limit has been reached.")
	case errors.Is(err, llmservice.ErrFeatureDisabled):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Mnemonic suggestions are currently disabled.")
	case errors.Is(err, llmservice.ErrInternal):
		writeInternalError(w)
	default:
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "llm_unavailable", "Mnemonic suggestions are temporarily unavailable. Try again.")
	}
}
