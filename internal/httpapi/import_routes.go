package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/contentimport"
)

const maxImportRequestBytes = (100 << 20) + (1 << 20)

// RegisterImportRoutes registers owner-scoped import and export endpoints.
// The caller is responsible for placing the mux behind the same-origin guard.
func RegisterImportRoutes(mux *http.ServeMux, authService *auth.Service, service *contentimport.Service) {
	mux.HandleFunc("POST /api/v1/import/preview", importPreviewHandler(authService, service))
	mux.HandleFunc("POST /api/v1/import/{batch_id}/apply", importApplyHandler(authService, service))
	mux.HandleFunc("GET /api/v1/import/batches", importHistoryHandler(authService, service))
	mux.HandleFunc("GET /api/v1/decks/{id}/export", deckExportHandler(authService, service))
}

func importPreviewHandler(authService *auth.Service, service *contentimport.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			_ = WriteAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxImportRequestBytes)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				_ = WriteAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The import file is too large.")
			} else {
				_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The multipart request is invalid.")
			}
			return
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The multipart request must include a file field.")
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, (100<<20)+1))
		if err != nil {
			writeInternalError(w)
			return
		}
		if len(raw) > 100<<20 {
			_ = WriteAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "The import file is too large.")
			return
		}
		result, err := service.Preview(r.Context(), user.ID, header.Filename, strings.TrimSpace(r.FormValue("adapter")), raw)
		if err != nil {
			writeImportError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func importApplyHandler(authService *auth.Service, service *contentimport.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		var input contentimport.ApplyInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Apply(r.Context(), user.ID, r.PathValue("batch_id"), input)
		if err != nil {
			writeImportError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func importHistoryHandler(authService *auth.Service, service *contentimport.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		result, err := service.History(r.Context(), user.ID)
		if err != nil {
			writeImportError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func deckExportHandler(authService *auth.Service, service *contentimport.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		deckID, ok := contentID(w, r)
		if !ok {
			return
		}
		result, err := service.Export(r.Context(), user.ID, deckID, r.URL.Query().Get("format"))
		if err != nil {
			writeImportError(w, err)
			return
		}
		w.Header().Set("Content-Type", result.ContentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+result.Filename+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(result.Body); err != nil {
			return
		}
	}
}

func writeImportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contentimport.ErrNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, contentimport.ErrConflict):
		_ = WriteAPIError(w, http.StatusConflict, "conflict", "The import batch conflicts with existing content or has already been applied.")
	case errors.Is(err, contentimport.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The import request contains invalid or conflicting data.")
	default:
		writeInternalError(w)
	}
}
