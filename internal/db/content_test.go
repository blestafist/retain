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

func TestContentQueriesCRUDAndOwnerScope(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	queries := database.Queries()
	owner := createTestUser(t, queries, "content-owner@example.com", false)
	other := createTestUser(t, queries, "content-other@example.com", false)

	otherRoot := createContentDeck(t, queries, other.ID, "Other root", sql.NullInt64{})
	root := createContentDeck(t, queries, owner.ID, "German", sql.NullInt64{})
	child := createContentDeck(t, queries, owner.ID, "Chapter 1", sql.NullInt64{Int64: root.ID, Valid: true})
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
	if got, err := queries.ListDecksByOwner(ctx, owner.ID); err != nil || len(got) != 2 {
		t.Fatalf("ListDecksByOwner() = %d decks, %v; want 2, nil", len(got), err)
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
	if card.State != 0 || card.Stability.Valid || card.Difficulty.Valid || card.ElapsedDays != 0 || card.Reps != 0 || card.Suspended || card.IsLeech {
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
	if got, err := queries.ListDecksByOwner(ctx, owner.ID); err != nil || len(got) != 1 || got[0].ID != child.ID {
		t.Errorf("ListDecksByOwner(after soft delete) = %+v, %v; want remaining child only", got, err)
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
		defaultDeck.Source.Valid || defaultDeck.ExternalID.Valid || defaultDeck.SourceDeckID.Valid ||
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
		defaultNote.Transcription.Valid || defaultNote.ExternalID.Valid || defaultNote.ImportBatchID.Valid {
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
		card.Reps != 0 || card.Lapses != 0 || card.Suspended || card.IsLeech || card.Due.IsZero() || card.UpdatedAt.IsZero() {
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
