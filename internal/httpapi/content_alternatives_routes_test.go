package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/notes"
)

func TestAddNoteAlternativeRouteIsAtomicIdempotentAndOwnerScoped(t *testing.T) {
	database, mux, cookie, ownerID := newContentRoutesFixture(t)
	ownerDeck, err := database.Queries().CreateDeck(context.Background(), gen.CreateDeckParams{
		OwnerID: ownerID, Name: "Owner deck", WrongArticleRating: 2,
	})
	if err != nil {
		t.Fatalf("create owner deck: %v", err)
	}
	ownerNote, err := database.Queries().CreateNote(context.Background(), gen.CreateNoteParams{
		DeckID: ownerDeck.ID, UserID: ownerID, Term: "hello", TermKey: "hello", Translation: "cześć",
		Tags: "[]", Alternatives: `[]`, Fields: "{}",
	})
	if err != nil {
		t.Fatalf("create owner note: %v", err)
	}

	other, err := database.Queries().CreateUser(context.Background(), gen.CreateUserParams{
		Email: "alternative-owner@example.test", Name: "Another owner", PasswordHash: "unused",
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherDeck, err := database.Queries().CreateDeck(context.Background(), gen.CreateDeckParams{
		OwnerID: other.ID, Name: "Private deck", WrongArticleRating: 2,
	})
	if err != nil {
		t.Fatalf("create other deck: %v", err)
	}
	otherNote, err := database.Queries().CreateNote(context.Background(), gen.CreateNoteParams{
		DeckID: otherDeck.ID, UserID: other.ID, Term: "secret", TermKey: "secret", Translation: "private",
		Tags: "[]", Alternatives: `[]`, Fields: "{}",
	})
	if err != nil {
		t.Fatalf("create other note: %v", err)
	}

	handler := RequireSameOrigin("http://example.test", mux)
	request := func(target, body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://example.test"+target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	path := "/api/v1/notes/" + jsonInt(ownerNote.ID) + "/alternatives"

	blocked := request(path, `{"alternative":"blocked"}`, "")
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("missing-Origin response = %d, want 403", blocked.Code)
	}

	for _, body := range []string{`{"alternative":"  hello there  "}`, `{"alternative":"hello there"}`} {
		response := request(path, body, "http://example.test")
		if response.Code != http.StatusOK {
			t.Fatalf("sequential append response = %d %s, want 200", response.Code, response.Body)
		}
	}

	for _, alternative := range []string{"first concurrent", "second concurrent"} {
		if response := request(path, `{"alternative":"`+alternative+`"}`, "http://example.test"); response.Code != http.StatusOK {
			t.Fatalf("append %q response = %d %s, want 200", alternative, response.Code, response.Body)
		}
	}

	concurrentValues := []string{"third concurrent", "fourth concurrent"}
	responses := make([]*httptest.ResponseRecorder, len(concurrentValues))
	var wait sync.WaitGroup
	for i, alternative := range concurrentValues {
		wait.Add(1)
		go func(index int, value string) {
			defer wait.Done()
			responses[index] = request(path, `{"alternative":"`+value+`"}`, "http://example.test")
		}(i, alternative)
	}
	wait.Wait()
	for i, response := range responses {
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent append response %d = %d %s, want 200", i, response.Code, response.Body)
		}
	}

	list, err := database.Queries().ListNotesByDeck(context.Background(), gen.ListNotesByDeckParams{DeckID: ownerDeck.ID, OwnerID: ownerID})
	if err != nil || len(list) != 1 {
		t.Fatalf("list owner note after appends = %d, %v", len(list), err)
	}
	var got []string
	if err := json.Unmarshal([]byte(list[0].Alternatives), &got); err != nil {
		t.Fatalf("decode alternatives %q: %v", list[0].Alternatives, err)
	}
	sort.Strings(got)
	want := []string{"first concurrent", "fourth concurrent", "hello there", "second concurrent", "third concurrent"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("alternatives = %q, want %q", got, want)
	}

	for _, noteID := range []int64{otherNote.ID, 999999} {
		response := request("/api/v1/notes/"+jsonInt(noteID)+"/alternatives", `{"alternative":"private"}`, "http://example.test")
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
			t.Errorf("foreign/missing note response = %d %s, want owner-scoped 404", response.Code, response.Body)
		}
	}

	for _, body := range []string{
		`{"alternative":"   "}`,
		`{"alternative":"` + strings.Repeat("x", 513) + `"}`,
		`{"alternative":"` + strings.Repeat("Ж", 201) + `"}`,
	} {
		response := request(path, body, "http://example.test")
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid alternative response = %d %s, want 400", response.Code, response.Body)
		}
	}

	if _, err := database.Queries().SoftDeleteNote(context.Background(), gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC), Valid: true},
		UpdatedAt: time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC), ID: ownerNote.ID, OwnerID: ownerID,
	}); err != nil {
		t.Fatalf("soft-delete owner note: %v", err)
	}
	deleted := request(path, `{"alternative":"after deletion"}`, "http://example.test")
	if deleted.Code != http.StatusNotFound {
		t.Errorf("deleted note response = %d %s, want 404", deleted.Code, deleted.Body)
	}
}

func TestAddNoteAlternativeRejectsInvalidUTF8AtServiceBoundary(t *testing.T) {
	database, _, _, ownerID := newContentRoutesFixture(t)
	service := notes.NewService(database, nil)
	if _, err := service.AddAlternative(context.Background(), ownerID, 1, string([]byte{0xff})); err != notes.ErrInvalidInput {
		t.Fatalf("AddAlternative(invalid UTF-8) error = %v, want %v", err, notes.ErrInvalidInput)
	}
}
