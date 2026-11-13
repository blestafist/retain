package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/notes"
)

type socialTestClock struct{ now time.Time }

func (c socialTestClock) Now() time.Time { return c.now }

type socialFixture struct {
	db           *db.DB
	service      *Service
	clock        socialTestClock
	ownerID      int64
	friendID     int64
	rootID       int64
	childID      int64
	otherID      int64
	otherChildID int64
	rootNote     int64
	childNote    int64
	otherNote    int64
}

func newSocialFixture(t *testing.T) *socialFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "social.db"))
	if err != nil {
		t.Fatalf("db.Open(): %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC)
	clock := socialTestClock{now: now}
	queries := database.Queries()
	owner := createSocialUser(t, queries, "social-owner@example.test", "Owner", now)
	friend := createSocialUser(t, queries, "social-friend@example.test", "Friend", now)
	rootID := createSocialDeck(t, database, owner.ID, nil, "Shared root", now)
	childID := createSocialDeck(t, database, owner.ID, &rootID, "Shared child", now)
	otherID := createSocialDeck(t, database, owner.ID, nil, "Other root", now)
	otherChildID := createSocialDeck(t, database, owner.ID, &otherID, "Other child", now)
	rootNote := createSocialNote(t, queries, owner.ID, rootID, "root term", "translation")
	childNote := createSocialNote(t, queries, owner.ID, childID, "child term", "child translation")
	otherNote := createSocialNote(t, queries, owner.ID, otherChildID, "copy term", "copy translation")
	return &socialFixture{
		db: database, service: NewService(database, clock), clock: clock,
		ownerID: owner.ID, friendID: friend.ID, rootID: rootID, childID: childID,
		otherID: otherID, otherChildID: otherChildID, rootNote: rootNote, childNote: childNote, otherNote: otherNote,
	}
}

func createSocialUser(t *testing.T, queries *gen.Queries, email, name string, now time.Time) gen.User {
	t.Helper()
	user, err := queries.CreateUser(context.Background(), gen.CreateUserParams{
		Email: email, Name: name, PasswordHash: "unused", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	if _, err := queries.CreateUserSettings(context.Background(), user.ID); err != nil {
		t.Fatalf("create settings for %q: %v", email, err)
	}
	return user
}

func createSocialDeck(t *testing.T, database *db.DB, ownerID int64, parentID *int64, name string, now time.Time) int64 {
	t.Helper()
	var parent any
	if parentID != nil {
		parent = *parentID
	}
	result, err := database.SQL.ExecContext(context.Background(), `
		INSERT INTO decks(owner_id, parent_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, ownerID, parent, name, now, now)
	if err != nil {
		t.Fatalf("create deck %q: %v", name, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read deck id: %v", err)
	}
	return id
}

func createSocialNote(t *testing.T, queries *gen.Queries, ownerID, deckID int64, term, translation string) int64 {
	t.Helper()
	note, err := queries.CreateNote(context.Background(), gen.CreateNoteParams{
		Term: term, TermKey: term, Translation: translation,
		Tags: "[]", Alternatives: "[]", Fields: "{}", DeckID: deckID, UserID: ownerID,
	})
	if err != nil {
		t.Fatalf("create note %q: %v", term, err)
	}
	if _, err := queries.CreateCard(context.Background(), gen.CreateCardParams{NoteID: note.ID, UserID: ownerID, Direction: "forward"}); err != nil {
		t.Fatalf("create card for %q: %v", term, err)
	}
	return note.ID
}

func (f *socialFixture) becomeFriends(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := f.service.RequestFriend(ctx, f.ownerID, "SOCIAL-FRIEND@example.test"); err != nil {
		t.Fatalf("RequestFriend(): %v", err)
	}
	requests, err := f.service.Requests(ctx, f.friendID)
	if err != nil || len(requests) != 1 || requests[0].Direction != "incoming" {
		t.Fatalf("incoming requests = %+v, %v; want one incoming request", requests, err)
	}
	if err := f.service.AcceptFriendRequest(ctx, f.friendID, requests[0].ID); err != nil {
		t.Fatalf("AcceptFriendRequest(): %v", err)
	}
}

func TestFriendRequestsArePrivateAndAcceptedExplicitly(t *testing.T) {
	f := newSocialFixture(t)
	ctx := context.Background()
	for _, email := range []string{"missing@example.test", "social-friend@example.test"} {
		if err := f.service.RequestFriend(ctx, f.ownerID, email); err != nil {
			t.Fatalf("RequestFriend(%q): %v", email, err)
		}
	}
	if friends, err := f.service.Friends(ctx, f.ownerID); err != nil || len(friends) != 0 {
		t.Fatalf("friends before acceptance = %+v, %v; want empty", friends, err)
	}
	if err := f.service.RequestFriend(ctx, f.ownerID, "social-friend@example.test"); err != nil {
		t.Fatalf("repeat pending RequestFriend(): %v", err)
	}
	requests, err := f.service.Requests(ctx, f.friendID)
	if err != nil || len(requests) != 1 || requests[0].Email != "social-owner@example.test" {
		t.Fatalf("incoming requests = %+v, %v; want only the sender account", requests, err)
	}
	if err := f.service.AcceptFriendRequest(ctx, f.friendID, requests[0].ID); err != nil {
		t.Fatalf("AcceptFriendRequest(): %v", err)
	}
	if friends, err := f.service.Friends(ctx, f.ownerID); err != nil || len(friends) != 1 || friends[0].ID != f.friendID {
		t.Fatalf("friends after acceptance = %+v, %v; want accepted friend", friends, err)
	}
}

func TestSubscriptionSharesReadOnlySubtreeAndSyncsAccessChanges(t *testing.T) {
	f := newSocialFixture(t)
	f.becomeFriends(t)
	ctx := context.Background()
	share, err := f.service.ShareDeck(ctx, f.ownerID, f.rootID, f.friendID, "subscribe")
	if err != nil {
		t.Fatalf("ShareDeck(subscribe): %v", err)
	}
	if share.Status != "active" || share.Mode != "subscribe" {
		t.Fatalf("subscription = %+v", share)
	}
	if _, err := f.db.Queries().GetReadableDeck(ctx, gen.GetReadableDeckParams{DeckID: f.childID, UserID: f.friendID}); err != nil {
		t.Fatalf("subscriber cannot read descendant deck: %v", err)
	}
	if got, err := f.db.Queries().ListNotesByDeck(ctx, gen.ListNotesByDeckParams{DeckID: f.childID, OwnerID: f.friendID}); err != nil || len(got) != 1 || got[0].ID != f.childNote {
		t.Fatalf("subscriber notes = %+v, %v", got, err)
	}
	if _, err := (notes.NewService(f.db, f.clock)).Update(ctx, f.friendID, f.childNote, notes.PatchInput{}); !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("subscriber edited source note: error = %v, want not found", err)
	}
	if _, err := f.service.ShareDeck(ctx, f.ownerID, f.rootID, f.friendID, "subscribe"); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat subscription error = %v, want conflict", err)
	}

	initialUpserts := countSyncEvents(t, f.db, f.friendID, "note", f.childNote, "upsert")
	if initialUpserts == 0 {
		t.Fatal("initial subscription did not publish the child note snapshot")
	}
	if _, err := (notes.NewService(f.db, f.clock)).Update(ctx, f.ownerID, f.childNote, notes.PatchInput{Translation: stringPtr("updated live")}); err != nil {
		t.Fatalf("owner update shared note: %v", err)
	}
	beforeMove := countSyncEvents(t, f.db, f.friendID, "note", f.childNote, "upsert")
	if beforeMove <= initialUpserts {
		t.Fatal("source note edit did not publish a live subscriber snapshot")
	}
	if _, err := f.db.SQL.ExecContext(ctx, `UPDATE decks SET parent_id = NULL, updated_at = ? WHERE id = ?`, f.clock.now.Add(time.Minute), f.childID); err != nil {
		t.Fatalf("move shared child out of subtree: %v", err)
	}
	if _, err := f.db.Queries().GetReadableDeck(ctx, gen.GetReadableDeckParams{DeckID: f.childID, UserID: f.friendID}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("moved descendant remained readable: %v", err)
	}
	if countSyncEvents(t, f.db, f.friendID, "note", f.childNote, "delete") == 0 {
		t.Fatal("moving a child out did not publish a note tombstone")
	}
	if _, err := f.db.SQL.ExecContext(ctx, `UPDATE decks SET parent_id = ?, updated_at = ? WHERE id = ?`, f.rootID, f.clock.now.Add(2*time.Minute), f.childID); err != nil {
		t.Fatalf("move shared child back into subtree: %v", err)
	}
	if _, err := f.db.Queries().GetReadableDeck(ctx, gen.GetReadableDeckParams{DeckID: f.childID, UserID: f.friendID}); err != nil {
		t.Fatalf("restored subtree access did not return: %v", err)
	}
	if countSyncEvents(t, f.db, f.friendID, "note", f.childNote, "upsert") <= beforeMove {
		t.Fatal("moving a child back did not republish its note")
	}

	if err := f.service.EndSubscription(ctx, f.ownerID, f.rootID, share.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if _, err := f.db.Queries().GetReadableDeck(ctx, gen.GetReadableDeckParams{DeckID: f.rootID, UserID: f.friendID}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked deck remained readable: %v", err)
	}
	if countSyncEvents(t, f.db, f.friendID, "note", f.rootNote, "delete") == 0 {
		t.Fatal("revocation did not publish root note tombstone")
	}
	var cardCount int
	if err := f.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM cards WHERE user_id = ? AND note_id IN (?, ?)`, f.friendID, f.rootNote, f.childNote).Scan(&cardCount); err != nil || cardCount != 2 {
		t.Fatalf("subscriber progress rows after revoke = %d, %v; want retained private rows", cardCount, err)
	}
	if _, err := f.service.ShareDeck(ctx, f.ownerID, f.rootID, f.friendID, "subscribe"); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-share after revocation error = %v, want conflict", err)
	}
}

func TestDeletingSharedDeckPublishesOfflineTombstones(t *testing.T) {
	f := newSocialFixture(t)
	f.becomeFriends(t)
	ctx := context.Background()
	if _, err := f.service.ShareDeck(ctx, f.ownerID, f.rootID, f.friendID, "subscribe"); err != nil {
		t.Fatalf("ShareDeck(subscribe): %v", err)
	}
	if err := decks.NewService(f.db, f.clock).Delete(ctx, f.ownerID, f.rootID); err != nil {
		t.Fatalf("delete shared source subtree: %v", err)
	}
	for _, noteID := range []int64{f.rootNote, f.childNote} {
		if countSyncEvents(t, f.db, f.friendID, "note", noteID, "delete") == 0 {
			t.Errorf("deleted shared note %d did not publish an offline tombstone", noteID)
		}
	}
	var cardIDs []int64
	rows, err := f.db.SQL.QueryContext(ctx, `SELECT id FROM cards WHERE user_id = ? AND note_id IN (?, ?) ORDER BY id`, f.friendID, f.rootNote, f.childNote)
	if err != nil {
		t.Fatalf("query subscriber cards: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			t.Fatalf("scan subscriber card: %v", err)
		}
		cardIDs = append(cardIDs, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close subscriber card rows: %v", err)
	}
	if len(cardIDs) != 2 {
		t.Fatalf("subscriber cards = %v; want one card per note", cardIDs)
	}
	for _, cardID := range cardIDs {
		if countSyncEvents(t, f.db, f.friendID, "card", cardID, "delete") == 0 {
			t.Errorf("deleted shared card %d did not publish an offline tombstone", cardID)
		}
	}
}

func TestCopyShareClonesWholeSubtreeWithFreshProgress(t *testing.T) {
	f := newSocialFixture(t)
	f.becomeFriends(t)
	ctx := context.Background()
	share, err := f.service.ShareDeck(ctx, f.ownerID, f.otherID, f.friendID, "copy")
	if err != nil {
		t.Fatalf("ShareDeck(copy): %v", err)
	}
	if share.CopiedDeckID == nil || share.Status != "complete" {
		t.Fatalf("copy share = %+v", share)
	}
	decksList, err := decks.NewService(f.db, f.clock).List(ctx, f.friendID)
	if err != nil {
		t.Fatalf("list recipient decks: %v", err)
	}
	var copied *decks.Deck
	for i := range decksList {
		if decksList[i].ID == *share.CopiedDeckID {
			copied = &decksList[i]
			break
		}
	}
	if copied == nil || copied.OwnerID != f.friendID || !copied.CanEdit || copied.Counts == nil || copied.Counts.Notes != 1 || len(copied.Children) != 1 {
		t.Fatalf("recipient's copied root not returned as editable independent deck: %+v", copied)
	}
	copyNoteID := int64(0)
	if err := f.db.SQL.QueryRowContext(ctx, `
		SELECT notes.id FROM notes JOIN decks ON decks.id = notes.deck_id
		WHERE decks.owner_id = ? AND decks.source_deck_id = ?`, f.friendID, f.otherChildID).Scan(&copyNoteID); err != nil {
		t.Fatalf("find copied note: %v", err)
	}
	if _, err := (notes.NewService(f.db, f.clock)).Update(ctx, f.friendID, copyNoteID, notes.PatchInput{Translation: stringPtr("edited copy")}); err != nil {
		t.Fatalf("edit independent copy: %v", err)
	}
	if source, err := f.db.Queries().GetNote(ctx, gen.GetNoteParams{ID: f.rootNote, OwnerID: f.ownerID}); err != nil || source.Translation != "translation" {
		t.Fatalf("copy edit changed source note: %+v, %v", source, err)
	}
}

func TestSocialVisibilityOptInAndRemovalRevokesSubscriptions(t *testing.T) {
	f := newSocialFixture(t)
	f.becomeFriends(t)
	ctx := context.Background()
	if _, err := f.service.ShareDeck(ctx, f.ownerID, f.rootID, f.friendID, "subscribe"); err != nil {
		t.Fatalf("ShareDeck(subscribe): %v", err)
	}
	board, err := f.service.Leaderboard(ctx, f.ownerID, "week")
	if err != nil || len(board) != 1 || board[0].UserID != f.ownerID {
		t.Fatalf("leaderboard before opt-in = %+v, %v; want requester only", board, err)
	}
	if _, err := f.service.UpdateSettings(ctx, f.friendID, true); err != nil {
		t.Fatalf("enable social visibility: %v", err)
	}
	board, err = f.service.Leaderboard(ctx, f.ownerID, "week")
	if err != nil || len(board) != 2 {
		t.Fatalf("leaderboard after opt-in = %+v, %v; want both friends", board, err)
	}
	if strings.Contains(strings.ToLower(mustJSON(t, board)), "term") || strings.Contains(strings.ToLower(mustJSON(t, board)), "deck") {
		t.Fatalf("aggregate leaderboard exposed content details: %+v", board)
	}
	if err := f.service.RemoveFriend(ctx, f.ownerID, f.friendID); err != nil {
		t.Fatalf("remove friend: %v", err)
	}
	if _, err := f.db.Queries().GetReadableDeck(ctx, gen.GetReadableDeckParams{DeckID: f.rootID, UserID: f.friendID}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed friend retained subscription access: %v", err)
	}
	if board, err := f.service.Leaderboard(ctx, f.ownerID, "week"); err != nil || len(board) != 1 {
		t.Fatalf("leaderboard after friend removal = %+v, %v", board, err)
	}
}

func countSyncEvents(t *testing.T, database *db.DB, ownerID int64, entityType string, entityID int64, operation string) int {
	t.Helper()
	var count int
	err := database.SQL.QueryRowContext(context.Background(), `
		SELECT count(*) FROM sync_events
		WHERE owner_id = ? AND entity_type = ? AND entity_id = ? AND operation = ?`,
		ownerID, entityType, entityID, operation).Scan(&count)
	if err != nil {
		t.Fatalf("count sync events: %v", err)
	}
	return count
}

func stringPtr(value string) *string { return &value }

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test value: %v", err)
	}
	return string(encoded)
}

var _ clock.Clock = socialTestClock{}
