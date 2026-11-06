package db

import (
	"context"
	"database/sql"
	"io/fs"
	"maps"
	"path/filepath"
	"testing"

	"github.com/pestit/retain/internal/db/gen"
	"github.com/pressly/goose/v3"
)

func TestDeckParentDeleteCascadeMigrationPreservesExistingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retain.db")
	dsn, _, err := dataSourceName(path)
	if err != nil {
		t.Fatalf("dataSourceName() error = %v", err)
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	isOpen := true
	t.Cleanup(func() {
		if isOpen {
			if err := sqlDB.Close(); err != nil {
				t.Errorf("close version-3 database: %v", err)
			}
		}
	})
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("ping version-3 database: %v", err)
	}

	migrations := newEmbeddedMigrationProvider(t, sqlDB)
	if _, err := migrations.UpTo(ctx, 3); err != nil {
		t.Fatalf("migrate persistent database through version 3: %v", err)
	}
	queries := gen.New(sqlDB)
	owner := createTestUser(t, queries, "cascade-migration@example.com", false)
	root := createContentDeck(t, queries, owner.ID, "Root", sql.NullInt64{})
	child := createContentDeck(t, queries, owner.ID, "Child", sql.NullInt64{Int64: root.ID, Valid: true})
	grandchild := createContentDeck(t, queries, owner.ID, "Grandchild", sql.NullInt64{Int64: child.ID, Valid: true})
	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE decks
		SET description = ?, position = ?, lang_target = ?, lang_native = ?, reverse_cards = ?,
		    wrong_article_rating = ?, color_seed = ?, source = ?, external_id = ?, expected_count = ?,
		    source_deck_id = ?, updated_at = ?
		WHERE id = ?`,
		"Preserve all deck fields", 7, "de", "pl", true, 3, "#123456", "school-wordsets", "root-1", 12, 987654,
		contentTime(6), root.ID,
	); err != nil {
		t.Fatalf("set root deck fields: %v", err)
	}
	note := createContentNote(t, queries, owner.ID, grandchild.ID, "vorhandenes Wort", "vorhandenes wort", "word-1")
	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE notes
		SET transcription = ?, example = ?, example_translation = ?, notes = ?, tags = ?,
		    alternatives = ?, fields = ?, updated_at = ?
		WHERE id = ?`,
		"fɔɐ̯ˈhandənəs", "Ein Beispiel.", "Przykład.", "Keep this note", `["topic:test"]`,
		`["existing word"]`, `{"kind":"word","pos":"noun"}`, contentTime(7), note.ID,
	); err != nil {
		t.Fatalf("set note fields: %v", err)
	}
	card, err := queries.CreateCard(ctx, gen.CreateCardParams{NoteID: note.ID, UserID: owner.ID, Direction: "forward"})
	if err != nil {
		t.Fatalf("CreateCard() error = %v", err)
	}
	setContentCardProgress(t, &DB{SQL: sqlDB}, card.ID, 4)

	wantDecks := make([]gen.Deck, 0, 3)
	for _, deck := range []gen.Deck{root, child, grandchild} {
		got, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: deck.ID, OwnerID: owner.ID})
		if err != nil {
			t.Fatalf("GetDeck(%d) before migration: %v", deck.ID, err)
		}
		wantDecks = append(wantDecks, got)
	}
	wantNote, err := queries.GetNote(ctx, gen.GetNoteParams{ID: note.ID, OwnerID: owner.ID})
	if err != nil {
		t.Fatalf("GetNote() before migration: %v", err)
	}
	wantCard, err := queries.GetCard(ctx, gen.GetCardParams{ID: card.ID, UserID: owner.ID})
	if err != nil {
		t.Fatalf("GetCard() before migration: %v", err)
	}
	wantSchemaObjects := readMigrationSchemaObjects(t, ctx, sqlDB)
	if got := deckParentDeleteAction(t, ctx, sqlDB); got != "RESTRICT" {
		t.Fatalf("version-3 decks.parent_id ON DELETE = %q, want RESTRICT", got)
	}

	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close version-3 database: %v", err)
	}
	isOpen = false

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen persistent database and apply migration 4: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close migrated database: %v", err)
		}
	})
	queries = database.Queries()
	assertMigrationVersion(t, ctx, database.SQL, 4)
	assertPreservedMigrationContent(t, ctx, queries, owner.ID, wantDecks, wantNote, wantCard)
	if got := deckParentDeleteAction(t, ctx, database.SQL); got != "CASCADE" {
		t.Fatalf("version-4 decks.parent_id ON DELETE = %q, want CASCADE", got)
	}
	assertForeignKeysEnabled(t, ctx, database.SQL)
	assertMigrationSchemaObjects(t, ctx, database.SQL, wantSchemaObjects)
	assertForeignKeyCheckClean(t, ctx, database.SQL)

	// Exercise the down rebuild too, then reapply version 4 before testing hard deletion.
	migrations = newEmbeddedMigrationProvider(t, database.SQL)
	if _, err := migrations.DownTo(ctx, 3); err != nil {
		t.Fatalf("roll back migration 4: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 3)
	if got := deckParentDeleteAction(t, ctx, database.SQL); got != "RESTRICT" {
		t.Fatalf("down migration decks.parent_id ON DELETE = %q, want RESTRICT", got)
	}
	assertPreservedMigrationContent(t, ctx, queries, owner.ID, wantDecks, wantNote, wantCard)
	assertMigrationSchemaObjects(t, ctx, database.SQL, wantSchemaObjects)
	assertForeignKeysEnabled(t, ctx, database.SQL)
	if _, err := migrations.Up(ctx); err != nil {
		t.Fatalf("reapply migration 4: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 4)
	if got := deckParentDeleteAction(t, ctx, database.SQL); got != "CASCADE" {
		t.Fatalf("reapplied migration decks.parent_id ON DELETE = %q, want CASCADE", got)
	}
	assertPreservedMigrationContent(t, ctx, queries, owner.ID, wantDecks, wantNote, wantCard)
	assertMigrationSchemaObjects(t, ctx, database.SQL, wantSchemaObjects)
	assertForeignKeysEnabled(t, ctx, database.SQL)

	// The rebuilt deck triggers still soft-delete and restore the complete subtree, without
	// changing a card's FSRS state. The note triggers continue to do the same for individual notes.
	deckDeletedAt := contentTime(8)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE decks SET deleted_at = ?, deletion_batch = ?, updated_at = ? WHERE id = ?",
		deckDeletedAt, "migration-delete-batch", deckDeletedAt, root.ID,
	); err != nil {
		t.Fatalf("soft-delete root through preserved deck trigger: %v", err)
	}
	for _, id := range []int64{root.ID, child.ID, grandchild.ID} {
		assertMigrationDeletedState(t, ctx, database.SQL, "decks", id, true)
	}
	assertMigrationDeletedState(t, ctx, database.SQL, "notes", note.ID, true)
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", card.ID, true)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE decks SET deleted_at = NULL, deletion_batch = NULL, updated_at = ? WHERE id = ?",
		contentTime(9), root.ID,
	); err != nil {
		t.Fatalf("restore root through preserved deck trigger: %v", err)
	}
	for _, id := range []int64{root.ID, child.ID, grandchild.ID} {
		assertMigrationDeletedState(t, ctx, database.SQL, "decks", id, false)
	}
	assertMigrationDeletedState(t, ctx, database.SQL, "notes", note.ID, false)
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", card.ID, false)
	assertContentCardProgress(t, queries, owner.ID, card.ID, contentCardProgress{
		state: wantCard.State, due: wantCard.Due, stability: wantCard.Stability.Float64,
		difficulty: wantCard.Difficulty.Float64, elapsedDays: wantCard.ElapsedDays,
		scheduledDays: wantCard.ScheduledDays, step: wantCard.Step, reps: wantCard.Reps,
		lapses: wantCard.Lapses, lastReview: wantCard.LastReview.Time,
		suspended: wantCard.Suspended, isLeech: wantCard.IsLeech,
	})

	noteDeletedAt := contentTime(10)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE notes SET deleted_at = ?, deletion_batch = ?, updated_at = ? WHERE id = ?",
		noteDeletedAt, "migration-note-delete-batch", noteDeletedAt, note.ID,
	); err != nil {
		t.Fatalf("soft-delete note through preserved note trigger: %v", err)
	}
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", card.ID, true)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE notes SET deleted_at = NULL, deletion_batch = NULL, updated_at = ? WHERE id = ?",
		contentTime(11), note.ID,
	); err != nil {
		t.Fatalf("restore note through preserved note trigger: %v", err)
	}
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", card.ID, false)
	assertContentCardProgress(t, queries, owner.ID, card.ID, contentCardProgress{
		state: wantCard.State, due: wantCard.Due, stability: wantCard.Stability.Float64,
		difficulty: wantCard.Difficulty.Float64, elapsedDays: wantCard.ElapsedDays,
		scheduledDays: wantCard.ScheduledDays, step: wantCard.Step, reps: wantCard.Reps,
		lapses: wantCard.Lapses, lastReview: wantCard.LastReview.Time,
		suspended: wantCard.Suspended, isLeech: wantCard.IsLeech,
	})

	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM users WHERE id = ?", owner.ID); err != nil {
		t.Fatalf("delete owner with nested decks, note, and card: %v", err)
	}
	for _, table := range []string{"decks", "notes", "cards"} {
		var count int
		if err := database.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s after owner deletion: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s rows after owner deletion = %d, want 0", table, count)
		}
	}
	assertForeignKeyCheckClean(t, ctx, database.SQL)
}

func newEmbeddedMigrationProvider(t *testing.T, database *sql.DB) *goose.Provider {
	t.Helper()
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	return provider
}

func assertMigrationVersion(t *testing.T, ctx context.Context, database *sql.DB, want int64) {
	t.Helper()
	var got int64
	if err := database.QueryRowContext(ctx,
		"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1",
	).Scan(&got); err != nil {
		t.Fatalf("read goose migration version: %v", err)
	}
	if got != want {
		t.Fatalf("goose migration version = %d, want %d", got, want)
	}
}

func assertPreservedMigrationContent(
	t *testing.T,
	ctx context.Context,
	queries *gen.Queries,
	ownerID int64,
	wantDecks []gen.Deck,
	wantNote gen.Note,
	wantCard gen.Card,
) {
	t.Helper()
	for _, want := range wantDecks {
		got, err := queries.GetDeck(ctx, gen.GetDeckParams{ID: want.ID, OwnerID: ownerID})
		if err != nil || got != want {
			t.Errorf("GetDeck(%d) after migration = %+v, %v; want %+v", want.ID, got, err, want)
		}
	}
	gotNote, err := queries.GetNote(ctx, gen.GetNoteParams{ID: wantNote.ID, OwnerID: ownerID})
	if err != nil || gotNote != wantNote {
		t.Errorf("GetNote(%d) after migration = %+v, %v; want %+v", wantNote.ID, gotNote, err, wantNote)
	}
	gotCard, err := queries.GetCard(ctx, gen.GetCardParams{ID: wantCard.ID, UserID: ownerID})
	if err != nil || gotCard != wantCard {
		t.Errorf("GetCard(%d) after migration = %+v, %v; want %+v", wantCard.ID, gotCard, err, wantCard)
	}
}

func readMigrationSchemaObjects(t *testing.T, ctx context.Context, database *sql.DB) map[string]string {
	t.Helper()
	rows, err := database.QueryContext(ctx, `
		SELECT type, name, sql
		FROM sqlite_schema
		WHERE type IN ('index', 'trigger') AND sql IS NOT NULL
		ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read indexes and triggers: %v", err)
	}
	defer rows.Close()
	objects := make(map[string]string)
	for rows.Next() {
		var kind, name, definition string
		if err := rows.Scan(&kind, &name, &definition); err != nil {
			t.Fatalf("scan schema object: %v", err)
		}
		objects[kind+"."+name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema objects: %v", err)
	}
	return objects
}

func assertMigrationSchemaObjects(t *testing.T, ctx context.Context, database *sql.DB, want map[string]string) {
	t.Helper()
	got := readMigrationSchemaObjects(t, ctx, database)
	if !maps.Equal(got, want) {
		t.Errorf("schema indexes/triggers changed during migration:\n got: %#v\nwant: %#v", got, want)
	}
}

func deckParentDeleteAction(t *testing.T, ctx context.Context, database *sql.DB) string {
	t.Helper()
	rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_list(decks)")
	if err != nil {
		t.Fatalf("read decks foreign keys: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var table, column, referencedColumn, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &table, &column, &referencedColumn, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan decks foreign key: %v", err)
		}
		if column == "parent_id" {
			return onDelete
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate decks foreign keys: %v", err)
	}
	t.Fatal("decks.parent_id foreign key not found")
	return ""
}

func assertForeignKeysEnabled(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var enabled int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", enabled)
	}
}

func assertMigrationDeletedState(t *testing.T, ctx context.Context, database *sql.DB, table string, id int64, wantDeleted bool) {
	t.Helper()
	var deletedAt sql.NullTime
	var deletionBatch sql.NullString
	query := "SELECT deleted_at, deletion_batch FROM " + table + " WHERE id = ?"
	if err := database.QueryRowContext(ctx, query, id).Scan(&deletedAt, &deletionBatch); err != nil {
		t.Fatalf("read %s %d deletion state: %v", table, id, err)
	}
	if deletedAt.Valid != wantDeleted || deletionBatch.Valid != wantDeleted {
		t.Errorf("%s %d deletion state = deleted_at %+v, deletion_batch %+v; want deleted=%t", table, id, deletedAt, deletionBatch, wantDeleted)
	}
}

func assertForeignKeyCheckClean(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			t.Fatalf("scan foreign key violation: %v", err)
		}
		t.Errorf("foreign key violation: table=%s row=%+v parent=%s fk=%d", table, rowID, parent, foreignKeyID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
}
