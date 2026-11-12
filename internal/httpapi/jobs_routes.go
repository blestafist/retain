package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmstore"
)

// RegisterJobRoutes registers authenticated, owner-scoped background job routes.
func RegisterJobRoutes(mux *http.ServeMux, authService *auth.Service, llmstoreStore *llmstore.Store) {
	mux.HandleFunc("GET /api/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if authService == nil {
			writeInternalError(w)
			return
		}
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		if llmstoreStore == nil {
			writeInternalError(w)
			return
		}

		job, err := llmstoreStore.GetJob(r.Context(), user.ID, r.PathValue("id"))
		if errors.Is(err, llmstore.ErrNotFound) {
			_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
			return
		}
		if err != nil {
			writeInternalError(w)
			return
		}

		response := jobStatusResponse{
			ID: job.ID, Kind: job.Kind, Status: job.Status, Progress: job.Progress, Total: job.Total,
			Error: job.Error, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		}
		if job.Status == "done" {
			response.Result = job.Result
			if len(response.Result) == 0 {
				response.Result = json.RawMessage("null")
			}
		}
		if err := WriteJSON(w, http.StatusOK, response); err != nil {
			writeInternalError(w)
		}
	})
}

type jobStatusResponse struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Progress  int64           `json:"progress"`
	Total     int64           `json:"total"`
	Error     *string         `json:"error"`
	Result    json.RawMessage `json:"result,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
