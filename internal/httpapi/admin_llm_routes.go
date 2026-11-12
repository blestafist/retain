package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

type llmAdminController interface {
	Models(context.Context) (llmservice.ModelStatus, error)
	SetModel(context.Context, string) (string, error)
	Usage(context.Context) ([]llmservice.LLMUsageAggregate, error)
}

type adminLLMUsageResponse struct {
	Users []adminLLMUsageUser `json:"users"`
}

type adminLLMUsageUser struct {
	UserID int64                `json:"user_id"`
	Name   string               `json:"name"`
	Email  string               `json:"email"`
	Models []adminLLMUsageModel `json:"models"`
}

type adminLLMUsageModel struct {
	Model            string `json:"model"`
	Kind             string `json:"kind"`
	Requests         int64  `json:"requests"`
	CachedRequests   int64  `json:"cached_requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

// RegisterLLMAdminRoutes registers authenticated administrator-only model
// controls and lifetime LLM usage reporting. The server chooses when to mount
// these routes so existing handler constructors remain unchanged.
func RegisterLLMAdminRoutes(mux *http.ServeMux, authService *auth.Service, service llmAdminController) {
	mux.HandleFunc("GET /api/v1/admin/llm/models", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedAdmin(w, r, authService); !ok {
			return
		}
		if service == nil {
			writeInternalError(w)
			return
		}
		status, err := service.Models(r.Context())
		if err != nil {
			writeInternalError(w)
			return
		}
		if status.Models == nil {
			status.Models = []string{}
		}
		if err := WriteJSON(w, http.StatusOK, status); err != nil {
			writeInternalError(w)
		}
	})

	mux.HandleFunc("PUT /api/v1/admin/llm/model", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedAdmin(w, r, authService); !ok {
			return
		}
		var input struct {
			Model string `json:"model"`
		}
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		if service == nil {
			writeInternalError(w)
			return
		}
		selected, err := service.SetModel(r.Context(), input.Model)
		if err != nil {
			writeLLMAdminError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, struct {
			SelectedModel string `json:"selected_model"`
		}{SelectedModel: selected}); err != nil {
			writeInternalError(w)
		}
	})

	mux.HandleFunc("GET /api/v1/admin/llm-usage", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedAdmin(w, r, authService); !ok {
			return
		}
		if service == nil {
			writeInternalError(w)
			return
		}
		usage, err := service.Usage(r.Context())
		if err != nil {
			writeInternalError(w)
			return
		}
		if usage == nil {
			usage = []llmservice.LLMUsageAggregate{}
		}
		if err := WriteJSON(w, http.StatusOK, groupAdminLLMUsage(usage)); err != nil {
			writeInternalError(w)
		}
	})
}

func groupAdminLLMUsage(rows []llmservice.LLMUsageAggregate) adminLLMUsageResponse {
	response := adminLLMUsageResponse{Users: []adminLLMUsageUser{}}
	userIndexes := make(map[int64]int, len(rows))
	for _, row := range rows {
		index, exists := userIndexes[row.UserID]
		if !exists {
			index = len(response.Users)
			userIndexes[row.UserID] = index
			response.Users = append(response.Users, adminLLMUsageUser{
				UserID: row.UserID, Name: row.UserName, Email: row.UserEmail, Models: []adminLLMUsageModel{},
			})
		}
		response.Users[index].Models = append(response.Users[index].Models, adminLLMUsageModel{
			Model: row.Model, Kind: row.Feature, Requests: row.Requests, CachedRequests: row.CachedRequests,
			PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
		})
	}
	return response
}

func authenticatedAdmin(w http.ResponseWriter, r *http.Request, service *auth.Service) (auth.User, bool) {
	if service == nil {
		writeInternalError(w)
		return auth.User{}, false
	}
	user, ok := authenticatedUser(w, r, service)
	if !ok {
		return auth.User{}, false
	}
	if !user.IsAdmin {
		_ = WriteAPIError(w, http.StatusForbidden, "forbidden", "You are not allowed to perform this action.")
		return auth.User{}, false
	}
	return user, true
}

func writeLLMAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llmservice.ErrInvalidModel):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_model", "A valid model ID is required.")
	case errors.Is(err, llmservice.ErrUnknownModel):
		_ = WriteAPIError(w, http.StatusUnprocessableEntity, "unknown_model", "The selected model is not currently offered by the provider.")
	case errors.Is(err, llmservice.ErrModelDiscoveryUnavailable):
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "model_discovery_unavailable", "The model provider is currently unavailable.")
	default:
		writeInternalError(w)
	}
}
