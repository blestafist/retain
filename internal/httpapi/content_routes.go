package httpapi

import (
	"net/http"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/notes"
)

// RegisterContentRoutes registers owner-scoped deck and note endpoints.
// The caller is responsible for placing the mux behind the same-origin guard.
func RegisterContentRoutes(mux *http.ServeMux, authentication *auth.Service, deckService *decks.Service, noteService *notes.Service) {
	mux.HandleFunc("GET /api/v1/decks", listDecksHandler(authentication, deckService))
	mux.HandleFunc("POST /api/v1/decks", createDeckHandler(authentication, deckService))
	mux.HandleFunc("PATCH /api/v1/decks/{id}", updateDeckHandler(authentication, deckService))
	mux.HandleFunc("DELETE /api/v1/decks/{id}", deleteDeckHandler(authentication, deckService))
	mux.HandleFunc("GET /api/v1/decks/{id}/notes", listNotesHandler(authentication, noteService))
	mux.HandleFunc("POST /api/v1/decks/{id}/notes", createNoteHandler(authentication, noteService))
	mux.HandleFunc("PATCH /api/v1/notes/{id}", updateNoteHandler(authentication, noteService))
	mux.HandleFunc("DELETE /api/v1/notes/{id}", deleteNoteHandler(authentication, noteService))
}
