package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/notes"
)

func listDecksHandler(authentication *auth.Service, service *decks.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		result, err := service.List(r.Context(), user.ID)
		if err != nil {
			writeContentError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func createDeckHandler(authentication *auth.Service, service *decks.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		var input decks.CreateInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Create(r.Context(), user.ID, input)
		if err != nil {
			writeContentError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusCreated, result); err != nil {
			writeInternalError(w)
		}
	}
}

func updateDeckHandler(authentication *auth.Service, service *decks.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		id, ok := contentID(w, r)
		if !ok {
			return
		}
		patch, err := decodeDeckPatch(r)
		if err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Update(r.Context(), user.ID, id, patch)
		if err != nil {
			writeContentError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func deleteDeckHandler(authentication *auth.Service, service *decks.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		id, ok := contentID(w, r)
		if !ok {
			return
		}
		if err := service.Delete(r.Context(), user.ID, id); err != nil {
			writeContentError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listNotesHandler(authentication *auth.Service, service *notes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		deckID, ok := contentID(w, r)
		if !ok {
			return
		}
		result, err := service.List(r.Context(), user.ID, deckID)
		if err != nil {
			writeContentError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func createNoteHandler(authentication *auth.Service, service *notes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		deckID, ok := contentID(w, r)
		if !ok {
			return
		}
		var input notes.CreateInput
		if err := DecodeJSON(r, &input); err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Create(r.Context(), user.ID, deckID, input)
		if err != nil {
			writeContentError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusCreated, result); err != nil {
			writeInternalError(w)
		}
	}
}

func updateNoteHandler(authentication *auth.Service, service *notes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		id, ok := contentID(w, r)
		if !ok {
			return
		}
		patch, err := decodeNotePatch(r)
		if err != nil {
			writeJSONDecodeError(w, err)
			return
		}
		result, err := service.Update(r.Context(), user.ID, id, patch)
		if err != nil {
			writeContentError(w, err)
			return
		}
		if err := WriteJSON(w, http.StatusOK, result); err != nil {
			writeInternalError(w)
		}
	}
}

func deleteNoteHandler(authentication *auth.Service, service *notes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authentication)
		if !ok {
			return
		}
		id, ok := contentID(w, r)
		if !ok {
			return
		}
		if err := service.Delete(r.Context(), user.ID, id); err != nil {
			writeContentError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeDeckPatch(r *http.Request) (decks.PatchInput, error) {
	patch, err := decodePatchObject(r, "name", "parent_id", "description", "position", "lang_target", "lang_native", "reverse_cards", "wrong_article_rating", "color_seed")
	if err != nil {
		return decks.PatchInput{}, err
	}
	var result decks.PatchInput
	if result.Name, err = patchValue[string](patch, "name"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.Position, err = patchValue[int64](patch, "position"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.ReverseCards, err = patchValue[bool](patch, "reverse_cards"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.WrongArticleRating, err = patchValue[int64](patch, "wrong_article_rating"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.ParentID, err = patchNullable[int64](patch, "parent_id"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.Description, err = patchNullable[string](patch, "description"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.LangTarget, err = patchNullable[string](patch, "lang_target"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.LangNative, err = patchNullable[string](patch, "lang_native"); err != nil {
		return decks.PatchInput{}, err
	}
	if result.ColorSeed, err = patchNullable[string](patch, "color_seed"); err != nil {
		return decks.PatchInput{}, err
	}
	return result, nil
}

func decodeNotePatch(r *http.Request) (notes.PatchInput, error) {
	patch, err := decodePatchObject(r, "term", "translation", "transcription", "example", "example_translation", "notes", "tags", "alternatives", "fields", "external_id")
	if err != nil {
		return notes.PatchInput{}, err
	}
	var result notes.PatchInput
	if result.Term, err = patchValue[string](patch, "term"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Translation, err = patchValue[string](patch, "translation"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Transcription, err = patchNullable[string](patch, "transcription"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Example, err = patchNullable[string](patch, "example"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.ExampleTranslation, err = patchNullable[string](patch, "example_translation"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Notes, err = patchNullable[string](patch, "notes"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.ExternalID, err = patchNullable[string](patch, "external_id"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Tags, err = patchValue[[]string](patch, "tags"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Alternatives, err = patchValue[[]string](patch, "alternatives"); err != nil {
		return notes.PatchInput{}, err
	}
	if result.Fields, err = patchValue[map[string]any](patch, "fields"); err != nil {
		return notes.PatchInput{}, err
	}
	return result, nil
}

func decodePatchObject(r *http.Request, allowed ...string) (map[string]json.RawMessage, error) {
	var patch map[string]json.RawMessage
	if err := DecodeJSON(r, &patch); err != nil {
		return nil, err
	}
	if patch == nil {
		return nil, ErrInvalidJSON
	}
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for key := range patch {
		if _, ok := known[key]; !ok {
			return nil, ErrInvalidJSON
		}
	}
	return patch, nil
}

func patchValue[T any](patch map[string]json.RawMessage, key string) (*T, error) {
	raw, ok := patch[key]
	if !ok {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, ErrInvalidJSON
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, ErrInvalidJSON
	}
	return &value, nil
}

func patchNullable[T any](patch map[string]json.RawMessage, key string) (field struct {
	Set   bool
	Value *T
}, err error) {
	raw, ok := patch[key]
	if !ok {
		return field, nil
	}
	field.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return field, nil
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return field, ErrInvalidJSON
	}
	field.Value = &value
	return field, nil
}

func contentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
		return 0, false
	}
	return id, true
}

func writeContentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, decks.ErrNotFound), errors.Is(err, notes.ErrNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, decks.ErrInvalidInput), errors.Is(err, notes.ErrInvalidInput):
		_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The request contains invalid input.")
	case errors.Is(err, notes.ErrConflict):
		_ = WriteAPIError(w, http.StatusConflict, "conflict", "The resource conflicts with an existing resource.")
	default:
		writeInternalError(w)
	}
}
