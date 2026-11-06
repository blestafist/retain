package db

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pestit/retain/internal/db/gen"
	"github.com/pressly/goose/v3"
)

func TestOriginalContentMigrationPreservesExistingData(t *testing.T) {
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
		t.Fatalf("migrate original database through version 3: %v", err)
	}
	owner := createTestUser(t, gen.New(sqlDB), "compat-migration@example.com", false)
	ids := seedOriginalContentRows(t, ctx, sqlDB, owner.ID)
	wantRows := readOriginalContentRows(t, ctx, sqlDB)
	wantVersion3Objects := readMigrationSchemaObjects(t, ctx, sqlDB)
	if got := migrationDeckParentDeleteAction(t, ctx, sqlDB); got != "RESTRICT" {
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
	assertMigrationVersion(t, ctx, database.SQL, 4)
	assertOriginalContentRowsEqual(t, ctx, database.SQL, wantRows)
	assertMigrationDeletionBatch(t, ctx, database.SQL, "decks", ids.archivedDeck,
		fmt.Sprintf("migration-00004-deck-%d", ids.archivedDeck))
	assertMigrationDeletionBatch(t, ctx, database.SQL, "notes", ids.archivedNote,
		fmt.Sprintf("migration-00004-note-%d", ids.archivedNote))
	if got := migrationDeckParentDeleteAction(t, ctx, database.SQL); got != "CASCADE" {
		t.Fatalf("version-4 decks.parent_id ON DELETE = %q, want CASCADE", got)
	}
	assertLatestMigrationSchema(t, ctx, database.SQL)
	assertForeignKeysEnabled(t, ctx, database.SQL)
	assertForeignKeyCheckClean(t, ctx, database.SQL)
	assertLatestMigrationColumns(t, ctx, database.SQL)
	for _, foreignKey := range []struct {
		table, column, parent, action string
	}{
		{"decks", "owner_id", "users", "CASCADE"},
		{"decks", "parent_id", "decks", "CASCADE"},
		{"notes", "deck_id", "decks", "CASCADE"},
		{"notes", "import_batch_id", "import_batches", "SET NULL"},
		{"cards", "note_id", "notes", "CASCADE"},
		{"cards", "user_id", "users", "CASCADE"},
	} {
		assertMigrationForeignKey(t, ctx, database.SQL, foreignKey.table, foreignKey.column, foreignKey.parent, foreignKey.action)
	}
	assertPartialUniqueIndex(t, ctx, database.SQL, "decks", "decks_owner_source_external_active_uq")
	assertPartialUniqueIndex(t, ctx, database.SQL, "notes", "notes_deck_term_key_active_uq")
	assertPartialUniqueIndex(t, ctx, database.SQL, "notes", "notes_deck_external_active_uq")

	// The down rebuild is safe for data expressible by the original schema, and restores its exact
	// explicit indexes. Reapplying v4 must preserve that data again.
	migrations = newEmbeddedMigrationProvider(t, database.SQL)
	if _, err := migrations.DownTo(ctx, 3); err != nil {
		t.Fatalf("roll back migration 4: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 3)
	if got := migrationDeckParentDeleteAction(t, ctx, database.SQL); got != "RESTRICT" {
		t.Fatalf("down migration decks.parent_id ON DELETE = %q, want RESTRICT", got)
	}
	assertOriginalContentRowsEqual(t, ctx, database.SQL, wantRows)
	assertMigrationSchemaObjects(t, ctx, database.SQL, wantVersion3Objects)
	assertForeignKeysEnabled(t, ctx, database.SQL)
	if _, err := migrations.Up(ctx); err != nil {
		t.Fatalf("reapply migration 4: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 4)
	assertOriginalContentRowsEqual(t, ctx, database.SQL, wantRows)
	assertMigrationDeletionBatch(t, ctx, database.SQL, "decks", ids.archivedDeck,
		fmt.Sprintf("migration-00004-deck-%d", ids.archivedDeck))
	assertMigrationDeletionBatch(t, ctx, database.SQL, "notes", ids.archivedNote,
		fmt.Sprintf("migration-00004-note-%d", ids.archivedNote))
	assertLatestMigrationSchema(t, ctx, database.SQL)
	assertForeignKeysEnabled(t, ctx, database.SQL)
	assertForeignKeyCheckClean(t, ctx, database.SQL)
	// A down migration must not erase card tombstones that the original schema could not represent.
	rollbackDeletedAt := contentTime(6)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE cards SET deleted_at = ?, deletion_batch = ? WHERE id = ?",
		rollbackDeletedAt, "rollback-must-preserve", ids.cardIDs[0],
	); err != nil {
		t.Fatalf("set card tombstone for down-safety check: %v", err)
	}
	migrations = newEmbeddedMigrationProvider(t, database.SQL)
	if _, err := migrations.DownTo(ctx, 3); err == nil {
		t.Fatal("down migration with a card tombstone succeeded, want safe refusal")
	}
	if err := cleanupFailedMigration(ctx, database.SQL); err != nil {
		t.Fatalf("clean up refused down migration: %v", err)
	}
	assertMigrationVersion(t, ctx, database.SQL, 4)
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", ids.cardIDs[0], true)
	assertForeignKeysEnabled(t, ctx, database.SQL)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE cards SET deleted_at = NULL, deletion_batch = NULL WHERE id = ?", ids.cardIDs[0]); err != nil {
		t.Fatalf("restore card after down-safety check: %v", err)
	}
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", ids.cardIDs[0], false)

	// Active identity constraints reject duplicates, while the partial indexes allow replacing a
	// soft-deleted imported row. Deck and note cascade/restore triggers preserve FSRS state.
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO decks (owner_id, name, source, external_id)
		VALUES (?, 'Duplicate active root', 'school-wordsets', 'root-1')`, owner.ID); err == nil {
		t.Fatal("insert duplicate active deck identity succeeded, want unique constraint error")
	}
	for _, item := range []struct {
		table string
		id    int64
	}{
		{"decks", ids.rootDeck},
		{"notes", ids.noteIDs[0]},
		{"cards", ids.cardIDs[0]},
	} {
		if _, err := database.SQL.ExecContext(ctx,
			"UPDATE "+item.table+" SET deletion_batch = 'invalid-without-deleted-at' WHERE id = ?", item.id); err == nil {
			t.Errorf("%s deletion marker check accepted an orphan deletion_batch", item.table)
		}
	}
	fsrsBefore := readMigrationRows(t, ctx, database.SQL, "cards",
		"id,state,due,stability,difficulty,elapsed_days,scheduled_days,step,reps,lapses,last_review,suspended,is_leech")
	deckDeletedAt := contentTime(8)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE decks SET deleted_at = ?, deletion_batch = ?, updated_at = ? WHERE id = ?",
		deckDeletedAt, "migration-delete-batch", deckDeletedAt, ids.rootDeck,
	); err != nil {
		t.Fatalf("soft-delete root through migrated trigger: %v", err)
	}
	for _, id := range ids.deckIDs {
		assertMigrationDeletedState(t, ctx, database.SQL, "decks", id, true)
	}
	for _, id := range ids.noteIDs {
		assertMigrationDeletedState(t, ctx, database.SQL, "notes", id, true)
	}
	for _, id := range ids.cardIDs {
		assertMigrationDeletedState(t, ctx, database.SQL, "cards", id, true)
	}
	replacementDeck, err := database.SQL.ExecContext(ctx, `
		INSERT INTO decks (owner_id, name, source, external_id)
		VALUES (?, 'Replacement root', 'school-wordsets', 'root-1')`, owner.ID)
	if err != nil {
		t.Fatalf("reuse deleted deck identity: %v", err)
	}
	replacementDeckID, err := replacementDeck.LastInsertId()
	if err != nil {
		t.Fatalf("read replacement deck ID: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM decks WHERE id = ?", replacementDeckID); err != nil {
		t.Fatalf("remove replacement deck: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE decks SET deleted_at = NULL, deletion_batch = NULL, updated_at = ? WHERE id = ?",
		contentTime(9), ids.rootDeck,
	); err != nil {
		t.Fatalf("restore root through migrated trigger: %v", err)
	}
	for _, id := range ids.deckIDs {
		assertMigrationDeletedState(t, ctx, database.SQL, "decks", id, false)
	}
	for _, id := range ids.noteIDs {
		assertMigrationDeletedState(t, ctx, database.SQL, "notes", id, false)
	}
	for _, id := range ids.cardIDs {
		assertMigrationDeletedState(t, ctx, database.SQL, "cards", id, false)
	}
	assertMigrationRowsEqual(t, "FSRS state after deck restore", fsrsBefore,
		readMigrationRows(t, ctx, database.SQL, "cards",
			"id,state,due,stability,difficulty,elapsed_days,scheduled_days,step,reps,lapses,last_review,suspended,is_leech"))

	noteDeletedAt := contentTime(10)
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE notes SET deleted_at = ?, deletion_batch = ?, updated_at = ? WHERE id = ?",
		noteDeletedAt, "migration-note-delete-batch", noteDeletedAt, ids.noteIDs[0],
	); err != nil {
		t.Fatalf("soft-delete note through migrated trigger: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO notes (deck_id, term, term_key, translation, external_id)
		VALUES (?, 'Replacement root word', 'root word', 'replacement', 'root-word-1')`, ids.rootDeck); err != nil {
		t.Fatalf("reuse deleted note identities: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"DELETE FROM notes WHERE deck_id = ? AND term_key = ? AND deleted_at IS NULL", ids.rootDeck, "root word"); err != nil {
		t.Fatalf("remove replacement note: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		"UPDATE notes SET deleted_at = NULL, deletion_batch = NULL, updated_at = ? WHERE id = ?",
		contentTime(11), ids.noteIDs[0],
	); err != nil {
		t.Fatalf("restore note through migrated trigger: %v", err)
	}
	assertMigrationDeletedState(t, ctx, database.SQL, "cards", ids.cardIDs[0], false)
	assertMigrationRowsEqual(t, "FSRS state after note restore", fsrsBefore,
		readMigrationRows(t, ctx, database.SQL, "cards",
			"id,state,due,stability,difficulty,elapsed_days,scheduled_days,step,reps,lapses,last_review,suspended,is_leech"))

	if _, err := database.SQL.ExecContext(ctx, "DELETE FROM users WHERE id = ?", owner.ID); err != nil {
		t.Fatalf("delete owner with nested decks, notes, cards, and import batch: %v", err)
	}
	for _, table := range []string{"decks", "notes", "cards", "import_batches"} {
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

func TestMigrateFailureRollsBackAndRestoresForeignKeys(t *testing.T) {
	ctx := context.Background()
	dsn, _, err := dataSourceName(filepath.Join(t.TempDir(), "failed-migration.db"))
	if err != nil {
		t.Fatalf("dataSourceName() error = %v", err)
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close failed-migration database: %v", err)
		}
	})
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping failed-migration database: %v", err)
	}

	brokenMigrations := fstest.MapFS{
		"00001_deliberately_broken.sql": &fstest.MapFile{Data: []byte(`-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
CREATE TABLE migration_partial_ddl (id INTEGER PRIMARY KEY);
INSERT INTO missing_migration_table VALUES (1);
COMMIT;
-- +goose Down
DROP TABLE migration_partial_ddl;
`)},
	}
	if err := migrate(ctx, database, brokenMigrations); err == nil {
		t.Fatal("migrate(deliberately broken migration) error = nil, want an error")
	}
	assertForeignKeysEnabled(t, ctx, database)
	var partialTables int
	if err := database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'migration_partial_ddl'").Scan(&partialTables); err != nil {
		t.Fatalf("check partial migration DDL: %v", err)
	}
	if partialTables != 0 {
		t.Fatalf("partial migration table count = %d, want rollback to remove it", partialTables)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE cleanup_fk_parent (id INTEGER PRIMARY KEY);
		CREATE TABLE cleanup_fk_child (parent_id INTEGER REFERENCES cleanup_fk_parent(id));`); err != nil {
		t.Fatalf("create FK cleanup probe: %v", err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO cleanup_fk_child(parent_id) VALUES (1)"); err == nil {
		t.Fatal("foreign-key check after failed migration accepted an orphan row")
	}
}

type originalContentIDs struct {
	rootDeck     int64
	archivedDeck int64
	archivedNote int64
	deckIDs      []int64
	noteIDs      []int64
	cardIDs      []int64
}

func seedOriginalContentRows(t *testing.T, ctx context.Context, database *sql.DB, ownerID int64) originalContentIDs {
	t.Helper()
	insertID := func(query string, args ...any) int64 {
		t.Helper()
		result, err := database.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("insert original content row: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("read original content row ID: %v", err)
		}
		return id
	}

	root := insertID(`
		INSERT INTO decks (
			owner_id, parent_id, name, description, position, lang_target, lang_native,
			reverse_cards, wrong_article_rating, color_seed, source, external_id,
			expected_count, source_deck_id, created_at, updated_at, deleted_at
		) VALUES (?, NULL, 'Root', 'preserve root fields', 7, 'de', 'pl', 1, 3, '#123456',
			'school-wordsets', 'root-1', 12, 987654, ?, ?, NULL)`,
		ownerID, contentTime(1), contentTime(2),
	)
	child := insertID(`
		INSERT INTO decks (owner_id, parent_id, name, position, source, external_id, created_at, updated_at)
		VALUES (?, ?, 'Child', 2, 'school-wordsets', 'child-1', ?, ?)`,
		ownerID, root, contentTime(3), contentTime(4),
	)
	grandchild := insertID(`
		INSERT INTO decks (owner_id, parent_id, name, position, source, external_id, created_at, updated_at)
		VALUES (?, ?, 'Grandchild', 4, 'school-wordsets', 'grandchild-1', ?, ?)`,
		ownerID, child, contentTime(5), contentTime(6),
	)
	archivedDeck := insertID(`
		INSERT INTO decks (owner_id, name, created_at, updated_at, deleted_at)
		VALUES (?, 'Archived before v4', ?, ?, ?)`,
		ownerID, contentTime(1), contentTime(2), contentTime(3),
	)
	batchID := "original-v3-import"
	hash := bytes.Repeat([]byte{0x7b}, 32)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO import_batches (
			id, user_id, adapter, filename, raw, raw_sha256, status, summary, created_at, applied_at
		) VALUES (?, ?, 'retain', 'source.json', ?, ?, 'applied', '{"imported":3}', ?, ?)
		`, batchID, ownerID, []byte("original gzipped source"), hash, contentTime(7), contentTime(8)); err != nil {
		t.Fatalf("insert original import batch: %v", err)
	}
	archivedNote := insertID(`
		INSERT INTO notes (deck_id, term, term_key, translation, created_at, updated_at, deleted_at)
		VALUES (?, 'archived word', 'archived word', 'archived translation', ?, ?, ?)`,
		archivedDeck, contentTime(4), contentTime(5), contentTime(6),
	)

	decks := []int64{root, child, grandchild}
	notes := make([]int64, 0, len(decks))
	cards := make([]int64, 0, len(decks))
	for index, deckID := range decks {
		term := []string{"root word", "child word", "grandchild word"}[index]
		noteID := insertID(`
			INSERT INTO notes (
				deck_id, term, term_key, translation, transcription, example,
				example_translation, notes, tags, alternatives, fields, external_id,
				import_batch_id, created_at, updated_at, deleted_at
			) VALUES (?, ?, ?, ?, 'ˈlegacy', 'example', 'przykład', 'keep note',
				'["topic:legacy"]', '["variant"]', '{"kind":"word"}', ?, ?, ?, ?, NULL)`,
			deckID, term, term, "translation of "+term, "legacy-word-"+string(rune('1'+index)),
			batchID, contentTime(8+index), contentTime(9+index),
		)
		cardID := insertID(`
			INSERT INTO cards (
				note_id, user_id, direction, state, due, stability, difficulty, elapsed_days,
				scheduled_days, step, reps, lapses, last_review, suspended, is_leech, updated_at
			) VALUES (?, ?, 'forward', 2, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			noteID, ownerID, contentTime(12+index), 4.5+float64(index), 3.5+float64(index)/10,
			10+index, 20+index, index+1, 30+index, 1+index, contentTime(10+index),
			index%2, (index+1)%2, contentTime(20+index),
		)
		notes = append(notes, noteID)
		cards = append(cards, cardID)
	}
	return originalContentIDs{
		rootDeck: root, archivedDeck: archivedDeck, archivedNote: archivedNote,
		deckIDs: decks, noteIDs: notes, cardIDs: cards,
	}
}

func readOriginalContentRows(t *testing.T, ctx context.Context, database *sql.DB) map[string][][]any {
	t.Helper()
	return map[string][][]any{
		"decks":          readMigrationRows(t, ctx, database, "decks", "id,owner_id,parent_id,name,description,position,lang_target,lang_native,reverse_cards,wrong_article_rating,color_seed,source,external_id,expected_count,source_deck_id,created_at,updated_at,deleted_at"),
		"notes":          readMigrationRows(t, ctx, database, "notes", "id,deck_id,term,term_key,translation,transcription,example,example_translation,notes,tags,alternatives,fields,external_id,import_batch_id,created_at,updated_at,deleted_at"),
		"cards":          readMigrationRows(t, ctx, database, "cards", "id,note_id,user_id,direction,state,due,stability,difficulty,elapsed_days,scheduled_days,step,reps,lapses,last_review,suspended,is_leech,updated_at"),
		"import_batches": readMigrationRows(t, ctx, database, "import_batches", "id,user_id,adapter,filename,raw,raw_sha256,status,summary,created_at,applied_at"),
	}
}

func readMigrationRows(t *testing.T, ctx context.Context, database *sql.DB, table, columns string) [][]any {
	t.Helper()
	rows, err := database.QueryContext(ctx, "SELECT "+columns+" FROM "+table+" ORDER BY 1")
	if err != nil {
		t.Fatalf("read %s migration snapshot: %v", table, err)
	}
	defer rows.Close()
	columnNames := strings.Split(columns, ",")
	var snapshot [][]any
	for rows.Next() {
		values := make([]any, len(columnNames))
		destinations := make([]any, len(columnNames))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatalf("scan %s migration snapshot: %v", table, err)
		}
		for index, value := range values {
			if raw, ok := value.([]byte); ok {
				values[index] = bytes.Clone(raw)
			}
		}
		snapshot = append(snapshot, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s migration snapshot: %v", table, err)
	}
	return snapshot
}

func assertOriginalContentRowsEqual(t *testing.T, ctx context.Context, database *sql.DB, want map[string][][]any) {
	t.Helper()
	got := readOriginalContentRows(t, ctx, database)
	for table, expected := range want {
		if !reflect.DeepEqual(got[table], expected) {
			t.Errorf("%s rows changed during migration:\n got: %#v\nwant: %#v", table, got[table], expected)
		}
	}
}

func assertMigrationRowsEqual(t *testing.T, label string, want, got [][]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got: %#v\nwant: %#v", label, got, want)
	}
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema indexes/triggers changed during migration:\n got: %#v\nwant: %#v", got, want)
	}
}

func assertLatestMigrationSchema(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	objects := readMigrationSchemaObjects(t, ctx, database)
	wantNames := []string{
		"index.sessions_expires_at_idx",
		"index.decks_owner_parent_idx", "index.decks_parent_id_idx", "index.decks_owner_source_external_active_uq",
		"index.import_batches_user_created_idx", "index.notes_deck_id_idx", "index.notes_deck_term_key_active_uq",
		"index.notes_deck_external_active_uq", "index.cards_user_due_idx", "index.cards_note_id_idx",
		"trigger.decks_soft_delete_cascade", "trigger.decks_restore_cascade",
		"trigger.notes_soft_delete_cards", "trigger.notes_restore_cards",
	}
	if len(objects) != len(wantNames) {
		t.Errorf("latest schema has %d explicit indexes/triggers, want %d: %#v", len(objects), len(wantNames), objects)
	}
	for _, name := range wantNames {
		if _, ok := objects[name]; !ok {
			t.Errorf("latest schema is missing %s", name)
		}
	}
	for _, name := range []string{
		"index.decks_owner_source_external_active_uq",
		"index.notes_deck_term_key_active_uq",
		"index.notes_deck_external_active_uq",
	} {
		if !strings.Contains(objects[name], "WHERE deleted_at IS NULL") {
			t.Errorf("%s is not active-row-only: %s", name, objects[name])
		}
	}
}

func assertLatestMigrationColumns(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	for table, wanted := range map[string][]string{
		"decks": {"deletion_batch"},
		"notes": {"deletion_batch"},
		"cards": {"deleted_at", "deletion_batch"},
	} {
		rows, err := database.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			t.Fatalf("read %s columns: %v", table, err)
		}
		columns := make(map[string]bool)
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				t.Fatalf("scan %s column: %v", table, err)
			}
			columns[name] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate %s columns: %v", table, err)
		}
		rows.Close()
		for _, column := range wanted {
			if !columns[column] {
				t.Errorf("%s is missing migrated column %s", table, column)
			}
		}
	}
}

func assertPartialUniqueIndex(t *testing.T, ctx context.Context, database *sql.DB, table, wantName string) {
	t.Helper()
	rows, err := database.QueryContext(ctx, "PRAGMA index_list("+table+")")
	if err != nil {
		t.Fatalf("read %s indexes: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence, isUnique, isPartial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &isUnique, &origin, &isPartial); err != nil {
			t.Fatalf("scan %s index: %v", table, err)
		}
		if name == wantName {
			if isUnique != 1 || isPartial != 1 {
				t.Errorf("%s index flags = unique %d partial %d, want 1/1", name, isUnique, isPartial)
			}
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s indexes: %v", table, err)
	}
	t.Errorf("%s index is missing from %s", wantName, table)
}

func migrationDeckParentDeleteAction(t *testing.T, ctx context.Context, database *sql.DB) string {
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

func assertMigrationForeignKey(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	table, column, parent, wantAction string,
) {
	t.Helper()
	rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_list("+table+")")
	if err != nil {
		t.Fatalf("read %s foreign keys: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var target, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan %s foreign key: %v", table, err)
		}
		if from == column && target == parent {
			if onDelete != wantAction {
				t.Errorf("%s.%s ON DELETE = %q, want %q", table, column, onDelete, wantAction)
			}
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s foreign keys: %v", table, err)
	}
	t.Errorf("foreign key %s.%s -> %s is missing", table, column, parent)
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

func assertMigrationDeletionBatch(t *testing.T, ctx context.Context, database *sql.DB, table string, id int64, want string) {
	t.Helper()
	var got sql.NullString
	if err := database.QueryRowContext(ctx,
		"SELECT deletion_batch FROM "+table+" WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("read %s %d deletion batch: %v", table, id, err)
	}
	if !got.Valid || got.String != want {
		t.Errorf("%s %d deletion_batch = %+v, want %q", table, id, got, want)
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
