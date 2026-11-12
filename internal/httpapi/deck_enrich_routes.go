package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

type deckEnrichSubmitter interface {
	Submit(context.Context, int64, int64) (string, error)
}

// RegisterDeckEnrichRoutes registers POST /api/v1/decks/{id}/enrich. The mux
// should be wrapped in the server's same-origin guard by its caller.
func RegisterDeckEnrichRoutes(mux *http.ServeMux, authService *auth.Service, service deckEnrichSubmitter) {
	mux.HandleFunc("POST /api/v1/decks/{id}/enrich", func(w http.ResponseWriter, r *http.Request) {
		if authService == nil {
			writeInternalError(w)
			return
		}
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		deckID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || deckID <= 0 {
			_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
			return
		}
		if service == nil {
			writeInternalError(w)
			return
		}
		jobID, err := service.Submit(r.Context(), user.ID, deckID)
		if err != nil {
			writeDeckEnrichError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusAccepted, struct {
			JobID string `json:"job_id"`
		}{JobID: jobID}); err != nil {
			writeInternalError(w)
		}
	})
}

func writeDeckEnrichError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llmservice.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
	case errors.Is(err, llmservice.ErrDeckNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, llmservice.ErrDeckEnrichCandidateLimit):
		_ = WriteAPIError(w, http.StatusUnprocessableEntity, "too_many_candidates", "A deck enrichment job can include at most 100 notes.")
	case errors.Is(err, llmservice.ErrDeckEnrichQueueFull):
		_ = WriteAPIError(w, http.StatusTooManyRequests, "job_capacity_reached", "The background job queue is busy. Try again later.")
	case errors.Is(err, llmservice.ErrFeatureDisabled):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "Deck enrichment is currently disabled.")
	default:
		writeInternalError(w)
	}
}
