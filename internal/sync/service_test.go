package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/srs"
	"github.com/pestit/retain/internal/study"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type syncFixture struct {
	database *db.DB
	service  *Service
	now      time.Time
	ownerID  int64
	otherID  int64
	cardID   int64
	noteID   int64
}

func newSyncFixture(t *testing.T, withCard bool) *syncFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("DB.Close() error = %v", err)
		}
	})
	now := time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC)
	ownerID := syncCreateUser(t, database, "owner@example.test")
	otherID := syncCreateUser(t, database, "other@example.test")
	clock := fixedClock{now: now}
	studyService := study.NewService(database, clock)
	fixture := &syncFixture{
		database: database, service: NewService(database, studyService), now: now,
		ownerID: ownerID, otherID: otherID,
	}
	if withCard {
		fixture.cardID, fixture.noteID = syncCreateCard(t, database, ownerID, "hello")
	}
	return fixture
}

func syncCreateUser(t *testing.T, database *db.DB, email string) int64 {
	t.Helper()
	result, err := database.SQL.Exec(`INSERT INTO users(email, name, password_hash) VALUES (?, 'Sync user', 'unused')`, email)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read user ID: %v", err)
	}
	if _, err := database.Queries().CreateUserSettings(context.Background(), userID); err != nil {
		t.Fatalf("create settings: %v", err)
	}
	return userID
}

func syncCreateCard(t *testing.T, database *db.DB, userID int64, term string) (int64, int64) {
	t.Helper()
	deck, err := database.SQL.Exec(`INSERT INTO decks(owner_id, name) VALUES (?, 'Sync deck')`, userID)
	if err != nil {
		t.Fatalf("insert deck: %v", err)
	}
	deckID, err := deck.LastInsertId()
	if err != nil {
		t.Fatalf("read deck ID: %v", err)
	}
	note, err := database.SQL.Exec(`INSERT INTO notes(deck_id, term, term_key, translation) VALUES (?, ?, ?, 'translation')`, deckID, term, strings.ToLower(term))
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	noteID, err := note.LastInsertId()
	if err != nil {
		t.Fatalf("read note ID: %v", err)
	}
	card, err := database.SQL.Exec(`INSERT INTO cards(note_id, user_id, direction) VALUES (?, ?, 'forward')`, noteID, userID)
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}
	cardID, err := card.LastInsertId()
	if err != nil {
		t.Fatalf("read card ID: %v", err)
	}
	return cardID, noteID
}

func TestSyncCursorPagesOwnerScopedEventsAndStabilizes(t *testing.T) {
	f := newSyncFixture(t, false)
	_, err := f.database.SQL.Exec(`INSERT INTO decks(owner_id, name) VALUES (?, 'owner')`, f.ownerID)
	if err != nil {
		t.Fatalf("insert decks: %v", err)
	}
	var ownerDeckID int64
	if err := f.database.SQL.QueryRow(`SELECT id FROM decks WHERE owner_id = ?`, f.ownerID).Scan(&ownerDeckID); err != nil {
		t.Fatalf("read owner deck: %v", err)
	}
	for index := range maxPageSize/2 + 1 {
		_, err := f.database.SQL.Exec(`INSERT INTO notes(deck_id, term, term_key, translation) VALUES (?, ?, ?, 'translation')`, ownerDeckID, fmt.Sprintf("word-%03d", index), fmt.Sprintf("word-%03d", index))
		if err != nil {
			t.Fatalf("insert owner note %d: %v", index, err)
		}
		var noteID int64
		if err := f.database.SQL.QueryRow(`SELECT id FROM notes WHERE deck_id = ? AND term_key = ?`, ownerDeckID, fmt.Sprintf("word-%03d", index)).Scan(&noteID); err != nil {
			t.Fatalf("read owner note %d: %v", index, err)
		}
		if _, err := f.database.SQL.Exec(`INSERT INTO cards(note_id, user_id, direction) VALUES (?, ?, 'forward')`, noteID, f.ownerID); err != nil {
			t.Fatalf("insert owner card %d: %v", index, err)
		}
	}
	syncCreateCard(t, f.database, f.otherID, "private")
	first, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	if !first.HasMore || len(first.ChangedNotes)+len(first.ChangedCards) != maxPageSize {
		t.Fatalf("first page = notes:%d cards:%d has_more:%v, want %d events and more", len(first.ChangedNotes), len(first.ChangedCards), first.HasMore, maxPageSize)
	}
	second, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: first.Cursor})
	if err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}
	if second.HasMore || len(second.ChangedNotes)+len(second.ChangedCards) != 2 {
		t.Fatalf("second page = notes:%d cards:%d has_more:%v, want 2 events and no more", len(second.ChangedNotes), len(second.ChangedCards), second.HasMore)
	}
	stable, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: second.Cursor})
	if err != nil {
		t.Fatalf("stable Sync() error = %v", err)
	}
	if stable.Cursor != second.Cursor || stable.HasMore || len(stable.ChangedNotes) != 0 || len(stable.ChangedCards) != 0 {
		t.Errorf("stable page = %+v, want unchanged cursor and no events", stable)
	}
	for _, payload := range second.ChangedCards {
		var card struct {
			UserID int64  `json:"user_id"`
			Due    string `json:"due"`
		}
		if err := json.Unmarshal(payload, &card); err != nil {
			t.Fatalf("decode card payload: %v", err)
		}
		if card.UserID != f.ownerID {
			t.Errorf("owner feed leaked card for user %d", card.UserID)
		}
		if _, err := time.Parse(time.RFC3339Nano, card.Due); err != nil {
			t.Errorf("sync card due %q is not RFC3339: %v", card.Due, err)
		}
	}
}

func TestSyncCursorRejectsTamperingAndAnotherUser(t *testing.T) {
	f := newSyncFixture(t, false)
	first, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(first.Cursor) < 3 {
		t.Fatalf("cursor = %q, want signed opaque value", first.Cursor)
	}
	tampered := first.Cursor[:len(first.Cursor)-1] + map[bool]string{true: "A", false: "B"}[first.Cursor[len(first.Cursor)-1] == 'A']
	if _, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: tampered}); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("tampered cursor error = %v, want ErrInvalidCursor", err)
	}
	if _, err := f.service.Sync(context.Background(), f.otherID, Request{Since: first.Cursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("cross-user cursor error = %v, want ErrInvalidCursor", err)
	}
	var value string
	if err := f.database.SQL.QueryRow(`SELECT value FROM app_settings WHERE key = 'sync_cursor_hmac_key'`).Scan(&value); err != nil {
		t.Fatalf("read stored cursor key: %v", err)
	}
	var encoded string
	if err := json.Unmarshal([]byte(value), &encoded); err != nil || encoded == "" {
		t.Errorf("stored cursor key = %q, want JSON-encoded key", value)
	}
}

func TestSyncAppliesReviewIdempotentlyAndReturnsTombstones(t *testing.T) {
	f := newSyncFixture(t, true)
	initial, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	input := study.ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000001", CardID: f.cardID,
		Rating: srs.Good, Mode: "flip", ReviewedAt: f.now, DurationMS: 900,
	}
	first, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: initial.Cursor, Reviews: []study.ReviewInput{input}})
	if err != nil {
		t.Fatalf("review Sync() error = %v", err)
	}
	if len(first.Applied) != 1 || first.Applied[0] != input.ID || len(first.ChangedCards) != 1 {
		t.Fatalf("review response = %+v, want acknowledged ID and authoritative card", first)
	}
	var card struct {
		ID         int64  `json:"id"`
		SyncSeq    int64  `json:"sync_seq"`
		State      int64  `json:"state"`
		Deleted    bool   `json:"deleted"`
		Due        string `json:"due"`
		LastReview string `json:"last_review"`
	}
	if err := json.Unmarshal(first.ChangedCards[0], &card); err != nil {
		t.Fatalf("decode authoritative card: %v", err)
	}
	if card.ID != f.cardID || card.State == int64(srs.New) || card.Deleted || card.SyncSeq <= 0 {
		t.Errorf("authoritative card = %+v, want reviewed active card", card)
	}
	for label, value := range map[string]string{"due": card.Due, "last_review": card.LastReview} {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			t.Errorf("sync card %s %q is not RFC3339: %v", label, value, err)
		}
	}
	second, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: first.Cursor, Reviews: []study.ReviewInput{input}})
	if err != nil {
		t.Fatalf("duplicate review Sync() error = %v", err)
	}
	if len(second.Applied) != 1 || second.Applied[0] != input.ID || len(second.ChangedCards) != 0 {
		t.Errorf("duplicate response = %+v, want idempotent acknowledgement without a new event", second)
	}
	var reviewCount int
	if err := f.database.SQL.QueryRow(`SELECT COUNT(*) FROM review_logs WHERE id = ?`, input.ID).Scan(&reviewCount); err != nil || reviewCount != 1 {
		t.Fatalf("review row count = %d, err = %v; want exactly one", reviewCount, err)
	}

	deleteAt := f.now.Add(time.Minute)
	if _, err := f.database.SQL.Exec(`UPDATE notes SET deleted_at = ?, deletion_batch = 'sync-test-delete' WHERE id = ?`, deleteAt, f.noteID); err != nil {
		t.Fatalf("soft-delete note: %v", err)
	}
	tombstones, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: first.Cursor})
	if err != nil {
		t.Fatalf("tombstone Sync() error = %v", err)
	}
	if !containsTombstone(t, tombstones.ChangedNotes, f.noteID) || !containsTombstone(t, tombstones.ChangedCards, f.cardID) {
		t.Errorf("delete response lacks note/card tombstones: %+v", tombstones)
	}
}

func TestSyncRejectsInvalidReviewAndStillReturnsChangeFeed(t *testing.T) {
	f := newSyncFixture(t, true)
	initial, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	newCardID, _ := syncCreateCard(t, f.database, f.ownerID, "new note")
	invalid := study.ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000002", CardID: f.cardID + 10_000,
		Rating: srs.Good, Mode: "flip", ReviewedAt: f.now, DurationMS: 900,
	}
	response, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: initial.Cursor, Reviews: []study.ReviewInput{invalid}})
	if err != nil {
		t.Fatalf("Sync() error = %v, want per-review rejection without failing feed", err)
	}
	if len(response.Applied) != 0 || len(response.Rejected) != 1 || response.Rejected[0] != (RejectedReview{ID: invalid.ID, Code: "not_found"}) {
		t.Errorf("review result applied=%v rejected=%+v, want one not_found rejection", response.Applied, response.Rejected)
	}
	if response.Cursor == initial.Cursor || len(response.ChangedCards) != 1 {
		t.Errorf("sync feed cursor=%q changed_cards=%d; want cursor advance with the unrelated card", response.Cursor, len(response.ChangedCards))
	}
	var card struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(response.ChangedCards[0], &card); err != nil {
		t.Fatalf("decode changed card: %v", err)
	}
	if card.ID != newCardID {
		t.Errorf("changed card ID = %d, want %d", card.ID, newCardID)
	}
	invalidRating := study.ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000003", CardID: f.cardID,
		Rating: srs.Rating(9), Mode: "flip", ReviewedAt: f.now, DurationMS: 900,
	}
	reverted, err := f.service.Sync(context.Background(), f.ownerID, Request{
		Since: response.Cursor, Reviews: []study.ReviewInput{invalidRating},
	})
	if err != nil {
		t.Fatalf("invalid-rating Sync() error = %v", err)
	}
	if len(reverted.Rejected) != 1 || reverted.Rejected[0].Code != "invalid_input" || len(reverted.ChangedCards) != 1 {
		t.Fatalf("invalid-rating response = %+v, want rejection and current card snapshot", reverted)
	}
	var snapshot struct {
		ID      int64 `json:"id"`
		SyncSeq int64 `json:"sync_seq"`
		State   int64 `json:"state"`
		UserID  int64 `json:"user_id"`
		Deleted bool  `json:"deleted"`
	}
	if err := json.Unmarshal(reverted.ChangedCards[0], &snapshot); err != nil {
		t.Fatalf("decode rejection card snapshot: %v", err)
	}
	if snapshot.ID != f.cardID || snapshot.UserID != f.ownerID || snapshot.Deleted || snapshot.SyncSeq <= 0 {
		t.Errorf("rejection card snapshot = %+v, want active owner card %d", snapshot, f.cardID)
	}
}

func TestSyncAccountScopePreventsLeakingEventsWhenUserIDIsReused(t *testing.T) {
	f := newSyncFixture(t, false)
	syncCreateCard(t, f.database, f.otherID, "previous account word")
	oldAccount, err := f.service.Sync(context.Background(), f.otherID, Request{})
	if err != nil {
		t.Fatalf("old account Sync() error = %v", err)
	}
	var oldScope int64
	if err := f.database.SQL.QueryRow(`SELECT scope_id FROM sync_owner_scopes WHERE user_id = ?`, f.otherID).Scan(&oldScope); err != nil {
		t.Fatalf("read old account scope: %v", err)
	}
	if _, err := f.database.SQL.Exec(`DELETE FROM users WHERE id = ?`, f.otherID); err != nil {
		t.Fatalf("delete old account: %v", err)
	}
	newUserID := syncCreateUser(t, f.database, "replacement@example.test")
	if newUserID != f.otherID {
		t.Fatalf("reused user ID = %d, want %d to exercise reuse", newUserID, f.otherID)
	}
	var newScope int64
	if err := f.database.SQL.QueryRow(`SELECT scope_id FROM sync_owner_scopes WHERE user_id = ?`, newUserID).Scan(&newScope); err != nil {
		t.Fatalf("read replacement account scope: %v", err)
	}
	if newScope == oldScope {
		t.Fatalf("replacement account reused scope ID %d", newScope)
	}

	current, err := f.service.Sync(context.Background(), newUserID, Request{})
	if err != nil {
		t.Fatalf("replacement account Sync() error = %v", err)
	}
	if len(current.ChangedCards) != 0 || len(current.ChangedNotes) != 0 {
		t.Errorf("replacement account received old content: cards=%d notes=%d", len(current.ChangedCards), len(current.ChangedNotes))
	}
	if _, err := f.service.Sync(context.Background(), newUserID, Request{Since: oldAccount.Cursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("old account cursor error = %v, want ErrInvalidCursor", err)
	}
}

func TestSyncDeckVisibilityChangesTombstoneAndRestoreSubtree(t *testing.T) {
	f := newSyncFixture(t, true)
	var rootDeckID int64
	if err := f.database.SQL.QueryRow(`SELECT deck_id FROM notes WHERE id = ?`, f.noteID).Scan(&rootDeckID); err != nil {
		t.Fatalf("read root deck: %v", err)
	}
	childDeck, err := f.database.SQL.Exec(`INSERT INTO decks(owner_id, parent_id, name) VALUES (?, ?, 'child')`, f.ownerID, rootDeckID)
	if err != nil {
		t.Fatalf("insert child deck: %v", err)
	}
	childDeckID, err := childDeck.LastInsertId()
	if err != nil {
		t.Fatalf("read child deck ID: %v", err)
	}
	childNote, err := f.database.SQL.Exec(`INSERT INTO notes(deck_id, term, term_key, translation) VALUES (?, 'child word', 'child word', 'translation')`, childDeckID)
	if err != nil {
		t.Fatalf("insert child note: %v", err)
	}
	childNoteID, err := childNote.LastInsertId()
	if err != nil {
		t.Fatalf("read child note ID: %v", err)
	}
	childCard, err := f.database.SQL.Exec(`INSERT INTO cards(note_id, user_id, direction) VALUES (?, ?, 'forward')`, childNoteID, f.ownerID)
	if err != nil {
		t.Fatalf("insert child card: %v", err)
	}
	childCardID, err := childCard.LastInsertId()
	if err != nil {
		t.Fatalf("read child card ID: %v", err)
	}

	initial, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE decks SET deleted_at = ?, deletion_batch = 'sync-deck-delete', updated_at = ? WHERE id = ?`, f.now, f.now, rootDeckID); err != nil {
		t.Fatalf("soft-delete root deck: %v", err)
	}
	deleted, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: initial.Cursor})
	if err != nil {
		t.Fatalf("deleted deck Sync() error = %v", err)
	}
	for _, noteID := range []int64{f.noteID, childNoteID} {
		if !containsTombstone(t, deleted.ChangedNotes, noteID) {
			t.Errorf("deck deletion did not tombstone note %d", noteID)
		}
	}
	for _, cardID := range []int64{f.cardID, childCardID} {
		if !containsTombstone(t, deleted.ChangedCards, cardID) {
			t.Errorf("deck deletion did not tombstone card %d", cardID)
		}
	}

	if _, err := f.database.SQL.Exec(`UPDATE decks SET deleted_at = NULL, deletion_batch = NULL, updated_at = ? WHERE id = ?`, f.now.Add(time.Minute), rootDeckID); err != nil {
		t.Fatalf("restore root deck: %v", err)
	}
	restored, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: deleted.Cursor})
	if err != nil {
		t.Fatalf("restored deck Sync() error = %v", err)
	}
	for _, noteID := range []int64{f.noteID, childNoteID} {
		if !containsUpsert(t, restored.ChangedNotes, noteID) {
			t.Errorf("deck restoration did not upsert note %d", noteID)
		}
	}
	for _, cardID := range []int64{f.cardID, childCardID} {
		if !containsUpsert(t, restored.ChangedCards, cardID) {
			t.Errorf("deck restoration did not upsert card %d", cardID)
		}
	}
}

func TestSyncDoesNotPublishCardProgressForAnotherOwnersNote(t *testing.T) {
	f := newSyncFixture(t, true)
	foreignCard, err := f.database.SQL.Exec(`INSERT INTO cards(note_id, user_id, direction) VALUES (?, ?, 'forward')`, f.noteID, f.otherID)
	if err != nil {
		t.Fatalf("insert cross-owner card fixture: %v", err)
	}
	foreignCardID, err := foreignCard.LastInsertId()
	if err != nil {
		t.Fatalf("read cross-owner card ID: %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET reps = reps + 1 WHERE id = ?`, foreignCardID); err != nil {
		t.Fatalf("update cross-owner card fixture: %v", err)
	}

	response, err := f.service.Sync(context.Background(), f.otherID, Request{})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	for _, payload := range response.ChangedCards {
		var card struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(payload, &card); err != nil {
			t.Fatalf("decode card snapshot: %v", err)
		}
		if card.ID == foreignCardID {
			t.Errorf("published card %d referencing another owner's deck", foreignCardID)
		}
	}
}

func TestSyncTombstonesCardWhenItMovesToAnotherOwnersDeck(t *testing.T) {
	f := newSyncFixture(t, true)
	_, foreignNoteID := syncCreateCard(t, f.database, f.otherID, "foreign")
	ownerInitial, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("owner initial Sync() error = %v", err)
	}
	otherInitial, err := f.service.Sync(context.Background(), f.otherID, Request{})
	if err != nil {
		t.Fatalf("other initial Sync() error = %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET note_id = ? WHERE id = ?`, foreignNoteID, f.cardID); err != nil {
		t.Fatalf("move card to foreign deck: %v", err)
	}

	ownerChanges, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: ownerInitial.Cursor})
	if err != nil {
		t.Fatalf("owner change Sync() error = %v", err)
	}
	if !containsTombstone(t, ownerChanges.ChangedCards, f.cardID) {
		t.Fatalf("lost card access did not tombstone card %d", f.cardID)
	}
	otherChanges, err := f.service.Sync(context.Background(), f.otherID, Request{Since: otherInitial.Cursor})
	if err != nil {
		t.Fatalf("other change Sync() error = %v", err)
	}
	for _, payload := range otherChanges.ChangedCards {
		var card struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(payload, &card); err != nil {
			t.Fatalf("decode card event: %v", err)
		}
		if card.ID == f.cardID {
			t.Errorf("published another owner's card %d", f.cardID)
		}
	}
}

func TestSyncNoteMovedIntoAndOutOfDeletedDeckTombstonesAndRestoresCard(t *testing.T) {
	f := newSyncFixture(t, true)
	rootDeckID := syncDeckForNote(t, f.database, f.noteID)
	result, err := f.database.SQL.Exec(`INSERT INTO decks(owner_id, name) VALUES (?, 'deleted destination')`, f.ownerID)
	if err != nil {
		t.Fatalf("insert destination deck: %v", err)
	}
	deletedDeckID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read destination deck ID: %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE decks SET deleted_at = ?, deletion_batch = 'hidden-destination', updated_at = ? WHERE id = ?`, f.now, f.now, deletedDeckID); err != nil {
		t.Fatalf("delete destination deck: %v", err)
	}
	initial, err := f.service.Sync(context.Background(), f.ownerID, Request{})
	if err != nil {
		t.Fatalf("initial Sync() error = %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE notes SET deck_id = ?, updated_at = ? WHERE id = ?`, deletedDeckID, f.now.Add(time.Minute), f.noteID); err != nil {
		t.Fatalf("move note into deleted deck: %v", err)
	}
	hidden, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: initial.Cursor})
	if err != nil {
		t.Fatalf("hidden note Sync() error = %v", err)
	}
	if !containsTombstone(t, hidden.ChangedNotes, f.noteID) || !containsTombstone(t, hidden.ChangedCards, f.cardID) {
		t.Fatalf("moving note into inaccessible deck did not tombstone note/card: %+v", hidden)
	}
	if _, err := f.database.SQL.Exec(`UPDATE notes SET deck_id = ?, updated_at = ? WHERE id = ?`, rootDeckID, f.now.Add(2*time.Minute), f.noteID); err != nil {
		t.Fatalf("restore note to active deck: %v", err)
	}
	visible, err := f.service.Sync(context.Background(), f.ownerID, Request{Since: hidden.Cursor})
	if err != nil {
		t.Fatalf("visible note Sync() error = %v", err)
	}
	if !containsUpsert(t, visible.ChangedNotes, f.noteID) || !containsUpsert(t, visible.ChangedCards, f.cardID) {
		t.Fatalf("moving note back into accessible deck did not restore note/card: %+v", visible)
	}
}

func syncDeckForNote(t *testing.T, database *db.DB, noteID int64) int64 {
	t.Helper()
	var deckID int64
	if err := database.SQL.QueryRow(`SELECT deck_id FROM notes WHERE id = ?`, noteID).Scan(&deckID); err != nil {
		t.Fatalf("read note deck ID: %v", err)
	}
	return deckID
}

func containsTombstone(t *testing.T, values []json.RawMessage, id int64) bool {
	t.Helper()
	for _, value := range values {
		var payload struct {
			ID      int64 `json:"id"`
			Deleted bool  `json:"deleted"`
		}
		if err := json.Unmarshal(value, &payload); err != nil {
			t.Fatalf("decode sync event payload: %v", err)
		}
		if payload.ID == id && payload.Deleted {
			return true
		}
	}
	return false
}

func containsUpsert(t *testing.T, values []json.RawMessage, id int64) bool {
	t.Helper()
	for _, value := range values {
		var payload struct {
			ID      int64 `json:"id"`
			Deleted bool  `json:"deleted"`
		}
		if err := json.Unmarshal(value, &payload); err != nil {
			t.Fatalf("decode sync event payload: %v", err)
		}
		if payload.ID == id && !payload.Deleted {
			return true
		}
	}
	return false
}
