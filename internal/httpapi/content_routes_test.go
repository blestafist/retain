package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/notes"
)

const contentTestPassword = "correct horse battery staple"

func newContentRoutesFixture(t *testing.T) (*db.DB, http.Handler, *http.Cookie, int64) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "content.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})

	authentication := auth.NewService(database, clock.Real{})
	if _, err := authentication.CreateAdmin(context.Background(), "content@example.test", "Content owner", contentTestPassword); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	login, err := authentication.Login(context.Background(), auth.LoginInput{
		Email: "content@example.test", Password: contentTestPassword,
	}, "content-routes-test")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	mux := http.NewServeMux()
	RegisterContentRoutes(mux, authentication, decks.NewService(database, clock.Real{}), notes.NewService(database, clock.Real{}))
	return database, mux, &http.Cookie{Name: sessionCookieName, Value: login.Token}, login.User.ID
}

func contentRouteRequest(handler http.Handler, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestContentRoutesOwnerScopedCRUDTreeCountsAndCards(t *testing.T) {
	database, handler, cookie, ownerID := newContentRoutesFixture(t)

	unauthenticated := contentRouteRequest(handler, http.MethodGet, "/api/v1/decks", "", nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /decks status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
	}

	rootResponse := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks", `{"name":"German","lang_target":"de","lang_native":"pl"}`, cookie)
	if rootResponse.Code != http.StatusCreated {
		t.Fatalf("create root deck status = %d, body %s", rootResponse.Code, rootResponse.Body)
	}
	var root decks.Deck
	if err := json.Unmarshal(rootResponse.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode root deck: %v", err)
	}
	if root.ID <= 0 || root.Name != "German" || root.Children == nil {
		t.Fatalf("created root deck = %+v", root)
	}

	childResponse := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks", `{"name":"Chapter 1","parent_id":`+jsonInt(root.ID)+`,"reverse_cards":true}`, cookie)
	if childResponse.Code != http.StatusCreated {
		t.Fatalf("create child deck status = %d, body %s", childResponse.Code, childResponse.Body)
	}
	var child decks.Deck
	if err := json.Unmarshal(childResponse.Body.Bytes(), &child); err != nil {
		t.Fatalf("decode child deck: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != root.ID || !child.ReverseCards {
		t.Fatalf("created child deck = %+v", child)
	}

	createNote := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks/"+jsonInt(child.ID)+"/notes", `{"term":"café","translation":"kawa","tags":["food"],"alternatives":["kaffe"],"fields":{"kind":"word","pos":"noun"}}`, cookie)
	if createNote.Code != http.StatusCreated {
		t.Fatalf("create note status = %d, body %s", createNote.Code, createNote.Body)
	}
	var note notes.Note
	if err := json.Unmarshal(createNote.Body.Bytes(), &note); err != nil {
		t.Fatalf("decode note: %v", err)
	}
	if note.ID <= 0 || note.DeckID != child.ID || note.Term != "café" || len(note.Tags) != 1 || note.Fields["kind"] != "word" {
		t.Fatalf("created note = %+v", note)
	}
	cards, err := database.Queries().ListCardsByNote(context.Background(), gen.ListCardsByNoteParams{NoteID: note.ID, UserID: ownerID})
	if err != nil || len(cards) != 2 || cards[0].Direction != "forward" || cards[1].Direction != "reverse" {
		t.Fatalf("created cards = %+v, %v; want forward and reverse", cards, err)
	}

	duplicate := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks/"+jsonInt(child.ID)+"/notes", `{"term":"café","translation":"another translation"}`, cookie)
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), `"code":"conflict"`) {
		t.Fatalf("NFC duplicate status/body = %d/%s, want conflict", duplicate.Code, duplicate.Body)
	}

	listResponse := contentRouteRequest(handler, http.MethodGet, "/api/v1/decks", "", cookie)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list decks status = %d, body %s", listResponse.Code, listResponse.Body)
	}
	var tree []decks.Deck
	if err := json.Unmarshal(listResponse.Body.Bytes(), &tree); err != nil {
		t.Fatalf("decode deck tree: %v", err)
	}
	if len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("deck tree = %+v", tree)
	}
	if tree[0].Counts.Notes != 1 || tree[0].Counts.New != 2 || tree[0].Children[0].Counts.Notes != 1 || tree[0].Children[0].Counts.New != 2 {
		t.Errorf("deck counts root/child = %+v / %+v, want 1 note and 2 new cards", tree[0].Counts, tree[0].Children[0].Counts)
	}

	patchDeck := contentRouteRequest(handler, http.MethodPatch, "/api/v1/decks/"+jsonInt(child.ID), `{"name":"Chapter One"}`, cookie)
	if patchDeck.Code != http.StatusOK {
		t.Fatalf("PATCH deck status = %d, body %s", patchDeck.Code, patchDeck.Body)
	}
	var updatedDeck decks.Deck
	if err := json.Unmarshal(patchDeck.Body.Bytes(), &updatedDeck); err != nil {
		t.Fatalf("decode updated deck: %v", err)
	}
	if updatedDeck.Name != "Chapter One" || !updatedDeck.ReverseCards || updatedDeck.ParentID == nil || *updatedDeck.ParentID != root.ID {
		t.Errorf("PATCH deck did not preserve omitted fields: %+v", updatedDeck)
	}
	invalidArticleRating := contentRouteRequest(handler, http.MethodPatch, "/api/v1/decks/"+jsonInt(child.ID), `{"wrong_article_rating":3}`, cookie)
	if invalidArticleRating.Code != http.StatusBadRequest {
		t.Errorf("unsupported wrong-article rating status = %d, body %s, want 400", invalidArticleRating.Code, invalidArticleRating.Body)
	}
	articleRating := contentRouteRequest(handler, http.MethodPatch, "/api/v1/decks/"+jsonInt(child.ID), `{"wrong_article_rating":1}`, cookie)
	if articleRating.Code != http.StatusOK || !strings.Contains(articleRating.Body.String(), `"wrong_article_rating":1`) {
		t.Errorf("valid wrong-article rating response = %d/%s, want saved Again rating", articleRating.Code, articleRating.Body)
	}
	cycle := contentRouteRequest(handler, http.MethodPatch, "/api/v1/decks/"+jsonInt(root.ID), `{"parent_id":`+jsonInt(child.ID)+`}`, cookie)
	if cycle.Code != http.StatusBadRequest {
		t.Errorf("cycle PATCH status = %d, body %s, want 400", cycle.Code, cycle.Body)
	}
	clearParent := contentRouteRequest(handler, http.MethodPatch, "/api/v1/decks/"+jsonInt(child.ID), `{"parent_id":null}`, cookie)
	if clearParent.Code != http.StatusOK {
		t.Fatalf("clear deck parent status = %d, body %s", clearParent.Code, clearParent.Body)
	}
	clearParent = contentRouteRequest(handler, http.MethodPatch, "/api/v1/decks/"+jsonInt(child.ID), `{"parent_id":`+jsonInt(root.ID)+`}`, cookie)
	if clearParent.Code != http.StatusOK {
		t.Fatalf("restore deck parent status = %d, body %s", clearParent.Code, clearParent.Body)
	}

	patchNote := contentRouteRequest(handler, http.MethodPatch, "/api/v1/notes/"+jsonInt(note.ID), `{"example":"Ich trinke Kaffee.","fields":{"kind":"phrase"}}`, cookie)
	if patchNote.Code != http.StatusOK {
		t.Fatalf("PATCH note status = %d, body %s", patchNote.Code, patchNote.Body)
	}
	var updatedNote notes.Note
	if err := json.Unmarshal(patchNote.Body.Bytes(), &updatedNote); err != nil {
		t.Fatalf("decode updated note: %v", err)
	}
	if updatedNote.Term != note.Term || updatedNote.Example == nil || *updatedNote.Example != "Ich trinke Kaffee." ||
		updatedNote.Fields["kind"] != "phrase" || len(updatedNote.Alternatives) != 1 || updatedNote.Alternatives[0] != "kaffe" {
		t.Errorf("PATCH note did not preserve omitted fields: %+v", updatedNote)
	}
	listNotes := contentRouteRequest(handler, http.MethodGet, "/api/v1/decks/"+jsonInt(child.ID)+"/notes", "", cookie)
	if listNotes.Code != http.StatusOK || !strings.Contains(listNotes.Body.String(), `"term":"café"`) {
		t.Errorf("GET notes status/body = %d/%s", listNotes.Code, listNotes.Body)
	}

	// A second owner's deck and note must be indistinguishable from missing resources.
	other, err := database.Queries().CreateUser(context.Background(), gen.CreateUserParams{
		Email: "another-owner@example.test", Name: "Another owner", PasswordHash: "unused",
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	foreignDeck, err := database.Queries().CreateDeck(context.Background(), gen.CreateDeckParams{
		OwnerID: other.ID, Name: "Private deck", WrongArticleRating: 2,
	})
	if err != nil {
		t.Fatalf("create foreign deck: %v", err)
	}
	foreignNote, err := database.Queries().CreateNote(context.Background(), gen.CreateNoteParams{
		Term: "secret", TermKey: "secret", Translation: "private", Tags: "[]", Alternatives: "[]", Fields: "{}",
		DeckID: foreignDeck.ID, UserID: other.ID,
	})
	if err != nil {
		t.Fatalf("create foreign note: %v", err)
	}
	for _, item := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/decks/" + jsonInt(foreignDeck.ID) + "/notes", ""},
		{http.MethodPatch, "/api/v1/notes/" + jsonInt(foreignNote.ID), `{"term":"stolen"}`},
		{http.MethodDelete, "/api/v1/notes/" + jsonInt(foreignNote.ID), ""},
		{http.MethodDelete, "/api/v1/decks/" + jsonInt(foreignDeck.ID), ""},
		{http.MethodGet, "/api/v1/decks/999999/notes", ""},
	} {
		response := contentRouteRequest(handler, item.method, item.path, item.body, cookie)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
			t.Errorf("%s %s status/body = %d/%s, want not_found", item.method, item.path, response.Code, response.Body)
		}
	}
	foreignParent := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks", `{"name":"Invalid child","parent_id":`+jsonInt(foreignDeck.ID)+`}`, cookie)
	if foreignParent.Code != http.StatusNotFound {
		t.Errorf("create child under foreign deck status/body = %d/%s, want not_found", foreignParent.Code, foreignParent.Body)
	}

	deletedNote := contentRouteRequest(handler, http.MethodDelete, "/api/v1/notes/"+jsonInt(note.ID), "", cookie)
	if deletedNote.Code != http.StatusNoContent || deletedNote.Body.Len() != 0 {
		t.Fatalf("DELETE note status/body = %d/%q", deletedNote.Code, deletedNote.Body.String())
	}
	if cards, err := database.Queries().ListCardsByNote(context.Background(), gen.ListCardsByNoteParams{NoteID: note.ID, UserID: ownerID}); err != nil || len(cards) != 0 {
		t.Errorf("cards after note delete = %+v, %v; want no active cards", cards, err)
	}
	newRootNote := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks/"+jsonInt(root.ID)+"/notes", `{"term":"Wort","translation":"word"}`, cookie)
	if newRootNote.Code != http.StatusCreated {
		t.Fatalf("create root note before cascade delete status = %d, body %s", newRootNote.Code, newRootNote.Body)
	}
	deletedDeck := contentRouteRequest(handler, http.MethodDelete, "/api/v1/decks/"+jsonInt(root.ID), "", cookie)
	if deletedDeck.Code != http.StatusNoContent {
		t.Fatalf("DELETE deck status/body = %d/%s", deletedDeck.Code, deletedDeck.Body)
	}
	for _, deckID := range []int64{root.ID, child.ID} {
		response := contentRouteRequest(handler, http.MethodGet, "/api/v1/decks/"+jsonInt(deckID)+"/notes", "", cookie)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET notes from deleted deck %d status/body = %d/%s, want 404", deckID, response.Code, response.Body)
		}
	}
}

func TestContentNoteCreationIsAtomicWhenReverseCardFails(t *testing.T) {
	database, handler, cookie, ownerID := newContentRoutesFixture(t)
	deckResponse := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks", `{"name":"Reverse deck","reverse_cards":true}`, cookie)
	if deckResponse.Code != http.StatusCreated {
		t.Fatalf("create deck status = %d, body %s", deckResponse.Code, deckResponse.Body)
	}
	var deck decks.Deck
	if err := json.Unmarshal(deckResponse.Body.Bytes(), &deck); err != nil {
		t.Fatalf("decode deck: %v", err)
	}
	if _, err := database.SQL.ExecContext(context.Background(), `
		CREATE TRIGGER reject_reverse_card BEFORE INSERT ON cards
		WHEN NEW.direction = 'reverse'
		BEGIN SELECT RAISE(ABORT, 'test reverse failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	response := contentRouteRequest(handler, http.MethodPost, "/api/v1/decks/"+jsonInt(deck.ID)+"/notes", `{"term":"Hallo","translation":"cześć"}`, cookie)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("create note status/body = %d/%s, want internal error", response.Code, response.Body)
	}
	notes, err := database.Queries().ListNotesByDeck(context.Background(), gen.ListNotesByDeckParams{DeckID: deck.ID, OwnerID: ownerID})
	if err != nil || len(notes) != 0 {
		t.Errorf("notes after failed transaction = %+v, %v; want none", notes, err)
	}
	cards, err := database.Queries().ListCardsByUser(context.Background(), ownerID)
	if err != nil || len(cards) != 0 {
		t.Errorf("cards after failed transaction = %+v, %v; want none", cards, err)
	}
}

func jsonInt(value int64) string { return strconv.FormatInt(value, 10) }
