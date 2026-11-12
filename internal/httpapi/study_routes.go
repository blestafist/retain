package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/study"
)

// RegisterStudyRoutes registers the authenticated, owner-scoped review workflow.
// The caller is responsible for placing state-changing routes behind the same-origin guard.
func RegisterStudyRoutes(mux *http.ServeMux, authService *auth.Service, studyService *study.Service) {
	mux.HandleFunc("GET /api/v1/study/queue", studyQueueHandler(authService, studyService))
	mux.HandleFunc("POST /api/v1/study/review", studyReviewHandler(authService, studyService))
	mux.HandleFunc("POST /api/v1/study/undo", studyUndoHandler(authService, studyService))
}

func studyQueueHandler(authService *auth.Service, service *study.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		deckID, limit, ok := parseStudyQueueQuery(w, r)
		if !ok {
			return
		}
		result, err := service.Queue(r.Context(), user.ID, deckID, limit)
		if err != nil {
			writeStudyError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func studyReviewHandler(authService *auth.Service, service *study.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		var input study.ReviewInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Review(r.Context(), user.ID, input)
		if err != nil {
			writeStudyError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func studyUndoHandler(authService *auth.Service, service *study.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		var input struct {
			ReviewID string `json:"review_id"`
		}
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Undo(r.Context(), user.ID, input.ReviewID)
		if err != nil {
			writeStudyError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func parseStudyQueueQuery(w http.ResponseWriter, r *http.Request) (*int64, int, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeStudyInputError(w)
		return nil, 0, false
	}
	for key, items := range values {
		if (key != "deck_id" && key != "limit") || len(items) != 1 {
			writeStudyInputError(w)
			return nil, 0, false
		}
	}

	var deckID *int64
	if items, exists := values["deck_id"]; exists && items[0] != "" {
		parsed, err := strconv.ParseInt(items[0], 10, 64)
		if err != nil || parsed <= 0 {
			writeStudyInputError(w)
			return nil, 0, false
		}
		deckID = &parsed
	}

	limit := 50
	if items, exists := values["limit"]; exists {
		parsed, err := strconv.Atoi(items[0])
		if err != nil || parsed <= 0 || parsed > 500 {
			writeStudyInputError(w)
			return nil, 0, false
		}
		limit = parsed
	}
	return deckID, limit, true
}

func writeStudyInputError(w http.ResponseWriter) {
	_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The study request contains invalid input.")
}

func writeStudyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, study.ErrNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, study.ErrInvalidInput):
		writeStudyInputError(w)
	case errors.Is(err, study.ErrConflict):
		_ = WriteAPIError(w, http.StatusConflict, "conflict", "The review conflicts with an existing review.")
	default:
		writeInternalError(w)
	}
}
