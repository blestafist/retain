package decks

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

type deckTestClock struct{ now time.Time }

func (c deckTestClock) Now() time.Time { return c.now }

var _ clock.Clock = deckTestClock{}

func TestDeckTreeCRUDCountsAndOwnerScope(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "decks.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	queries := database.Queries()
	owner, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "decks-owner@example.test", Name: "Deck owner", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	other, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "decks-other@example.test", Name: "Other owner", PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser(other) error = %v", err)
	}
	service := NewService(database, deckTestClock{now})
	root, err := service.Create(ctx, owner.ID, CreateInput{Name: " German ", LangTarget: stringPtr("de"), LangNative: stringPtr("pl")})
	if err != nil {
		t.Fatalf("Create(root) error = %v", err)
	}
	child, err := service.Create(ctx, owner.ID, CreateInput{Name: "Unit 1", ParentID: &root.ID, ReverseCards: true})
	if err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}
	foreign, err := service.Create(ctx, other.ID, CreateInput{Name: "Private"})
	if err != nil {
		t.Fatalf("Create(foreign) error = %v", err)
	}
	if _, err := service.Create(ctx, owner.ID, CreateInput{Name: "Invalid", ParentID: &foreign.ID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Create(child under another owner) error = %v, want ErrNotFound", err)
	}
	if _, err := service.Update(ctx, owner.ID, root.ID, PatchInput{ParentID: OptionalInt64{Set: true, Value: &child.ID}}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Update(cyclic parent) error = %v, want ErrInvalidInput", err)
	}

	note, err := queries.CreateNote(ctx, gen.CreateNoteParams{
		Term: "Hallo", TermKey: "hallo", Translation: "Cześć", Tags: "[]", Alternatives: "[]", Fields: "{}",
		DeckID: child.ID, UserID: owner.ID,
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "forward"}); err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	tree, err := service.List(ctx, owner.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(tree) != 1 || tree[0].ID != root.ID || len(tree[0].Children) != 1 || tree[0].Children[0].ID != child.ID {
		t.Fatalf("List() tree = %+v, want root and child", tree)
	}
	if tree[0].Counts == nil || tree[0].Counts.Notes != 1 || tree[0].Counts.New != 1 || tree[0].Children[0].Counts.Notes != 1 {
		t.Errorf("List() counts = %+v, child = %+v; want one note and new card", tree[0].Counts, tree[0].Children[0].Counts)
	}

	newName := "Renamed unit"
	updated, err := service.Update(ctx, owner.ID, child.ID, PatchInput{Name: &newName})
	if err != nil || updated.Name != newName || updated.ParentID == nil || *updated.ParentID != root.ID || !updated.ReverseCards {
		t.Errorf("Update(child name) = %+v, %v; want updated name and preserved fields", updated, err)
	}
	if err := service.Delete(ctx, owner.ID, root.ID); err != nil {
		t.Fatalf("Delete(root) error = %v", err)
	}
	if _, err := service.List(ctx, owner.ID); err != nil {
		t.Fatalf("List() after delete error = %v", err)
	} else if tree, _ := service.List(ctx, owner.ID); len(tree) != 0 {
		t.Errorf("List() after deleting root = %+v, want empty", tree)
	}
}

func stringPtr(value string) *string { return &value }
