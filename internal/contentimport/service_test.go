package contentimport

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/importer"
	"github.com/pestit/retain/internal/importer/adapters"
)

type importTestClock struct{ now time.Time }

func (c importTestClock) Now() time.Time { return c.now }

var _ clock.Clock = importTestClock{}

func newImportFixture(t *testing.T) (*db.DB, *Service, int64, int64) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	queries := database.Queries()
	owner := makeImportUser(t, queries, "import-owner@example.test")
	other := makeImportUser(t, queries, "import-other@example.test")
	fixed := importTestClock{now: time.Date(2026, time.August, 9, 12, 30, 0, 0, time.UTC)}
	return database, NewService(database, fixed), owner.ID, other.ID
}

func makeImportUser(t *testing.T, queries *gen.Queries, email string) gen.User {
	t.Helper()
	user, err := queries.CreateUser(context.Background(), gen.CreateUserParams{
		Email: email, Name: "Importer", PasswordHash: "test-hash",
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", email, err)
	}
	return user
}

func TestPreviewApplyReimportHistoryAndExports(t *testing.T) {
	database, service, ownerID, _ := newImportFixture(t)
	ctx := context.Background()
	first := retainBundle(t, importer.Bundle{
		Format: "retain/v1", Source: "school-wordsets",
		Decks: []importer.Deck{{ExternalID: "class:1", Name: "German class", LangTarget: "de", LangNative: "pl", Decks: []importer.Deck{{
			ExternalID: "wordset:1", Name: "Unit 1", ExpectedCount: int64Pointer(2), Notes: []importer.Note{
				{ExternalID: "note:haus", Term: "Haus", Translation: "house", Example: "Das Haus", Tags: []string{"home"}},
				{ExternalID: "note:gehen", Term: "gehen", Translation: "to go"},
			},
		}}}},
	})
	preview, err := service.Preview(ctx, ownerID, "first.retain.json", "", first)
	if err != nil {
		t.Fatalf("Preview(first) error = %v", err)
	}
	if preview.Status != "previewed" || preview.Counts.New != 2 || preview.Counts.Changed != 0 || preview.Counts.Unchanged != 0 {
		t.Fatalf("first preview counts/status = %+v %+v", preview.Counts, preview)
	}
	batch, err := database.Queries().GetImportBatch(ctx, gen.GetImportBatchParams{ID: preview.BatchID, UserID: ownerID})
	if err != nil {
		t.Fatalf("GetImportBatch() error = %v", err)
	}
	gzipReader, err := gzip.NewReader(strings.NewReader(string(batch.Raw)))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	storedRaw, err := io.ReadAll(gzipReader)
	_ = gzipReader.Close()
	if err != nil || string(storedRaw) != string(first) {
		t.Fatalf("stored compressed source = %q, %v; want original source", storedRaw, err)
	}
	digest := sha256.Sum256(first)
	if string(batch.RawSha256) != string(digest[:]) {
		t.Fatal("stored import digest does not match original source")
	}
	apply := ApplyInput{LangTarget: "de", LangNative: "pl"}
	if result, err := service.Apply(ctx, ownerID, preview.BatchID, apply); err != nil || result.Status != "applied" {
		t.Fatalf("Apply(first) = %+v, %v", result, err)
	}
	root := getImportedDeck(t, database, ownerID, "school-wordsets", "class:1")
	child := getImportedDeck(t, database, ownerID, "school-wordsets", "wordset:1")
	notes, err := database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: child.ID, OwnerID: ownerID})
	if err != nil || len(notes) != 2 {
		t.Fatalf("first applied notes = %d, %v; want 2", len(notes), err)
	}
	var haus gen.Note
	for _, note := range notes {
		if note.Term == "Haus" {
			haus = note
		}
	}
	cards, err := database.Queries().ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: haus.ID, UserID: ownerID})
	if err != nil || len(cards) != 1 {
		t.Fatalf("Haus cards = %+v, %v; want forward card", cards, err)
	}
	if _, err := database.SQL.ExecContext(ctx, `UPDATE cards SET state = 2, due = ?, stability = 8.25, difficulty = 4.5, reps = 17, lapses = 3 WHERE id = ?`, time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC), cards[0].ID); err != nil {
		t.Fatalf("set FSRS progress: %v", err)
	}

	second := retainBundle(t, importer.Bundle{
		Format: "retain/v1", Source: "school-wordsets",
		Decks: []importer.Deck{{ExternalID: "class:1", Name: "German class renamed", LangTarget: "de", LangNative: "pl", Decks: []importer.Deck{{
			ExternalID: "wordset:1", Name: "Unit 1 renamed", ExpectedCount: int64Pointer(3), Notes: []importer.Note{
				{ExternalID: "note:haus", Term: "Haus", Translation: "building", Example: "Das Haus", Tags: []string{"home"}},
				{ExternalID: "note:gehen", Term: "gehen", Translation: "to go"},
				{ExternalID: "note:katze", Term: "Katze", Translation: "cat"},
			},
		}}}},
	})
	secondPreview, err := service.Preview(ctx, ownerID, "second.json", "retain", second)
	if err != nil {
		t.Fatalf("Preview(second) error = %v", err)
	}
	if secondPreview.Counts != (Counts{New: 1, Changed: 1, Unchanged: 1}) || secondPreview.DeckCounts != (DeckCounts{Changed: 2}) {
		t.Fatalf("second preview summary = counts %+v deck counts %+v", secondPreview.Counts, secondPreview.DeckCounts)
	}
	if len(secondPreview.Warnings) != 0 {
		t.Fatalf("second preview warnings = %v, want none", secondPreview.Warnings)
	}
	if _, err := service.Apply(ctx, ownerID, secondPreview.BatchID, apply); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	root = getImportedDeck(t, database, ownerID, "school-wordsets", "class:1")
	child = getImportedDeck(t, database, ownerID, "school-wordsets", "wordset:1")
	if root.Name != "German class renamed" || child.Name != "Unit 1 renamed" {
		t.Fatalf("updated deck names = %q / %q", root.Name, child.Name)
	}
	notes, err = database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: child.ID, OwnerID: ownerID})
	if err != nil || len(notes) != 3 {
		t.Fatalf("re-imported notes = %d, %v; want 3", len(notes), err)
	}
	progress, err := database.Queries().ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: haus.ID, UserID: ownerID})
	if err != nil || len(progress) != 1 || progress[0].State != 2 || progress[0].Stability.Float64 != 8.25 || progress[0].Difficulty.Float64 != 4.5 || progress[0].Reps != 17 || progress[0].Lapses != 3 {
		t.Fatalf("updated note FSRS progress = %+v, %v", progress, err)
	}

	thirdPreview, err := service.Preview(ctx, ownerID, "third.json", "retain", second)
	if err != nil {
		t.Fatalf("Preview(unchanged) error = %v", err)
	}
	if thirdPreview.Counts != (Counts{Unchanged: 3}) || thirdPreview.DeckCounts != (DeckCounts{Unchanged: 2}) {
		t.Fatalf("unchanged preview summary = counts %+v deck counts %+v", thirdPreview.Counts, thirdPreview.DeckCounts)
	}
	if _, err := service.Apply(ctx, ownerID, thirdPreview.BatchID, apply); err != nil {
		t.Fatalf("Apply(unchanged) error = %v", err)
	}
	history, err := service.History(ctx, ownerID)
	if err != nil || len(history) != 3 || history[0].Status != "applied" || history[0].AppliedAt == nil || history[0].SHA256 == "" {
		t.Fatalf("History() = %+v, %v", history, err)
	}

	retainExport, err := service.Export(ctx, ownerID, root.ID, "retain")
	if err != nil {
		t.Fatalf("Export(retain) error = %v", err)
	}
	var exported importer.Bundle
	if err := json.Unmarshal(retainExport.Body, &exported); err != nil {
		t.Fatalf("decode retain export: %v", err)
	}
	if exported.Format != "retain/v1" || len(exported.Decks) != 1 || len(exported.Decks[0].Decks) != 1 || len(exported.Decks[0].Decks[0].Notes) != 3 {
		t.Fatalf("retain export did not contain full descendant tree: %+v", exported)
	}
	csvExport, err := service.Export(ctx, ownerID, root.ID, "csv")
	if err != nil {
		t.Fatalf("Export(csv) error = %v", err)
	}
	csvBundle, err := (adapters.CSVAdapter{}).Parse(csvExport.Body)
	if err != nil || len(csvBundle.Decks) != 1 || len(csvBundle.Decks[0].Notes) != 3 {
		t.Fatalf("CSV export round trip = %+v, %v", csvBundle, err)
	}
}

func TestDeleteRecoveryKeepSuspendAndTermKeyFallback(t *testing.T) {
	database, service, ownerID, _ := newImportFixture(t)
	ctx := context.Background()
	apply := ApplyInput{LangTarget: "en", LangNative: "ru"}
	bundle := importer.Bundle{Format: "retain/v1", Source: "manual-source", Decks: []importer.Deck{{ExternalID: "root", Name: "English", Notes: []importer.Note{
		{ExternalID: "one", Term: " Zebra ", Translation: "animal"},
		{ExternalID: "two", Term: "horse", Translation: "animal"},
	}}}}
	firstRaw := retainBundle(t, bundle)
	first, err := service.Preview(ctx, ownerID, "source.json", "retain", firstRaw)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, first.BatchID, apply); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	root := getImportedDeck(t, database, ownerID, "manual-source", "root")
	notes, err := database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: root.ID, OwnerID: ownerID})
	if err != nil || len(notes) != 2 {
		t.Fatalf("initial notes = %d, %v", len(notes), err)
	}
	var zebra, horse gen.Note
	for _, note := range notes {
		if strings.EqualFold(strings.TrimSpace(note.Term), "zebra") {
			zebra = note
		} else {
			horse = note
		}
	}
	card, err := database.Queries().ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: zebra.ID, UserID: ownerID})
	if err != nil || len(card) != 1 {
		t.Fatalf("Zebra card = %+v, %v", card, err)
	}
	if _, err := database.SQL.ExecContext(ctx, `UPDATE cards SET state = 2, stability = 5.75, difficulty = 6.25, reps = 9 WHERE id = ?`, card[0].ID); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	if _, err := database.Queries().SoftDeleteDeck(ctx, gen.SoftDeleteDeckParams{
		DeletedAt: sql.NullTime{Time: time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC), Valid: true},
		UpdatedAt: time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC), ID: root.ID, OwnerID: ownerID,
	}); err != nil {
		t.Fatalf("SoftDeleteDeck() error = %v", err)
	}
	deleted, err := database.SQL.QueryContext(ctx, `SELECT count(*) FROM cards WHERE note_id IN (?, ?) AND deleted_at IS NOT NULL`, zebra.ID, horse.ID)
	if err != nil {
		t.Fatalf("check deleted cards: %v", err)
	}
	var deletedCards int
	if deleted.Next() {
		_ = deleted.Scan(&deletedCards)
	}
	_ = deleted.Close()
	if deletedCards != 2 {
		t.Fatalf("soft-deleted cards = %d, want 2", deletedCards)
	}

	recovery, err := service.Preview(ctx, ownerID, "recover.json", "retain", firstRaw)
	if err != nil {
		t.Fatalf("Preview(recovery) error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, recovery.BatchID, apply); err != nil {
		t.Fatalf("Apply(recovery) error = %v", err)
	}
	root = getImportedDeck(t, database, ownerID, "manual-source", "root")
	if root.ID == 0 || root.DeletedAt.Valid {
		t.Fatalf("recovered deck = %+v", root)
	}
	card, err = database.Queries().ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: zebra.ID, UserID: ownerID})
	if err != nil || len(card) != 1 || card[0].State != 2 || card[0].Stability.Float64 != 5.75 || card[0].Difficulty.Float64 != 6.25 || card[0].Reps != 9 {
		t.Fatalf("recovered card state = %+v, %v", card, err)
	}

	// A second import can find a word without source IDs by its normalized term_key.
	termKeyFallback := importer.Bundle{Format: "retain/v1", Source: "manual-source", Decks: []importer.Deck{{ExternalID: "root", Name: "English", Notes: []importer.Note{
		{Term: "zebra", Translation: "striped animal"},
		{ExternalID: "two", Term: "horse", Translation: "animal"},
	}}}}
	changedRaw := retainBundle(t, termKeyFallback)
	changedPreview, err := service.Preview(ctx, ownerID, "term-key.json", "retain", changedRaw)
	if err != nil || changedPreview.Counts != (Counts{Changed: 1, Unchanged: 1}) {
		t.Fatalf("term-key fallback preview = %+v, %v", changedPreview.Counts, err)
	}
	if _, err := service.Apply(ctx, ownerID, changedPreview.BatchID, apply); err != nil {
		t.Fatalf("Apply(term-key fallback) error = %v", err)
	}
	if _, err := database.Queries().SoftDeleteNote(ctx, gen.SoftDeleteNoteParams{
		DeletedAt: sql.NullTime{Time: time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC), Valid: true},
		UpdatedAt: time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC), ID: zebra.ID, OwnerID: ownerID,
	}); err != nil {
		t.Fatalf("SoftDeleteNote() error = %v", err)
	}
	individualRecovery, err := service.Preview(ctx, ownerID, "note-recover.json", "retain", changedRaw)
	if err != nil {
		t.Fatalf("Preview(individual recovery) error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, individualRecovery.BatchID, apply); err != nil {
		t.Fatalf("Apply(individual recovery) error = %v", err)
	}
	activeNotes, err := database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: root.ID, OwnerID: ownerID})
	if err != nil || len(activeNotes) != 2 {
		t.Fatalf("restored individual note count = %d, %v", len(activeNotes), err)
	}

	keepBundle := importer.Bundle{Format: "retain/v1", Source: "manual-source", Decks: []importer.Deck{{ExternalID: "root", Name: "English", Notes: []importer.Note{{Term: "zebra", Translation: "striped animal"}}}}}
	keepRaw := retainBundle(t, keepBundle)
	keepPreview, err := service.Preview(ctx, ownerID, "keep.json", "retain", keepRaw)
	if err != nil {
		t.Fatalf("Preview(keep) error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, keepPreview.BatchID, apply); err != nil {
		t.Fatalf("Apply(default keep) error = %v", err)
	}
	horseCards, err := database.Queries().ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: horse.ID, UserID: ownerID})
	if err != nil || len(horseCards) != 1 || horseCards[0].Suspended {
		t.Fatalf("missing word was not kept by default: %+v, %v", horseCards, err)
	}
	suspendPreview, err := service.Preview(ctx, ownerID, "suspend.json", "retain", keepRaw)
	if err != nil {
		t.Fatalf("Preview(suspend) error = %v", err)
	}
	apply.OnMissing = "suspend"
	if _, err := service.Apply(ctx, ownerID, suspendPreview.BatchID, apply); err != nil {
		t.Fatalf("Apply(on_missing=suspend) error = %v", err)
	}
	horseCards, err = database.Queries().ListCardsByNote(ctx, gen.ListCardsByNoteParams{NoteID: horse.ID, UserID: ownerID})
	if err != nil || len(horseCards) != 1 || !horseCards[0].Suspended {
		t.Fatalf("missing word was not suspended: %+v, %v", horseCards, err)
	}
}

func TestOwnerIsolationCSVReimportSchoolLanguageAndExpectedWarning(t *testing.T) {
	database, service, ownerID, otherID := newImportFixture(t)
	ctx := context.Background()
	csvRaw := []byte("term;translation;example;tags\nhello;привет;Hello there;greeting\n")
	apply := ApplyInput{LangTarget: "en", LangNative: "ru"}
	first, err := service.Preview(ctx, ownerID, "words.csv", "", csvRaw)
	if err != nil || first.Adapter != "csv" {
		t.Fatalf("CSV Preview = %+v, %v", first, err)
	}
	if _, err := service.Apply(ctx, ownerID, first.BatchID, apply); err != nil {
		t.Fatalf("CSV Apply() error = %v", err)
	}
	second, err := service.Preview(ctx, ownerID, "words.csv", "csv", csvRaw)
	if err != nil || second.Counts != (Counts{Unchanged: 1}) || second.DeckCounts != (DeckCounts{Unchanged: 1}) {
		t.Fatalf("CSV re-import preview = %+v, %v", second, err)
	}
	if _, err := service.Apply(ctx, ownerID, second.BatchID, apply); err != nil {
		t.Fatalf("CSV re-import Apply() error = %v", err)
	}
	csvDeck := getImportedDeck(t, database, ownerID, "csv", "")
	csvNotes, err := database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: csvDeck.ID, OwnerID: ownerID})
	if err != nil || len(csvNotes) != 1 {
		t.Fatalf("CSV re-import duplicated content: %d notes, %v", len(csvNotes), err)
	}

	schoolRaw := []byte(`{"classes":[{"class":{"title":"Class 1","group_id":10},"wordsets":[{"info":{"id":20,"name":"Chapter","expected_count":2},"entries":[{"word":"der Drachenflieger","translation":"paralotnia, lotnia (rare)"}]}]}]}`)
	school, err := service.Preview(ctx, ownerID, "school.json", "", schoolRaw)
	if err != nil || school.Adapter != "school-wordsets" || len(school.Warnings) != 1 {
		t.Fatalf("school preview/warnings = %+v, %v", school, err)
	}
	if _, err := service.Apply(ctx, ownerID, school.BatchID, ApplyInput{LangTarget: "de", LangNative: "ru"}); err != nil {
		t.Fatalf("school Apply() error = %v", err)
	}
	schoolRoot := getImportedDeck(t, database, ownerID, "school-wordsets", "class:10")
	schoolChild := getImportedDeck(t, database, ownerID, "school-wordsets", "wordset:20")
	if !schoolRoot.LangTarget.Valid || schoolRoot.LangTarget.String != "de" || !schoolRoot.LangNative.Valid || schoolRoot.LangNative.String != "ru" {
		t.Fatalf("school root language pair = %v / %v", schoolRoot.LangTarget, schoolRoot.LangNative)
	}
	if schoolChild.LangTarget.Valid || schoolChild.LangNative.Valid {
		t.Fatalf("school child language should inherit: %v / %v", schoolChild.LangTarget, schoolChild.LangNative)
	}
	schoolNotes, err := database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: schoolChild.ID, OwnerID: ownerID})
	if err != nil || len(schoolNotes) != 1 {
		t.Fatalf("school imported notes = %+v, %v", schoolNotes, err)
	}
	var schoolFields map[string]any
	var schoolAlternatives []string
	if json.Unmarshal([]byte(schoolNotes[0].Fields), &schoolFields) != nil || json.Unmarshal([]byte(schoolNotes[0].Alternatives), &schoolAlternatives) != nil {
		t.Fatalf("decode enriched school note fields: %s / %s", schoolNotes[0].Fields, schoolNotes[0].Alternatives)
	}
	if schoolNotes[0].Translation != "paralotnia" || schoolNotes[0].Notes.String != "(rare)" || schoolFields["article"] != "der" || schoolFields["pos"] != "noun" || len(schoolAlternatives) != 1 || schoolAlternatives[0] != "lotnia" {
		t.Fatalf("school import was not enriched using selected inherited German: %+v, fields=%v alternatives=%v", schoolNotes[0], schoolFields, schoolAlternatives)
	}
	reimportPreview, err := service.Preview(ctx, ownerID, "school-again.json", "", schoolRaw)
	if err != nil || reimportPreview.Counts != (Counts{Unchanged: 1}) || reimportPreview.DeckCounts != (DeckCounts{Unchanged: 2}) {
		t.Fatalf("enriched school re-import preview = %+v, %v", reimportPreview, err)
	}
	if _, err := service.Apply(ctx, ownerID, reimportPreview.BatchID, ApplyInput{LangTarget: "en", LangNative: "pl"}); err != nil {
		t.Fatalf("Apply(enriched school re-import) error = %v", err)
	}
	rootAfterReimport := getImportedDeck(t, database, ownerID, "school-wordsets", "class:10")
	if rootAfterReimport.LangTarget.String != "de" || rootAfterReimport.LangNative.String != "ru" {
		t.Fatalf("re-import unexpectedly changed the selected language pair: %v / %v", rootAfterReimport.LangTarget, rootAfterReimport.LangNative)
	}
	if _, err := service.Apply(ctx, otherID, first.BatchID, apply); err != ErrNotFound {
		t.Fatalf("other user's apply error = %v, want ErrNotFound", err)
	}
	otherHistory, err := service.History(ctx, otherID)
	if err != nil || len(otherHistory) != 0 {
		t.Fatalf("other user's import history = %+v, %v", otherHistory, err)
	}
	if _, err := service.Export(ctx, otherID, csvDeck.ID, "retain"); err != ErrNotFound {
		t.Fatalf("other user's export error = %v, want ErrNotFound", err)
	}
}

func TestCSVExportGuardsFormulaCellsAndRetainPreservesAllNoteFields(t *testing.T) {
	database, service, ownerID, _ := newImportFixture(t)
	ctx := context.Background()
	bundle := importer.Bundle{
		Format: "retain/v1", Source: "csv-safety",
		Decks: []importer.Deck{{
			ExternalID: "root", Name: "CSV safety",
			Notes: []importer.Note{{
				ExternalID: "note", Term: "=1+1", Translation: "+1+2", Transcription: "/ˈsɑːm.pəl/",
				Example: "@SUM", ExampleTranslation: "Example translation", Notes: "Important note",
				Tags: []string{"-cmd"}, Alternatives: []string{"alternate"},
				Fields: map[string]any{"article": "der", "custom": map[string]any{"value": "preserved"}},
			}},
		}},
	}
	preview, err := service.Preview(ctx, ownerID, "safety.json", "retain", retainBundle(t, bundle))
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, preview.BatchID, ApplyInput{LangTarget: "en", LangNative: "ru"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	root := getImportedDeck(t, database, ownerID, "csv-safety", "root")
	retainFile, err := service.Export(ctx, ownerID, root.ID, "retain")
	if err != nil {
		t.Fatalf("Export(retain) error = %v", err)
	}
	var retained importer.Bundle
	if err := json.Unmarshal(retainFile.Body, &retained); err != nil {
		t.Fatalf("decode retain export: %v", err)
	}
	retainedNote := retained.Decks[0].Notes[0]
	if retainedNote.Transcription != "/ˈsɑːm.pəl/" || retainedNote.ExampleTranslation != "Example translation" || retainedNote.Notes != "Important note" ||
		len(retainedNote.Alternatives) != 1 || retainedNote.Alternatives[0] != "alternate" || retainedNote.Fields["article"] != "der" {
		t.Fatalf("retain export lost note fields: %+v", retainedNote)
	}
	csvFile, err := service.Export(ctx, ownerID, root.ID, "csv")
	if err != nil {
		t.Fatalf("Export(csv) error = %v", err)
	}
	csvBundle, err := (adapters.CSVAdapter{}).Parse(csvFile.Body)
	if err != nil {
		t.Fatalf("parse CSV export: %v", err)
	}
	got := csvBundle.Decks[0].Notes[0]
	if got.Term != "'=1+1" || got.Translation != "'+1+2" || got.Example != "'@SUM" || len(got.Tags) != 1 || got.Tags[0] != "'-cmd" {
		t.Fatalf("CSV export formula cells were not neutralized: %+v; raw=%q", got, csvFile.Body)
	}
	for _, value := range []string{" =1+1", "\t+1+2", "\r\n-1+2", "\x00@SUM", "\ufeff=1+1"} {
		if safe := safeCSVCell(value); safe != "'"+value {
			t.Errorf("safeCSVCell(%q) = %q, want apostrophe-prefixed value", value, safe)
		}
	}
}

func TestSelectedSchoolLanguageDrivesInheritedEnrichmentAndStablePreview(t *testing.T) {
	database, service, ownerID, _ := newImportFixture(t)
	ctx := context.Background()
	raw := []byte(`{"classes":[{"class":{"title":"English class","group_id":"english"},"wordsets":[{"info":{"id":"english-set","name":"Chapter"},"entries":[{"word":"der Hund","translation":"dog, hound"}]}]}]}`)
	preview, err := service.Preview(ctx, ownerID, "english-school.json", "", raw)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, preview.BatchID, ApplyInput{LangTarget: "en", LangNative: "ru"}); err != nil {
		t.Fatalf("Apply(en) error = %v", err)
	}
	root := getImportedDeck(t, database, ownerID, "school-wordsets", "class:english")
	child := getImportedDeck(t, database, ownerID, "school-wordsets", "wordset:english-set")
	if !root.LangTarget.Valid || root.LangTarget.String != "en" || child.LangTarget.Valid {
		t.Fatalf("selected/inherited language pair = root %v, child %v", root.LangTarget, child.LangTarget)
	}
	notes, err := database.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: child.ID, OwnerID: ownerID})
	if err != nil || len(notes) != 1 {
		t.Fatalf("English school notes = %+v, %v", notes, err)
	}
	var fields map[string]any
	var alternatives []string
	if json.Unmarshal([]byte(notes[0].Fields), &fields) != nil || json.Unmarshal([]byte(notes[0].Alternatives), &alternatives) != nil {
		t.Fatalf("decode English note metadata: %s / %s", notes[0].Fields, notes[0].Alternatives)
	}
	if notes[0].Translation != "dog" || len(alternatives) != 1 || alternatives[0] != "hound" || len(fields) != 0 {
		t.Fatalf("English import did not use selected language: %+v fields=%v alternatives=%v", notes[0], fields, alternatives)
	}
	reimport, err := service.Preview(ctx, ownerID, "english-school-again.json", "", raw)
	if err != nil || reimport.Counts != (Counts{Unchanged: 1}) {
		t.Fatalf("English enriched preview is not stable: %+v, %v", reimport.Counts, err)
	}
}

func TestApplyRollsBackEveryWriteOnLateConflict(t *testing.T) {
	database, service, ownerID, _ := newImportFixture(t)
	ctx := context.Background()
	apply := ApplyInput{LangTarget: "en", LangNative: "ru"}
	initial := importer.Bundle{Format: "retain/v1", Source: "atomic", Decks: []importer.Deck{{ExternalID: "root", Name: "Original", Notes: []importer.Note{
		{ExternalID: "a", Term: "alpha", Translation: "first"},
		{ExternalID: "b", Term: "beta", Translation: "second"},
	}}}}
	first, err := service.Preview(ctx, ownerID, "initial.json", "retain", retainBundle(t, initial))
	if err != nil {
		t.Fatalf("Preview(initial) error = %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, first.BatchID, apply); err != nil {
		t.Fatalf("Apply(initial) error = %v", err)
	}
	root := getImportedDeck(t, database, ownerID, "atomic", "root")
	current := importer.Bundle{Format: "retain/v1", Source: "atomic", Decks: []importer.Deck{{ExternalID: "root", Name: "Renamed", Notes: []importer.Note{
		{ExternalID: "a", Term: "alpha-new", Translation: "changed first"},
		{ExternalID: "b", Term: "beta", Translation: "second"},
	}}}}
	preview, err := service.Preview(ctx, ownerID, "conflict.json", "retain", retainBundle(t, current))
	if err != nil {
		t.Fatalf("Preview(conflict candidate) error = %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `UPDATE notes SET term = ?, term_key = ? WHERE deck_id = ? AND external_id = ?`, "alpha-new", termKey("alpha-new"), root.ID, "b"); err != nil {
		t.Fatalf("create late conflicting state: %v", err)
	}
	if _, err := service.Apply(ctx, ownerID, preview.BatchID, apply); err != ErrConflict {
		t.Fatalf("Apply(late conflict) error = %v, want ErrConflict", err)
	}
	root = getImportedDeck(t, database, ownerID, "atomic", "root")
	if root.Name != "Original" {
		t.Fatalf("deck update escaped rollback: name = %q", root.Name)
	}
	note, err := database.Queries().GetNoteByExternalID(ctx, gen.GetNoteByExternalIDParams{
		DeckID: root.ID, OwnerID: ownerID, ExternalID: sql.NullString{String: "a", Valid: true},
	})
	if err != nil || note.Term != "alpha" || note.Translation != "first" {
		t.Fatalf("first note update escaped rollback: %+v, %v", note, err)
	}
	batch, err := database.Queries().GetImportBatch(ctx, gen.GetImportBatchParams{ID: preview.BatchID, UserID: ownerID})
	if err != nil || batch.Status != "previewed" {
		t.Fatalf("batch state escaped rollback: %+v, %v", batch, err)
	}
}

func retainBundle(t *testing.T, bundle importer.Bundle) []byte {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("json.Marshal(bundle): %v", err)
	}
	return raw
}

func getImportedDeck(t *testing.T, database *db.DB, ownerID int64, source, externalID string) gen.Deck {
	t.Helper()
	if externalID == "" {
		rows, err := database.Queries().ListDecksByOwner(context.Background(), ownerID)
		if err != nil {
			t.Fatalf("ListDecksByOwner() error = %v", err)
		}
		for _, row := range rows {
			if row.Source.Valid && row.Source.String == source && !row.ExternalID.Valid {
				return row
			}
		}
		t.Fatalf("no imported deck for source %q without external ID", source)
	}
	deck, err := database.Queries().GetDeckBySourceExternalID(context.Background(), gen.GetDeckBySourceExternalIDParams{
		OwnerID: ownerID, Source: sql.NullString{String: source, Valid: true}, ExternalID: sql.NullString{String: externalID, Valid: true},
	})
	if err != nil {
		t.Fatalf("GetDeckBySourceExternalID(%q/%q) error = %v", source, externalID, err)
	}
	return deck
}

func int64Pointer(value int64) *int64 { return &value }
