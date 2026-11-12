package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/llmservice"
)

const (
	llmAskHeartbeatInterval = 15 * time.Second
	llmAskEventBuffer       = 16
)

type llmAskService interface {
	Enabled() bool
	Ask(context.Context, int64, llmservice.AskRequest, func(context.Context, string) error) error
}

// RegisterLLMAskRoutes registers the authenticated streaming ask endpoint.
// The caller is responsible for placing this state-changing route behind the
// same-origin guard.
func RegisterLLMAskRoutes(mux *http.ServeMux, authService *auth.Service, service llmAskService) {
	mux.HandleFunc("POST /api/v1/llm/ask", func(w http.ResponseWriter, r *http.Request) {
		serveLLMAsk(w, r, authService, service, llmAskHeartbeatInterval)
	})
}

type llmAskEvent struct {
	token string
	err   error
	done  bool
}

func serveLLMAsk(w http.ResponseWriter, r *http.Request, authService *auth.Service, service llmAskService, heartbeat time.Duration) {
	if service == nil || !service.Enabled() {
		_ = WriteAPIError(w, http.StatusServiceUnavailable, "feature_disabled", "The assistant is currently disabled.")
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
	var input llmservice.AskRequest
	if err := DecodeJSON(r, &input); err != nil {
		writeJSONDecodeError(w, err)
		return
	}
	if err := llmservice.ValidateAskRequest(input); err != nil {
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeInternalError(w)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan llmAskEvent, llmAskEventBuffer)
	go func() {
		err := service.Ask(streamCtx, user.ID, input, func(callbackCtx context.Context, token string) error {
			if err := callbackCtx.Err(); err != nil {
				return err
			}
			select {
			case events <- llmAskEvent{token: token}:
				return nil
			case <-callbackCtx.Done():
				return callbackCtx.Err()
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		})
		select {
		case events <- llmAskEvent{err: err, done: true}:
		case <-streamCtx.Done():
		}
	}()

	if heartbeat <= 0 {
		heartbeat = llmAskHeartbeatInterval
	}
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-streamCtx.Done():
			return
		case event := <-events:
			if event.done {
				if event.err != nil {
					_ = writeLLMAskErrorEvent(w, event.err)
				} else {
					_ = writeSSEEvent(w, "done", struct{}{})
				}
				flusher.Flush()
				return
			}
			if err := writeSSEEvent(w, "token", struct {
				Text string `json:"text"`
			}{Text: event.token}); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, name string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("event: " + name + "\ndata: ")); err != nil {
		return err
	}
	if _, err := w.Write(encoded); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n\n"))
	return err
}

func writeLLMAskErrorEvent(w http.ResponseWriter, err error) error {
	code, message := llmAskErrorDetails(err)
	return writeSSEEvent(w, "error", struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}

func llmAskErrorDetails(err error) (code, message string) {
	switch {
	case errors.Is(err, llmservice.ErrInvalidInput):
		return "invalid_input", "The request contains invalid input."
	case errors.Is(err, llmservice.ErrNoteNotFound):
		return "not_found", "The requested note was not found."
	case errors.Is(err, llmservice.ErrUnsupportedPair):
		return "unsupported_language_pair", "This language pair is not supported."
	case errors.Is(err, llmservice.ErrDailyLimit):
		return "daily_limit_reached", "The daily assistant usage limit has been reached."
	case errors.Is(err, llmservice.ErrFeatureDisabled):
		return "feature_disabled", "The assistant is currently disabled."
	case errors.Is(err, llmservice.ErrInternal):
		return "internal_error", "An internal error occurred."
	default:
		return "llm_unavailable", "The assistant could not answer right now. Try again."
	}
}
