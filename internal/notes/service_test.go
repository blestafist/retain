package notes

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/decks"
)

func TestGetIsOwnerScopedAndRequiresActiveNoteAndDeck(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "notes-get.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	now := time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC)
	queries := database.Queries()
	owner, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "notes-get@example.test", Name: "Notes Get", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "notes-get-other@example.test", Name: "Other", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	deckService := decks.NewService(database, clock.Real{})
	ownerDeck, err := deckService.Create(ctx, owner.ID, decks.CreateInput{Name: "Owner deck"})
	if err != nil {
		t.Fatalf("Create(owner deck) error = %v", err)
	}
	otherDeck, err := deckService.Create(ctx, other.ID, decks.CreateInput{Name: "Other deck"})
	if err != nil {
		t.Fatalf("Create(other deck) error = %v", err)
	}
	service := NewService(database, clock.Real{})
	ownerNote, err := service.Create(ctx, owner.ID, ownerDeck.ID, CreateInput{Term: "Hallo", Translation: "cześć"})
	if err != nil {
		t.Fatalf("Create(owner note) error = %v", err)
	}
	foreignNote, err := service.Create(ctx, other.ID, otherDeck.ID, CreateInput{Term: "Geheim", Translation: "sekret"})
	if err != nil {
		t.Fatalf("Create(foreign note) error = %v", err)
	}

	got, err := service.Get(ctx, owner.ID, ownerNote.ID)
	if err != nil || got.ID != ownerNote.ID || got.Term != "Hallo" || got.Translation != "cześć" {
		t.Fatalf("Get(owned note) = %+v, %v", got, err)
	}
	for _, noteID := range []int64{foreignNote.ID, ownerNote.ID + 100000} {
		if _, err := service.Get(ctx, owner.ID, noteID); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(note %d) error = %v, want ErrNotFound", noteID, err)
		}
	}

	if err := service.Delete(ctx, owner.ID, ownerNote.ID); err != nil {
		t.Fatalf("Delete(note) error = %v", err)
	}
	if _, err := service.Get(ctx, owner.ID, ownerNote.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted note) error = %v, want ErrNotFound", err)
	}

	deckNote, err := service.Create(ctx, owner.ID, ownerDeck.ID, CreateInput{Term: "Wort", Translation: "słowo"})
	if err != nil {
		t.Fatalf("Create(note in deck) error = %v", err)
	}
	if err := deckService.Delete(ctx, owner.ID, ownerDeck.ID); err != nil {
		t.Fatalf("Delete(deck) error = %v", err)
	}
	if _, err := service.Get(ctx, owner.ID, deckNote.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(note in deleted deck) error = %v, want ErrNotFound", err)
	}
}
