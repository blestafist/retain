package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db/gen"
)

func createContentDeck(t *testing.T, queries *gen.Queries, ownerID int64, name string, parentID sql.NullInt64) gen.Deck {
	t.Helper()

	deck, err := queries.CreateDeck(context.Background(), gen.CreateDeckParams{
		OwnerID:            ownerID,
		ParentID:           parentID,
		Name:               name,
		Position:           0,
		WrongArticleRating: 2,
	})
	if err != nil {
		t.Fatalf("CreateDeck(%q) error = %v", name, err)
	}
	return deck
}

func createContentNote(t *testing.T, queries *gen.Queries, userID, deckID int64, term, termKey, externalID string) gen.Note {
	t.Helper()

	params := gen.CreateNoteParams{
		Term:         term,
		TermKey:      termKey,
		Translation:  "translation of " + term,
		Tags:         "[]",
		Alternatives: "[]",
		Fields:       "{}",
		DeckID:       deckID,
		UserID:       userID,
	}
	if externalID != "" {
		params.ExternalID = sql.NullString{String: externalID, Valid: true}
	}
	note, err := queries.CreateNote(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateNote(%q) error = %v", term, err)
	}
	return note
}

func contentTime(hour int) time.Time {
	return time.Date(2026, time.March, 4, hour, 5, 6, 0, time.UTC)
}

func createIdentifiedContentDeck(t *testing.T, queries *gen.Queries, ownerID int64, name string, parentID sql.NullInt64, source, externalID string) gen.Deck {
	t.Helper()
	deck, err := queries.CreateDeck(context.Background(), gen.CreateDeckParams{
		OwnerID:            ownerID,
		ParentID:           parentID,
		Name:               name,
		WrongArticleRating: 2,
		Source:             sql.NullString{String: source, Valid: true},
		ExternalID:         sql.NullString{String: externalID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateDeck(%q, %q/%q) error = %v", name, source, externalID, err)
	}
	return deck
}

type contentCardProgress struct {
	state         int64
	due           time.Time
	stability     float64
	difficulty    float64
	elapsedDays   int64
	scheduledDays int64
	step          int64
	reps          int64
	lapses        int64
	lastReview    time.Time
	suspended     bool
	isLeech       bool
}

func setContentCardProgress(t *testing.T, database *DB, cardID int64, index int64) contentCardProgress {
	t.Helper()
	progress := contentCardProgress{
		state:         2,
		due:           contentTime(int(index) + 12),
		stability:     4.5 + float64(index),
		difficulty:    3.5 + float64(index)/10,
		elapsedDays:   10 + index,
		scheduledDays: 20 + index,
		step:          index,
		reps:          30 + index,
		lapses:        index + 1,
		lastReview:    contentTime(int(index) + 2),
		suspended:     true,
		isLeech:       index%2 == 0,
	}
	_, err := database.SQL.ExecContext(context.Background(), `
		UPDATE cards
		SET state = ?, due = ?, stability = ?, difficulty = ?, elapsed_days = ?,
		    scheduled_days = ?, step = ?, reps = ?, lapses = ?, last_review = ?,
		    suspended = ?, is_leech = ?, updated_at = ?
		WHERE id = ?`,
		progress.state, progress.due, progress.stability, progress.difficulty,
		progress.elapsedDays, progress.scheduledDays, progress.step, progress.reps,
		progress.lapses, progress.lastReview, progress.suspended, progress.isLeech,
		contentTime(20), cardID,
	)
	if err != nil {
		t.Fatalf("set card %d FSRS progress: %v", cardID, err)
	}
	return progress
}

func assertContentCardProgress(t *testing.T, queries *gen.Queries, ownerID, cardID int64, want contentCardProgress) {
	t.Helper()
	got, err := queries.GetCard(context.Background(), gen.GetCardParams{ID: cardID, UserID: ownerID})
	if err != nil {
		t.Fatalf("GetCard(%d) error = %v", cardID, err)
	}
	if got.State != want.state || !got.Due.Equal(want.due) || !got.Stability.Valid || got.Stability.Float64 != want.stability ||
		!got.Difficulty.Valid || got.Difficulty.Float64 != want.difficulty || got.ElapsedDays != want.elapsedDays ||
		got.ScheduledDays != want.scheduledDays || got.Step != want.step || got.Reps != want.reps || got.Lapses != want.lapses ||
		!got.LastReview.Valid || !got.LastReview.Time.Equal(want.lastReview) || got.Suspended != want.suspended || got.IsLeech != want.isLeech ||
		got.DeletedAt.Valid || got.DeletionBatch.Valid {
		t.Errorf("card %d progress = %+v, want %+v", cardID, got, want)
	}
}

func TestContentQueriesCRUDAndOwnerScope(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	owner := createTestUser(t, queries, "content-owner@example.com", false)
	other := createTestUser(t, queries, "content-other@example.com", false)

	otherRoot := createContentDeck(t, queries, other.ID, "Other root", sql.NullInt64{})
	root := createContentDeck(t, queries, owner.ID, "German", sql.NullInt64{})
	child := createContentDeck(t, queries, owner.ID, "Chapter 1", sql.NullInt64{Int64: root.ID, Valid: true})
	grandchild := createContentDeck(t, queries, owner.ID, "Lesson 1", sql.NullInt64{Int64: child.ID, Valid: true})
	if _, err := queries.CreateDeck(ctx, gen.CreateDeckParams{
		OwnerID: owner.ID, ParentID: sql.NullInt64{Int64: otherRoot.ID, Valid: true}, Name: "Cross-owner child", WrongArticleRating: 2,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("CreateDeck(with another owner's parent) error = %v, want sql.ErrNoRows", err)
	}
	if root.ParentID.Valid || root.LangTarget.Valid || root.LangNative.Valid || child.ParentID.Int64 != root.ID {
		t.Fatalf("deck inheritance fields = root %+v, child %+v", root, child)
	}
	if got, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: root.ID, OwnerID: owner.ID}); err != nil || got != root {
		t.Fatalf("GetDeck(root) = %+v, %v; want %+v, nil", got, err, root)
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: root.ID, OwnerID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeck(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListDecksByOwner(ctx, owner.ID); err != nil || len(got) != 3 {
		t.Fatalf("ListDecksByOwner() = %d decks, %v; want 3, nil", len(got), err)
	}
	if got, err := queries.ListChildDecks(ctx, gen.ListChildDecksParams{
		OwnerID: owner.ID, ParentID: sql.NullInt64{Int64: root.ID, Valid: true},
	}); err != nil || len(got) != 1 || got[0].ID != child.ID {
		t.Fatalf("ListChildDecks() = %+v, %v; want child %d", got, err, child.ID)
	}
	if got, err := queries.ListDecksByOwner(ctx, other.ID); err != nil || len(got) != 1 || got[0].ID != otherRoot.ID {
		t.Fatalf("ListDecksByOwner(other) = %+v, %v; want only the other owner's deck", got, err)
	}

	updatedDeckAt := contentTime(7)
	updatedDeck, err := queries.UpdateDeck(ctx, gen.UpdateDeckParams{
		ParentID:           child.ParentID,
		Name:               "First chapter",
		Description:        sql.NullString{String: "Updated description", Valid: true},
		Position:           3,
		LangTarget:         sql.NullString{String: "de", Valid: true},
		LangNative:         sql.NullString{String: "pl", Valid: true},
		ReverseCards:       true,
		WrongArticleRating: 3,
		ColorSeed:          sql.NullString{String: "#123456", Valid: true},
		UpdatedAt:          updatedDeckAt,
		ID:                 child.ID,
		OwnerID:            owner.ID,
	})
	if err != nil {
		t.Fatalf("UpdateDeck() error = %v", err)
	}
	if updatedDeck.Name != "First chapter" || updatedDeck.Position != 3 || !updatedDeck.ReverseCards ||
		updatedDeck.LangTarget.String != "de" || updatedDeck.LangNative.String != "pl" ||
		!updatedDeck.UpdatedAt.Equal(updatedDeckAt) {
		t.Errorf("UpdateDeck() = %+v, want updated deck fields", updatedDeck)
	}
	for name, parentID := range map[string]int64{
		"self":       child.ID,
		"descendant": grandchild.ID,
	} {
		cycleParams := gen.UpdateDeckParams{
			ParentID:           sql.NullInt64{Int64: parentID, Valid: true},
			Name:               updatedDeck.Name,
			Description:        updatedDeck.Description,
			Position:           updatedDeck.Position,
			LangTarget:         updatedDeck.LangTarget,
			LangNative:         updatedDeck.LangNative,
			ReverseCards:       updatedDeck.ReverseCards,
			WrongArticleRating: updatedDeck.WrongArticleRating,
			ColorSeed:          updatedDeck.ColorSeed,
			UpdatedAt:          updatedDeckAt,
			ID:                 child.ID,
			OwnerID:            owner.ID,
		}
		if _, err := queries.UpdateDeck(ctx, cycleParams); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("UpdateDeck(parent=%s) error = %v, want sql.ErrNoRows", name, err)
		}
	}
	if _, err := queries.UpdateDeck(ctx, gen.UpdateDeckParams{
		ParentID:           sql.NullInt64{Int64: grandchild.ID, Valid: true},
		Name:               root.Name,
		Description:        root.Description,
		Position:           root.Position,
		LangTarget:         root.LangTarget,
		LangNative:         root.LangNative,
		ReverseCards:       root.ReverseCards,
		WrongArticleRating: root.WrongArticleRating,
		ColorSeed:          root.ColorSeed,
		UpdatedAt:          updatedDeckAt,
		ID:                 root.ID,
		OwnerID:            owner.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateDeck(root parent=deep descendant) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.UpdateDeck(ctx, gen.UpdateDeckParams{ID: child.ID, OwnerID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateDeck(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.UpdateDeck(ctx, gen.UpdateDeckParams{
		ParentID: sql.NullInt64{Int64: otherRoot.ID, Valid: true}, Name: "Cross-owner move", WrongArticleRating: 2,
		UpdatedAt: contentTime(7), ID: child.ID, OwnerID: owner.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateDeck(with another owner's parent) error = %v, want sql.ErrNoRows", err)
	}

	hash := make([]byte, 32)
	hash[0] = 7
	batch, err := queries.CreateImportBatch(ctx, gen.CreateImportBatchParams{
		ID:        "batch-content-1",
		UserID:    owner.ID,
		Adapter:   "retain",
		Filename:  sql.NullString{String: "words.json", Valid: true},
		Raw:       []byte("compressed input"),
		RawSha256: hash,
	})
	if err != nil {
		t.Fatalf("CreateImportBatch() error = %v", err)
	}
	if batch.Status != "previewed" || batch.Summary != "{}" || batch.AppliedAt.Valid || batch.CreatedAt.IsZero() {
		t.Errorf("CreateImportBatch() defaults = %+v", batch)
	}
	if got, err := queries.GetImportBatch(ctx, gen.GetImportBatchParams{ID: batch.ID, UserID: owner.ID}); err != nil || got.ID != batch.ID {
		t.Fatalf("GetImportBatch() = %+v, %v", got, err)
	}
	if _, err := queries.GetImportBatch(ctx, gen.GetImportBatchParams{ID: batch.ID, UserID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetImportBatch(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListImportBatches(ctx, owner.ID); err != nil || len(got) != 1 || got[0].ID != batch.ID {
		t.Fatalf("ListImportBatches() = %+v, %v; want batch", got, err)
	}
	if got, err := queries.ListImportBatches(ctx, other.ID); err != nil || len(got) != 0 {
		t.Fatalf("ListImportBatches(other) = %+v, %v; want empty", got, err)
	}
	appliedAt := contentTime(8)
	updatedBatch, err := queries.UpdateImportBatch(ctx, gen.UpdateImportBatchParams{
		Adapter:   "retain",
		Filename:  batch.Filename,
		Raw:       batch.Raw,
		RawSha256: hash,
		Status:    "applied",
		Summary:   `{"created":1}`,
		AppliedAt: sql.NullTime{Time: appliedAt, Valid: true},
		ID:        batch.ID,
		UserID:    owner.ID,
	})
	if err != nil || updatedBatch.Status != "applied" || updatedBatch.Summary != `{"created":1}` ||
		!updatedBatch.AppliedAt.Valid || !updatedBatch.AppliedAt.Time.Equal(appliedAt) {
		t.Fatalf("UpdateImportBatch() = %+v, %v", updatedBatch, err)
	}
	if _, err := queries.UpdateImportBatch(ctx, gen.UpdateImportBatchParams{ID: batch.ID, UserID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateImportBatch(other owner) error = %v, want sql.ErrNoRows", err)
	}
	foreignBatch, err := queries.CreateImportBatch(ctx, gen.CreateImportBatchParams{
		ID: "other-owner-batch", UserID: other.ID, Adapter: "retain", Raw: []byte("other input"), RawSha256: make([]byte, 32),
	})
	if err != nil {
		t.Fatalf("CreateImportBatch(other owner) error = %v", err)
	}

	noteParams := gen.CreateNoteParams{
		Term:               "Guten Morgen",
		TermKey:            "guten morgen",
		Translation:        "dzień dobry",
		Example:            sql.NullString{String: "Guten Morgen!", Valid: true},
		ExampleTranslation: sql.NullString{String: "Dzień dobry!", Valid: true},
		Tags:               `["greeting"]`,
		Alternatives:       `["dzień dobry"]`,
		Fields:             `{"kind":"phrase"}`,
		ImportBatchID:      sql.NullString{String: batch.ID, Valid: true},
		DeckID:             root.ID,
		UserID:             owner.ID,
	}
	note, err := queries.CreateNote(ctx, noteParams)
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if note.Example.String != "Guten Morgen!" || note.Tags != `["greeting"]` || note.Fields != `{"kind":"phrase"}` {
		t.Errorf("CreateNote() = %+v, want optional and JSON fields", note)
	}
	if _, err := queries.CreateNote(ctx, gen.CreateNoteParams{
		Term: "Unauthorised", TermKey: "unauthorised", Translation: "not allowed", Tags: "[]", Alternatives: "[]", Fields: "{}",
		DeckID: root.ID, UserID: other.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("CreateNote(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.GetNote(ctx, gen.GetNoteParams{ID: note.ID, OwnerID: owner.ID}); err != nil || got != note {
		t.Fatalf("GetNote() = %+v, %v; want %+v", got, err, note)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: note.ID, OwnerID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetNote(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: root.ID, OwnerID: owner.ID}); err != nil || len(got) != 1 || got[0].ID != note.ID {
		t.Fatalf("ListNotesByDeck() = %+v, %v; want note", got, err)
	}
	if got, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: root.ID, OwnerID: other.ID}); err != nil || len(got) != 0 {
		t.Fatalf("ListNotesByDeck(other) = %+v, %v; want empty", got, err)
	}

	updatedNoteAt := contentTime(9)
	noteUpdateParams := gen.UpdateNoteParams{
		Term:               "Guten Tag",
		TermKey:            "guten tag",
		Translation:        "dzień dobry",
		Transcription:      sql.NullString{String: "ˈɡuːtn̩ taːk", Valid: true},
		Example:            note.Example,
		ExampleTranslation: note.ExampleTranslation,
		Notes:              sql.NullString{String: "A greeting", Valid: true},
		Tags:               note.Tags,
		Alternatives:       note.Alternatives,
		Fields:             note.Fields,
		ExternalID:         sql.NullString{String: "word-1", Valid: true},
		ImportBatchID:      noteParams.ImportBatchID,
		UpdatedAt:          updatedNoteAt,
		ID:                 note.ID,
		OwnerID:            owner.ID,
	}
	updatedNote, err := queries.UpdateNote(ctx, noteUpdateParams)
	if err != nil || updatedNote.Term != "Guten Tag" || !updatedNote.Transcription.Valid || !updatedNote.UpdatedAt.Equal(updatedNoteAt) {
		t.Fatalf("UpdateNote() = %+v, %v", updatedNote, err)
	}
	if _, err := queries.UpdateNote(ctx, gen.UpdateNoteParams{ID: note.ID, OwnerID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateNote(other owner) error = %v, want sql.ErrNoRows", err)
	}
	noteUpdateParams.ImportBatchID = sql.NullString{String: foreignBatch.ID, Valid: true}
	if _, err := queries.UpdateNote(ctx, noteUpdateParams); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateNote(with another owner's import batch) error = %v, want sql.ErrNoRows", err)
	}

	card, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "forward"})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	if card.State != 0 || card.Stability.Valid || card.Difficulty.Valid || card.ElapsedDays != 0 || card.Reps != 0 || card.Suspended || card.IsLeech || card.DeletedAt.Valid || card.DeletionBatch.Valid {
		t.Errorf("CreateCard() initial defaults = %+v", card)
	}
	progressDue := contentTime(10)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE cards SET state = 2, stability = 5, difficulty = 6, reps = 9, due = ?, updated_at = ? WHERE id = ?",
		progressDue, progressDue, card.ID,
	); err != nil {
		t.Fatalf("set card progress for re-create test: %v", err)
	}
	recreatedCard, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "forward"})
	if err != nil || recreatedCard.ID != card.ID || recreatedCard.State != 2 || recreatedCard.Stability.Float64 != 5 || recreatedCard.Reps != 9 || !recreatedCard.Due.Equal(progressDue) {
		t.Fatalf("idempotent CreateCard() = %+v, %v; want existing FSRS progress", recreatedCard, err)
	}
	reverseCard, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "reverse"})
	if err != nil || reverseCard.Direction != "reverse" || reverseCard.ID == card.ID {
		t.Fatalf("CreateCard(reverse) = %+v, %v", reverseCard, err)
	}
	if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: other.ID, Direction: "forward"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("CreateCard(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.GetCard(ctx, gen.GetCardParams{ID: card.ID, UserID: owner.ID}); err != nil || got.ID != card.ID {
		t.Fatalf("GetCard() = %+v, %v", got, err)
	}
	if _, err := queries.GetCard(ctx, gen.GetCardParams{ID: card.ID, UserID: other.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetCard(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListCardsByUser(ctx, owner.ID); err != nil || len(got) != 2 {
		t.Fatalf("ListCardsByUser() = %+v, %v; want 2 cards", got, err)
	}
	if got, err := queries.ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: note.ID, UserID: owner.ID}); err != nil || len(got) != 2 {
		t.Fatalf("ListCardsByNote() = %+v, %v; want 2 cards", got, err)
	}

	deletedAt := contentTime(11)
	deletedNote, err := queries.SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: deletedAt, Valid: true},
		UpdatedAt: deletedAt,
		ID:        note.ID,
		OwnerID:   owner.ID,
	})
	if err != nil || !deletedNote.DeletedAt.Valid || !deletedNote.DeletedAt.Time.Equal(deletedAt) {
		t.Fatalf("SoftDeleteNote() = %+v, %v", deletedNote, err)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: note.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetNote(deleted) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: root.ID, OwnerID: owner.ID}); err != nil || len(got) != 0 {
		t.Errorf("ListNotesByDeck(deleted) = %+v, %v; want empty", got, err)
	}
	if got, err := queries.ListCardsByUser(ctx, owner.ID); err != nil || len(got) != 0 {
		t.Errorf("ListCardsByUser(deleted note) = %+v, %v; want empty", got, err)
	}
	if _, err := queries.SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{ID: note.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("SoftDeleteNote(already deleted) error = %v, want sql.ErrNoRows", err)
	}
	activeNote := createContentNote(t, queries, owner.ID, root.ID, "Guten Abend", "guten abend", "")
	if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: activeNote.ID, UserID: owner.ID, Direction: "forward"}); err != nil {
		t.Fatalf("CreateCard(active note) error = %v", err)
	}

	deletedDeck, err := queries.SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
		DeletedAt: sql.NullTime{Time: deletedAt, Valid: true},
		UpdatedAt: deletedAt,
		ID:        root.ID,
		OwnerID:   owner.ID,
	})
	if err != nil || !deletedDeck.DeletedAt.Valid {
		t.Fatalf("SoftDeleteDeck() = %+v, %v", deletedDeck, err)
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: root.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeck(deleted) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListDecksByOwner(ctx, owner.ID); err != nil || len(got) != 0 {
		t.Errorf("ListDecksByOwner(after soft delete) = %+v, %v; want empty subtree", got, err)
	}
	for _, deletedDeckID := range []int64{child.ID, grandchild.ID} {
		if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: deletedDeckID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetDeck(descendant %d after cascade) error = %v, want sql.ErrNoRows", deletedDeckID, err)
		}
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: activeNote.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetNote(in deleted deck) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: root.ID, OwnerID: owner.ID}); err != nil || len(got) != 0 {
		t.Errorf("ListNotesByDeck(in deleted deck) = %+v, %v; want empty", got, err)
	}
	if got, err := queries.ListCardsByUser(ctx, owner.ID); err != nil || len(got) != 0 {
		t.Errorf("ListCardsByUser(in deleted deck) = %+v, %v; want empty", got, err)
	}
	if got, err := queries.ListChildDecks(ctx, gen.ListChildDecksParams{
		OwnerID: owner.ID, ParentID: sql.NullInt64{Int64: root.ID, Valid: true},
	}); err != nil || len(got) != 0 {
		t.Errorf("ListChildDecks(deleted parent) = %+v, %v; want empty", got, err)
	}
	if _, err := queries.SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{ID: root.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("SoftDeleteDeck(already deleted) error = %v, want sql.ErrNoRows", err)
	}
}

func TestContentCascadeRestorePreservesProgressAndIndependentDeletes(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	owner := createTestUser(t, queries, "restore-owner@example.com", false)
	other := createTestUser(t, queries, "restore-other@example.com", false)

	root := createIdentifiedContentDeck(t, queries, owner.ID, "Imported class", sql.NullInt64{}, "school", "class-1")
	child := createIdentifiedContentDeck(t, queries, owner.ID, "Imported chapter", sql.NullInt64{Int64: root.ID, Valid: true}, "school", "chapter-1")
	grandchild := createContentDeck(t, queries, owner.ID, "Imported lesson", sql.NullInt64{Int64: child.ID, Valid: true})
	rootNote := createContentNote(t, queries, owner.ID, root.ID, "root word", "root word", "root-word-1")
	childNote := createContentNote(t, queries, owner.ID, child.ID, "chapter word", "chapter word", "chapter-word-1")
	grandchildNote := createContentNote(t, queries, owner.ID, grandchild.ID, "lesson word", "lesson word", "lesson-word-1")
	deletedExternalNote := createContentNote(t, queries, owner.ID, child.ID, "deleted by external", "deleted by external", "restore-external-1")
	deletedTermNote := createContentNote(t, queries, owner.ID, child.ID, "deleted by term", "deleted by term", "restore-term-1")

	cards := make(map[int64]contentCardProgress)
	for index, note := range []gen.Note{rootNote, childNote, grandchildNote, deletedExternalNote, deletedTermNote} {
		card, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "forward"})
		if err != nil {
			t.Fatalf("CreateCard(note %d) error = %v", note.ID, err)
		}
		cards[card.ID] = setContentCardProgress(t, database, card.ID, int64(index+1))
	}
	cardIDByNote := make(map[int64]int64, len(cards))
	for _, note := range []gen.Note{rootNote, childNote, grandchildNote, deletedExternalNote, deletedTermNote} {
		card, err := queries.ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: note.ID, UserID: owner.ID})
		if err != nil || len(card) != 1 {
			t.Fatalf("ListCardsByNote(%d) = %+v, %v; want one card", note.ID, card, err)
		}
		cardIDByNote[note.ID] = card[0].ID
	}

	deletedExternalAt := contentTime(8)
	if _, err := queries.SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: deletedExternalAt, Valid: true}, UpdatedAt: deletedExternalAt,
		ID: deletedExternalNote.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteNote(external identity) error = %v", err)
	}
	deletedTermAt := contentTime(9)
	if _, err := queries.SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: deletedTermAt, Valid: true}, UpdatedAt: deletedTermAt,
		ID: deletedTermNote.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteNote(term identity) error = %v", err)
	}
	deletedDeckAt := contentTime(10)
	if _, err := queries.SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
		DeletedAt: sql.NullTime{Time: deletedDeckAt, Valid: true}, UpdatedAt: deletedDeckAt,
		ID: root.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteDeck(root) error = %v", err)
	}

	for _, id := range []int64{root.ID, child.ID, grandchild.ID} {
		if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: id, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetDeck(%d) after cascade error = %v, want sql.ErrNoRows", id, err)
		}
	}
	if got, err := queries.ListDecksByOwner(ctx, owner.ID); err != nil || len(got) != 0 {
		t.Errorf("ListDecksByOwner(after cascade) = %+v, %v; want empty", got, err)
	}
	for _, note := range []gen.Note{rootNote, childNote, grandchildNote, deletedExternalNote, deletedTermNote} {
		if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: note.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetNote(%d) after cascade error = %v, want sql.ErrNoRows", note.ID, err)
		}
		if _, err := queries.GetCard(ctx, gen.GetCardParams{ID: cardIDByNote[note.ID], UserID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetCard(%d) after cascade error = %v, want sql.ErrNoRows", cardIDByNote[note.ID], err)
		}
	}
	if got, err := queries.ListCardsByUser(ctx, owner.ID); err != nil || len(got) != 0 {
		t.Errorf("ListCardsByUser(after cascade) = %+v, %v; want empty", got, err)
	}
	if _, err := queries.GetDeletedDeckBySourceExternalID(ctx, gen.GetDeletedDeckBySourceExternalIDParams{
		OwnerID: owner.ID, Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "class-1", Valid: true},
	}); err != nil {
		t.Errorf("GetDeletedDeckBySourceExternalID() error = %v, want root", err)
	}
	if _, err := queries.GetDeletedDeckBySourceExternalID(ctx, gen.GetDeletedDeckBySourceExternalIDParams{
		OwnerID: other.ID, Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "class-1", Valid: true},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeletedDeckBySourceExternalID(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.GetDeletedNoteByExternalID(ctx, gen.GetDeletedNoteByExternalIDParams{
		DeckID: child.ID, OwnerID: owner.ID, ExternalID: sql.NullString{String: "restore-external-1", Valid: true},
	}); err != nil || got.ID != deletedExternalNote.ID {
		t.Errorf("GetDeletedNoteByExternalID() = %+v, %v; want independently deleted note", got, err)
	}
	if got, err := queries.GetDeletedNoteByTermKey(ctx, gen.GetDeletedNoteByTermKeyParams{
		DeckID: child.ID, OwnerID: owner.ID, TermKey: "deleted by term",
	}); err != nil || got.ID != deletedTermNote.ID {
		t.Errorf("GetDeletedNoteByTermKey() = %+v, %v; want independently deleted note", got, err)
	}
	if _, err := queries.GetDeletedNoteByExternalID(ctx, gen.GetDeletedNoteByExternalIDParams{
		DeckID: child.ID, OwnerID: other.ID, ExternalID: sql.NullString{String: "restore-external-1", Valid: true},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeletedNoteByExternalID(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.RestoreDeckBySourceExternalID(ctx, gen.RestoreDeckBySourceExternalIDParams{
		UpdatedAt: contentTime(11), OwnerID: other.ID,
		Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "class-1", Valid: true},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RestoreDeckBySourceExternalID(other owner) error = %v, want sql.ErrNoRows", err)
	}

	var rootBatch, childBatch, rootNoteBatch, childNoteBatch, independentlyDeletedBatch string
	for _, item := range []struct {
		query string
		id    int64
		batch *string
	}{
		{"SELECT deletion_batch FROM decks WHERE id = ?", root.ID, &rootBatch},
		{"SELECT deletion_batch FROM decks WHERE id = ?", child.ID, &childBatch},
		{"SELECT deletion_batch FROM notes WHERE id = ?", rootNote.ID, &rootNoteBatch},
		{"SELECT deletion_batch FROM notes WHERE id = ?", childNote.ID, &childNoteBatch},
		{"SELECT deletion_batch FROM notes WHERE id = ?", deletedExternalNote.ID, &independentlyDeletedBatch},
	} {
		if err := database.SQL.QueryRowContext(ctx, item.query, item.id).Scan(item.batch); err != nil {
			t.Fatalf("read deletion batch for %d: %v", item.id, err)
		}
	}
	if rootBatch == "" || rootBatch != childBatch || rootBatch != rootNoteBatch || rootBatch != childNoteBatch || rootBatch == independentlyDeletedBatch {
		t.Fatalf("deletion batches = root %q, child %q, root note %q, child note %q, independent note %q", rootBatch, childBatch, rootNoteBatch, childNoteBatch, independentlyDeletedBatch)
	}

	restoredRoot, err := queries.RestoreDeckBySourceExternalID(ctx, gen.RestoreDeckBySourceExternalIDParams{
		UpdatedAt: contentTime(11), OwnerID: owner.ID,
		Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "class-1", Valid: true},
	})
	if err != nil || restoredRoot.ID != root.ID || restoredRoot.DeletedAt.Valid || restoredRoot.DeletionBatch.Valid {
		t.Fatalf("RestoreDeckBySourceExternalID() = %+v, %v; want original active root", restoredRoot, err)
	}
	for _, id := range []int64{root.ID, child.ID, grandchild.ID} {
		if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: id, OwnerID: owner.ID}); err != nil {
			t.Errorf("GetDeck(%d) after restore error = %v", id, err)
		}
	}
	if got, err := queries.ListDecksByOwner(ctx, owner.ID); err != nil || len(got) != 3 {
		t.Errorf("ListDecksByOwner(after restore) = %d decks, %v; want original subtree", len(got), err)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: rootNote.ID, OwnerID: owner.ID}); err != nil {
		t.Errorf("GetNote(root note after restore) error = %v", err)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: childNote.ID, OwnerID: owner.ID}); err != nil {
		t.Errorf("GetNote(child note after restore) error = %v", err)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: grandchildNote.ID, OwnerID: owner.ID}); err != nil {
		t.Errorf("GetNote(grandchild note after restore) error = %v", err)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: deletedExternalNote.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetNote(independently deleted after deck restore) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.ListCardsByUser(ctx, owner.ID); err != nil || len(got) != 3 {
		t.Errorf("ListCardsByUser(after deck restore) = %d cards, %v; want only cascade-restored cards", len(got), err)
	}
	if _, err := queries.RestoreNoteByExternalID(ctx, gen.RestoreNoteByExternalIDParams{
		UpdatedAt: contentTime(14), DeckID: child.ID, OwnerID: other.ID,
		ExternalID: sql.NullString{String: "restore-external-1", Valid: true},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RestoreNoteByExternalID(other owner) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.RestoreNoteByTermKey(ctx, gen.RestoreNoteByTermKeyParams{
		UpdatedAt: contentTime(14), DeckID: child.ID, OwnerID: other.ID, TermKey: "deleted by term",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RestoreNoteByTermKey(other owner) error = %v, want sql.ErrNoRows", err)
	}

	for note, restore := range map[int64]func() (gen.Note, error){
		deletedExternalNote.ID: func() (gen.Note, error) {
			return queries.RestoreNoteByExternalID(ctx, gen.RestoreNoteByExternalIDParams{
				UpdatedAt: contentTime(12), DeckID: child.ID, OwnerID: owner.ID,
				ExternalID: sql.NullString{String: "restore-external-1", Valid: true},
			})
		},
		deletedTermNote.ID: func() (gen.Note, error) {
			return queries.RestoreNoteByTermKey(ctx, gen.RestoreNoteByTermKeyParams{
				UpdatedAt: contentTime(13), DeckID: child.ID, OwnerID: owner.ID, TermKey: "deleted by term",
			})
		},
	} {
		restored, err := restore()
		if err != nil || restored.ID != note || restored.DeletedAt.Valid || restored.DeletionBatch.Valid {
			t.Errorf("restore note %d = %+v, %v; want same active row", note, restored, err)
		}
	}
	if got, err := queries.GetNoteByExternalID(ctx, gen.GetNoteByExternalIDParams{
		DeckID: child.ID, OwnerID: owner.ID, ExternalID: sql.NullString{String: "restore-external-1", Valid: true},
	}); err != nil || got.ID != deletedExternalNote.ID {
		t.Errorf("GetNoteByExternalID(after restore) = %+v, %v; want original note", got, err)
	}
	if got, err := queries.GetNoteByTermKey(ctx, gen.GetNoteByTermKeyParams{
		DeckID: child.ID, OwnerID: owner.ID, TermKey: "deleted by term",
	}); err != nil || got.ID != deletedTermNote.ID {
		t.Errorf("GetNoteByTermKey(after restore) = %+v, %v; want original note", got, err)
	}
	for noteID, cardID := range cardIDByNote {
		assertContentCardProgress(t, queries, owner.ID, cardID, cards[cardID])
		if _, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: noteID, UserID: owner.ID, Direction: "forward"}); err != nil {
			t.Errorf("idempotent CreateCard(note %d) error = %v", noteID, err)
		}
		assertContentCardProgress(t, queries, owner.ID, cardID, cards[cardID])
	}
	if got, err := queries.ListCardsByUser(ctx, owner.ID); err != nil || len(got) != 5 {
		t.Errorf("ListCardsByUser(after note restores) = %d cards, %v; want five original cards", len(got), err)
	}
}

func TestContentDeletedImportKeysCanBeReusedWithoutDuplicateRestore(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	owner := createTestUser(t, queries, "identity-reuse-owner@example.com", false)

	oldDeck := createIdentifiedContentDeck(t, queries, owner.ID, "Old imported deck", sql.NullInt64{}, "school", "reused-deck-id")
	deletedAt := contentTime(5)
	if _, err := queries.SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
		DeletedAt: sql.NullTime{Time: deletedAt, Valid: true}, UpdatedAt: deletedAt, ID: oldDeck.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteDeck() error = %v", err)
	}
	newDeck := createIdentifiedContentDeck(t, queries, owner.ID, "Replacement imported deck", sql.NullInt64{}, "school", "reused-deck-id")
	if newDeck.ID == oldDeck.ID {
		t.Fatalf("replacement deck reused deleted row id %d instead of creating a separate active row", newDeck.ID)
	}
	if _, err := queries.RestoreDeckBySourceExternalID(ctx, gen.RestoreDeckBySourceExternalIDParams{
		UpdatedAt: contentTime(6), OwnerID: owner.ID,
		Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "reused-deck-id", Valid: true},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RestoreDeckBySourceExternalID(with active replacement) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.GetDeckBySourceExternalID(ctx, gen.GetDeckBySourceExternalIDParams{
		OwnerID: owner.ID, Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "reused-deck-id", Valid: true},
	}); err != nil || got.ID != newDeck.ID {
		t.Fatalf("GetDeckBySourceExternalID() = %+v, %v; want replacement %d", got, err, newDeck.ID)
	}

	oldNote := createContentNote(t, queries, owner.ID, newDeck.ID, "same term", "same term key", "reused-note-id")
	deletedAt = contentTime(7)
	if _, err := queries.SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: deletedAt, Valid: true}, UpdatedAt: deletedAt, ID: oldNote.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteNote() error = %v", err)
	}
	newNote := createContentNote(t, queries, owner.ID, newDeck.ID, "replacement term", "same term key", "reused-note-id")
	if newNote.ID == oldNote.ID {
		t.Fatalf("replacement note reused deleted row id %d instead of creating a separate active row", newNote.ID)
	}
	if _, err := queries.RestoreNoteByExternalID(ctx, gen.RestoreNoteByExternalIDParams{
		UpdatedAt: contentTime(8), DeckID: newDeck.ID, OwnerID: owner.ID,
		ExternalID: sql.NullString{String: "reused-note-id", Valid: true},
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RestoreNoteByExternalID(with active replacement) error = %v, want sql.ErrNoRows", err)
	}
	if got, err := queries.GetNoteByExternalID(ctx, gen.GetNoteByExternalIDParams{
		DeckID: newDeck.ID, OwnerID: owner.ID, ExternalID: sql.NullString{String: "reused-note-id", Valid: true},
	}); err != nil || got.ID != newNote.ID {
		t.Fatalf("GetNoteByExternalID() = %+v, %v; want replacement %d", got, err, newNote.ID)
	}
	if got, err := queries.GetNoteByTermKey(ctx, gen.GetNoteByTermKeyParams{
		DeckID: newDeck.ID, OwnerID: owner.ID, TermKey: "same term key",
	}); err != nil || got.ID != newNote.ID {
		t.Fatalf("GetNoteByTermKey() = %+v, %v; want replacement %d", got, err, newNote.ID)
	}
	var activeNotes int
	if err := database.SQL.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM notes WHERE deck_id = ? AND deleted_at IS NULL AND external_id = ? AND term_key = ?",
		newDeck.ID, "reused-note-id", "same term key",
	).Scan(&activeNotes); err != nil || activeNotes != 1 {
		t.Errorf("active matching note count = %d, %v; want exactly one", activeNotes, err)
	}
}

func TestContentDeckRestoreCascadeIsAtomicAndScopedToDeletionBatch(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	owner := createTestUser(t, queries, "restore-atomic-owner@example.com", false)

	root := createIdentifiedContentDeck(t, queries, owner.ID, "Original root", sql.NullInt64{}, "school", "atomic-root")
	child := createIdentifiedContentDeck(t, queries, owner.ID, "Original child", sql.NullInt64{Int64: root.ID, Valid: true}, "school", "atomic-child")
	note := createContentNote(t, queries, owner.ID, root.ID, "atomic note", "atomic note", "atomic-note")
	card, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "forward"})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	progress := setContentCardProgress(t, database, card.ID, 3)
	deletedAt := contentTime(4)
	if _, err := queries.SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
		DeletedAt: sql.NullTime{Time: deletedAt, Valid: true}, UpdatedAt: deletedAt, ID: root.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteDeck() error = %v", err)
	}

	conflictingRoot := createContentDeck(t, queries, owner.ID, "Independent root", sql.NullInt64{})
	conflictingChild := createIdentifiedContentDeck(t, queries, owner.ID, "Independent matching child", sql.NullInt64{Int64: conflictingRoot.ID, Valid: true}, "school", "atomic-child")
	if _, err := queries.RestoreDeckBySourceExternalID(ctx, gen.RestoreDeckBySourceExternalIDParams{
		UpdatedAt: contentTime(5), OwnerID: owner.ID,
		Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "atomic-root", Valid: true},
	}); err == nil {
		t.Fatal("RestoreDeckBySourceExternalID(with conflicting descendant) error = nil, want unique constraint failure")
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: root.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeck(root after failed restore) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: child.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeck(child after failed restore) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetNote(ctx, gen.GetNoteParams{ID: note.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetNote(after failed restore) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetCard(ctx, gen.GetCardParams{ID: card.ID, UserID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetCard(after failed restore) error = %v, want sql.ErrNoRows", err)
	}
	for _, item := range []struct {
		table string
		id    int64
	}{
		{table: "decks", id: root.ID},
		{table: "decks", id: child.ID},
		{table: "notes", id: note.ID},
		{table: "cards", id: card.ID},
	} {
		var deletedAt sql.NullTime
		var deletionBatch sql.NullString
		query := "SELECT deleted_at, deletion_batch FROM " + item.table + " WHERE id = ?"
		if err := database.SQL.QueryRowContext(ctx, query, item.id).Scan(&deletedAt, &deletionBatch); err != nil {
			t.Fatalf("read %s %d after failed restore: %v", item.table, item.id, err)
		}
		if !deletedAt.Valid || !deletionBatch.Valid {
			t.Errorf("%s %d partially restored after failed cascade: deleted_at=%+v deletion_batch=%+v", item.table, item.id, deletedAt, deletionBatch)
		}
	}

	if _, err := queries.SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
		DeletedAt: sql.NullTime{Time: contentTime(6), Valid: true}, UpdatedAt: contentTime(6),
		ID: conflictingRoot.ID, OwnerID: owner.ID,
	}); err != nil {
		t.Fatalf("SoftDeleteDeck(conflicting independent subtree) error = %v", err)
	}
	restored, err := queries.RestoreDeckBySourceExternalID(ctx, gen.RestoreDeckBySourceExternalIDParams{
		UpdatedAt: contentTime(7), OwnerID: owner.ID,
		Source: sql.NullString{String: "school", Valid: true}, ExternalID: sql.NullString{String: "atomic-root", Valid: true},
	})
	if err != nil || restored.ID != root.ID {
		t.Fatalf("retry RestoreDeckBySourceExternalID() = %+v, %v; want original root", restored, err)
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: conflictingRoot.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeck(independent root after restore) error = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: conflictingChild.ID, OwnerID: owner.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDeck(independent matching child after restore) error = %v, want sql.ErrNoRows", err)
	}
	assertContentCardProgress(t, queries, owner.ID, card.ID, progress)
}

func TestContentSchemaDefaultsConstraintsAndForeignKeys(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	owner := createTestUser(t, queries, "schema-owner@example.com", false)
	other := createTestUser(t, queries, "schema-other@example.com", false)

	result, err := database.SQL.ExecContext(ctx, "INSERT INTO decks (owner_id, name) VALUES (?, ?)", owner.ID, "Defaults")
	if err != nil {
		t.Fatalf("insert default deck: %v", err)
	}
	defaultDeckID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read default deck ID: %v", err)
	}
	defaultDeck, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: defaultDeckID, OwnerID: owner.ID})
	if err != nil {
		t.Fatalf("GetDeck(defaults) error = %v", err)
	}
	if defaultDeck.Position != 0 || defaultDeck.ReverseCards || defaultDeck.WrongArticleRating != 2 ||
		defaultDeck.ParentID.Valid || defaultDeck.LangTarget.Valid || defaultDeck.LangNative.Valid ||
		defaultDeck.Source.Valid || defaultDeck.ExternalID.Valid || defaultDeck.SourceDeckID.Valid || defaultDeck.DeletionBatch.Valid ||
		defaultDeck.DeletedAt.Valid || defaultDeck.CreatedAt.IsZero() || defaultDeck.UpdatedAt.IsZero() ||
		defaultDeck.CreatedAt.Location() != time.UTC || defaultDeck.UpdatedAt.Location() != time.UTC {
		t.Errorf("deck defaults = %+v", defaultDeck)
	}

	sourceDeck := createContentDeck(t, queries, owner.ID, "Imported", sql.NullInt64{})
	sourceDeckParams := gen.UpdateDeckParams{
		Name:               sourceDeck.Name,
		Position:           sourceDeck.Position,
		WrongArticleRating: 2,
		Source:             sql.NullString{String: "school-wordsets", Valid: true},
		ExternalID:         sql.NullString{String: "set-1", Valid: true},
		SourceDeckID:       sql.NullInt64{Int64: 987654, Valid: true},
		UpdatedAt:          contentTime(1),
		ID:                 sourceDeck.ID,
		OwnerID:            owner.ID,
	}
	if got, err := queries.UpdateDeck(ctx, sourceDeckParams); err != nil || got.SourceDeckID.Int64 != 987654 {
		t.Fatalf("UpdateDeck(source scalar) = %+v, %v; source_deck_id intentionally has no FK", got, err)
	}
	if _, err := queries.CreateDeck(ctx, gen.CreateDeckParams{
		OwnerID: owner.ID, Name: "Duplicate imported", WrongArticleRating: 2,
		Source:     sql.NullString{String: "school-wordsets", Valid: true},
		ExternalID: sql.NullString{String: "set-1", Valid: true},
	}); err == nil {
		t.Error("CreateDeck(duplicate owner/source/external_id) error = nil, want unique constraint error")
	}
	if _, err := queries.CreateDeck(ctx, gen.CreateDeckParams{
		OwnerID: other.ID, Name: "Other owner's imported", WrongArticleRating: 2,
		Source:     sql.NullString{String: "school-wordsets", Valid: true},
		ExternalID: sql.NullString{String: "set-1", Valid: true},
	}); err != nil {
		t.Errorf("same source identity for another owner error = %v, want nil", err)
	}
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO decks (owner_id, name) VALUES (?, ?)", owner.ID+1000, "Orphan"); err == nil {
		t.Error("insert deck with missing owner error = nil, want foreign-key constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx, "INSERT INTO decks (owner_id, name, lang_target) VALUES (?, ?, ?)", owner.ID, "Invalid language", "pl"); err == nil {
		t.Error("insert deck with invalid target language error = nil, want check constraint error")
	}

	result, err = database.SQL.ExecContext(ctx,
		"INSERT INTO notes (deck_id, term, term_key, translation) VALUES (?, ?, ?, ?)",
		defaultDeck.ID, "Hallo", "hallo", "cześć",
	)
	if err != nil {
		t.Fatalf("insert note with JSON defaults: %v", err)
	}
	defaultNoteID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read default note ID: %v", err)
	}
	defaultNote, err := queries.GetNote(ctx, gen.GetNoteParams{ID: defaultNoteID, OwnerID: owner.ID})
	if err != nil || defaultNote.Tags != "[]" || defaultNote.Alternatives != "[]" || defaultNote.Fields != "{}" ||
		defaultNote.Transcription.Valid || defaultNote.ExternalID.Valid || defaultNote.ImportBatchID.Valid || defaultNote.DeletionBatch.Valid {
		t.Fatalf("note defaults = %+v, %v", defaultNote, err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO notes (deck_id, term, term_key, translation) VALUES (?, ?, ?, ?)",
		defaultDeck.ID, "Another term", "hallo", "other",
	); err == nil {
		t.Error("duplicate (deck_id, term_key) error = nil, want unique constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO notes (deck_id, term, term_key, translation, external_id) VALUES (?, ?, ?, ?, ?)",
		defaultDeck.ID, "Hallo 2", "hallo 2", "other", "term-1",
	); err != nil {
		t.Fatalf("insert note with external ID: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO notes (deck_id, term, term_key, translation, external_id) VALUES (?, ?, ?, ?, ?)",
		defaultDeck.ID, "Hallo 3", "hallo 3", "other", "term-1",
	); err == nil {
		t.Error("duplicate (deck_id, external_id) error = nil, want unique constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO notes (deck_id, term, term_key, translation, tags) VALUES (?, ?, ?, ?, ?)",
		defaultDeck.ID, "Bad JSON", "bad json", "other", "not-json",
	); err == nil {
		t.Error("insert note with invalid JSON tags error = nil, want check constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO notes (deck_id, term, term_key, translation) VALUES (?, ?, ?, ?)",
		defaultDeck.ID+1000, "Orphan", "orphan", "other",
	); err == nil {
		t.Error("insert note with missing deck error = nil, want foreign-key constraint error")
	}
	if _, err := queries.CreateNote(ctx, gen.CreateNoteParams{
		Term: "Missing batch", TermKey: "missing batch", Translation: "other", Tags: "[]", Alternatives: "[]", Fields: "{}",
		ImportBatchID: sql.NullString{String: "missing-batch", Valid: true}, DeckID: defaultDeck.ID, UserID: owner.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("CreateNote(missing batch) error = %v, want sql.ErrNoRows", err)
	}

	card, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: defaultNote.ID, UserID: owner.ID, Direction: "forward"})
	if err != nil || card.State != 0 || card.ElapsedDays != 0 || card.ScheduledDays != 0 || card.Step != 0 ||
		card.Reps != 0 || card.Lapses != 0 || card.Suspended || card.IsLeech || card.DeletedAt.Valid || card.DeletionBatch.Valid || card.Due.IsZero() || card.UpdatedAt.IsZero() {
		t.Fatalf("initial card defaults = %+v, %v", card, err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO cards (note_id, user_id, direction) VALUES (?, ?, ?)", defaultNote.ID, owner.ID, "forward"); err == nil {
		t.Error("duplicate card (note_id,user_id,direction) error = nil, want unique constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO cards (note_id, user_id, direction) VALUES (?, ?, ?)", defaultNote.ID, owner.ID, "sideways"); err == nil {
		t.Error("insert card with invalid direction error = nil, want check constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO cards (note_id, user_id, direction) VALUES (?, ?, ?)", defaultNote.ID+1000, owner.ID, "forward"); err == nil {
		t.Error("insert card with missing note error = nil, want foreign-key constraint error")
	}
	if _, err := database.SQL.ExecContext(ctx,
		"INSERT INTO cards (note_id, user_id, direction) VALUES (?, ?, ?)", defaultNote.ID, other.ID+1000, "reverse"); err == nil {
		t.Error("insert card with missing user error = nil, want foreign-key constraint error")
	}

	if _, err := queries.CreateImportBatch(ctx, gen.CreateImportBatchParams{
		ID: "bad-sha", UserID: owner.ID, Adapter: "retain", Raw: []byte("raw"), RawSha256: []byte{1},
	}); err == nil {
		t.Error("CreateImportBatch(short SHA-256) error = nil, want check constraint error")
	}
	if _, err := queries.CreateImportBatch(ctx, gen.CreateImportBatchParams{
		ID: "missing-user", UserID: owner.ID + 1000, Adapter: "retain", Raw: []byte("raw"), RawSha256: make([]byte, 32),
	}); err == nil {
		t.Error("CreateImportBatch(missing user) error = nil, want foreign-key constraint error")
	}
	validBatch, err := queries.CreateImportBatch(ctx, gen.CreateImportBatchParams{
		ID: "constraint-check", UserID: owner.ID, Adapter: "retain", Raw: []byte("raw"), RawSha256: make([]byte, 32),
	})
	if err != nil {
		t.Fatalf("CreateImportBatch(constraint check) error = %v", err)
	}
	invalidBatchUpdate := gen.UpdateImportBatchParams{
		Adapter: validBatch.Adapter, Filename: validBatch.Filename, Raw: validBatch.Raw, RawSha256: validBatch.RawSha256,
		Status: validBatch.Status, Summary: validBatch.Summary, ID: validBatch.ID, UserID: owner.ID,
	}
	invalidBatchUpdate.Status = "unknown"
	if _, err := queries.UpdateImportBatch(ctx, invalidBatchUpdate); err == nil {
		t.Error("UpdateImportBatch(invalid status) error = nil, want check constraint error")
	}
	invalidBatchUpdate.Status = validBatch.Status
	invalidBatchUpdate.Summary = "not-json"
	if _, err := queries.UpdateImportBatch(ctx, invalidBatchUpdate); err == nil {
		t.Error("UpdateImportBatch(invalid summary JSON) error = nil, want check constraint error")
	}
}
