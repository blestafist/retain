package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/srs"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type studyFixture struct {
	database  *db.DB
	service   *Service
	queries   *gen.Queries
	userID    int64
	otherID   int64
	parentID  int64
	childID   int64
	otherDeck int64
	createdAt time.Time
	noteSeq   int
}

func newStudyFixture(t *testing.T, now time.Time) *studyFixture {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "study.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	queries := database.Queries()
	ctx := context.Background()
	owner, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "owner@example.test", Name: "Owner", PasswordHash: "hash", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserSettings(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	other, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "other@example.test", Name: "Other", PasswordHash: "hash", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserSettings(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	fixture := &studyFixture{
		database: database, queries: queries,
		service: NewService(database, fixedClock{now: now.UTC()}),
		userID:  owner.ID, otherID: other.ID, createdAt: now.UTC(),
	}
	fixture.parentID = fixture.deck(t, owner.ID, "Parent", nil)
	fixture.childID = fixture.deck(t, owner.ID, "Child", &fixture.parentID)
	fixture.otherDeck = fixture.deck(t, owner.ID, "Elsewhere", nil)
	return fixture
}

func (f *studyFixture) deck(t *testing.T, owner int64, name string, parent *int64) int64 {
	t.Helper()
	var parentID any
	if parent != nil {
		parentID = *parent
	}
	result, err := f.database.SQL.ExecContext(context.Background(),
		"INSERT INTO decks(owner_id, parent_id, name) VALUES (?, ?, ?)", owner, parentID, name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *studyFixture) card(t *testing.T, ownerID, deckID int64, state srs.State, due time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	f.noteSeq++
	term := fmt.Sprintf("term-%d", f.noteSeq)
	note, err := f.database.SQL.ExecContext(ctx,
		"INSERT INTO notes(deck_id, term, term_key, translation) VALUES (?, ?, ?, ?)",
		deckID, term, term, "translation")
	if err != nil {
		t.Fatal(err)
	}
	noteID, err := note.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	card, err := f.database.SQL.ExecContext(ctx,
		`INSERT INTO cards(note_id, user_id, direction, state, due, stability, difficulty,
		 elapsed_days, scheduled_days, last_review)
		 VALUES (?, ?, 'forward', ?, ?, ?, ?, 0, 0, ?)`,
		noteID, ownerID, int64(state), due.UTC(),
		nullableScheduleValue(state, 5), nullableScheduleValue(state, 5), nullableScheduleTime(state, due.Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	id, err := card.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func nullableScheduleValue(state srs.State, value float64) any {
	if state == srs.New {
		return nil
	}
	return value
}

func nullableScheduleTime(state srs.State, value time.Time) any {
	if state == srs.New {
		return nil
	}
	return value.UTC()
}

func (f *studyFixture) reviewLog(t *testing.T, id string, cardID int64, state srs.State, reviewedAt time.Time) {
	t.Helper()
	_, err := f.database.SQL.ExecContext(context.Background(), `
		INSERT INTO review_logs (
		 id, card_id, user_id, rating, mode, state_before, stability_before, difficulty_before,
		 stability_after, difficulty_after, scheduled_days, reviewed_at, received_at, duration_ms
		) VALUES (?, ?, ?, 3, 'flip', ?, NULL, NULL, 1, 5, 0, ?, ?, 0)`,
		id, cardID, f.userID, int64(state), reviewedAt.UTC(), f.createdAt)
	if err != nil {
		t.Fatal(err)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestLateReviewRebuildsDailyLimitProjectionFromActiveHistory(t *testing.T) {
	now := time.Date(2025, time.January, 20, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	cardID := f.card(t, f.userID, f.childID, srs.New, now.Add(-24*time.Hour))
	laterAt := now.Add(-20 * time.Hour)
	if _, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: "0194a9bd-62a0-7abc-8def-0123456789ab", CardID: cardID,
		Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt, DurationMS: 500,
	}); err != nil {
		t.Fatalf("Review(later event) error = %v", err)
	}
	dayStart, dayEnd, err := userDayBounds(laterAt, time.UTC, 4)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := f.queries.GetDailyReviewCounts(context.Background(), gen.GetDailyReviewCountsParams{
		UserID: f.userID, ReviewedAt: dayStart, ReviewedAt2: dayEnd,
	})
	if err != nil || counts.NewCount != 1 {
		t.Fatalf("daily new count before late event = %+v, %v; want one", counts, err)
	}

	if _, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: "0194a9bd-62a0-7abc-8def-0123456789ac", CardID: cardID,
		Rating: srs.Again, Mode: "flip", ReviewedAt: now.Add(-48 * time.Hour), DurationMS: 600,
	}); err != nil {
		t.Fatalf("Review(late earlier event) error = %v", err)
	}
	counts, err = f.queries.GetDailyReviewCounts(context.Background(), gen.GetDailyReviewCountsParams{
		UserID: f.userID, ReviewedAt: dayStart, ReviewedAt2: dayEnd,
	})
	if err != nil || counts.NewCount != 0 {
		t.Fatalf("daily new count after late event = %+v, %v; want zero because this event now follows Learning", counts, err)
	}
	var laterStateBefore int64
	if err := f.database.SQL.QueryRowContext(context.Background(), `
		SELECT projection.state_before
		FROM review_log_projection AS projection
		JOIN review_logs AS logs ON logs.id = projection.review_id
		WHERE logs.id = '0194a9bd-62a0-7abc-8def-0123456789ab'`).Scan(&laterStateBefore); err != nil {
		t.Fatalf("read rebuilt event projection: %v", err)
	}
	if laterStateBefore != int64(srs.Learning) {
		t.Fatalf("later event derived state_before = %d, want Learning after replay", laterStateBefore)
	}
	if _, err := f.service.Undo(context.Background(), f.userID, "0194a9bd-62a0-7abc-8def-0123456789ac"); err != nil {
		t.Fatalf("Undo(earlier event) error = %v", err)
	}
	counts, err = f.queries.GetDailyReviewCounts(context.Background(), gen.GetDailyReviewCountsParams{
		UserID: f.userID, ReviewedAt: dayStart, ReviewedAt2: dayEnd,
	})
	if err != nil || counts.NewCount != 1 {
		t.Fatalf("daily new count after undo = %+v, %v; want restored single new event", counts, err)
	}
}

func TestReviewReplayUsesTheSchedulerParametersSnapshottedPerEvent(t *testing.T) {
	now := time.Date(2025, time.February, 3, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	cardID := f.card(t, f.userID, f.childID, srs.New, now.Add(-24*time.Hour))
	firstAt := now.Add(-48 * time.Hour)
	if _, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: "0194a9bd-62a0-7abc-8def-0123456789ad", CardID: cardID,
		Rating: srs.Good, Mode: "flip", ReviewedAt: firstAt, DurationMS: 400,
	}); err != nil {
		t.Fatalf("Review(first event) error = %v", err)
	}
	if _, err := f.database.SQL.ExecContext(context.Background(), `
		UPDATE user_settings
		SET request_retention = 0.75, learning_steps = '2m 20m', relearning_steps = '20m'
		WHERE user_id = ?`, f.userID); err != nil {
		t.Fatalf("change scheduling settings: %v", err)
	}
	secondAt := now.Add(-24 * time.Hour)
	if _, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: "0194a9bd-62a0-7abc-8def-0123456789ae", CardID: cardID,
		Rating: srs.Good, Mode: "flip", ReviewedAt: secondAt, DurationMS: 450,
	}); err != nil {
		t.Fatalf("Review(second event) error = %v", err)
	}

	rows, err := f.database.SQL.QueryContext(context.Background(), `
		SELECT scheduler_parameters FROM review_logs WHERE card_id = ? ORDER BY reviewed_at, id`, cardID)
	if err != nil {
		t.Fatal(err)
	}
	var parameters []srs.Parameters
	for rows.Next() {
		var serialized string
		if err := rows.Scan(&serialized); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		var current srs.Parameters
		if err := json.Unmarshal([]byte(serialized), &current); err != nil {
			_ = rows.Close()
			t.Fatalf("decode scheduler snapshot: %v", err)
		}
		parameters = append(parameters, current)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(parameters) != 2 || parameters[0].RequestRetention != 0.9 || parameters[1].RequestRetention != 0.75 ||
		len(parameters[0].LearningSteps) != 2 || parameters[0].LearningSteps[0] != time.Minute ||
		len(parameters[1].LearningSteps) != 2 || parameters[1].LearningSteps[0] != 2*time.Minute {
		t.Fatalf("per-event scheduler snapshots = %+v, want old defaults followed by new settings", parameters)
	}

	settings, err := f.queries.GetUserSettings(context.Background(), f.userID)
	if err != nil {
		t.Fatal(err)
	}
	options, err := optionsFromSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := f.queries.ListActiveReviewLogs(context.Background(), gen.ListActiveReviewLogsParams{CardID: cardID, UserID: f.userID})
	if err != nil {
		t.Fatal(err)
	}
	replayed, _, ok := replay(logs, "", options)
	if !ok {
		t.Fatal("replay() rejected valid per-event scheduler snapshots")
	}
	var storedState int64
	var storedStability sql.NullFloat64
	if err := f.database.SQL.QueryRowContext(context.Background(),
		"SELECT state, stability FROM cards WHERE id = ?", cardID).Scan(&storedState, &storedStability); err != nil {
		t.Fatal(err)
	}
	if storedState != int64(replayed.State) || !storedStability.Valid || storedStability.Float64 != replayed.Stability {
		t.Fatalf("persisted card = state %d stability %v, replay = state %d stability %v", storedState, storedStability, replayed.State, replayed.Stability)
	}
}

func TestQueueRespectsNestedDeckOrderLimitsAndLocalDay(t *testing.T) {
	// At 03:00 in Los Angeles the user's study day still starts on the previous date.
	now := time.Date(2025, time.May, 10, 10, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	if _, err := f.database.SQL.Exec(`UPDATE user_settings SET timezone = 'America/Los_Angeles', day_start_hour = 4, new_per_day = 1, max_reviews_per_day = 1 WHERE user_id = ?`, f.userID); err != nil {
		t.Fatal(err)
	}
	reviewCard := f.card(t, f.userID, f.childID, srs.Review, now.Add(-time.Hour))
	learningCard := f.card(t, f.userID, f.parentID, srs.Learning, now.Add(-time.Minute))
	newCard := f.card(t, f.userID, f.childID, srs.New, now.Add(-24*time.Hour))
	outsideCard := f.card(t, f.userID, f.otherDeck, srs.Learning, now.Add(-time.Minute))
	f.reviewLog(t, "018f0f3e-7b00-7000-8000-000000000001", reviewCard, srs.Review, time.Date(2025, time.May, 9, 11, 30, 0, 0, time.UTC))
	// 10:30Z is 03:30 local, before the 04:00 study-day boundary.
	f.reviewLog(t, "018f0f3e-7b00-7000-8000-000000000002", newCard, srs.New, time.Date(2025, time.May, 9, 10, 30, 0, 0, time.UTC))

	deckID := f.parentID
	if _, err := f.database.SQL.Exec("UPDATE user_settings SET max_reviews_per_day = 10 WHERE user_id = ?", f.userID); err != nil {
		t.Fatal(err)
	}
	queue, err := f.service.Queue(context.Background(), f.userID, &deckID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 3 || queue[0].ID != reviewCard || queue[1].ID != learningCard || queue[2].ID != newCard {
		t.Fatalf("queue priority = %#v, want Review %d, Learning %d, New %d", queue, reviewCard, learningCard, newCard)
	}
	if _, err := f.database.SQL.Exec("UPDATE user_settings SET max_reviews_per_day = 1 WHERE user_id = ?", f.userID); err != nil {
		t.Fatal(err)
	}
	queue, err = f.service.Queue(context.Background(), f.userID, &deckID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 {
		t.Fatalf("Queue returned %d cards, want learning + new after review limit", len(queue))
	}
	if queue[0].ID != learningCard || queue[1].ID != newCard {
		t.Errorf("queue order/contents = [%d, %d], want learning %d then new %d", queue[0].ID, queue[1].ID, learningCard, newCard)
	}
	for _, item := range queue {
		if item.ID == reviewCard || item.ID == outsideCard {
			t.Errorf("queue unexpectedly included card %d", item.ID)
		}
		if len(item.Previews) != 4 {
			t.Errorf("card %d has %d previews, want 4", item.ID, len(item.Previews))
		}
	}

	// A new review inside the user's day consumes the new-card allowance.
	f.reviewLog(t, "018f0f3e-7b00-7000-8000-000000000003", newCard, srs.New, time.Date(2025, time.May, 9, 12, 0, 0, 0, time.UTC))
	queue, err = f.service.Queue(context.Background(), f.userID, &deckID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].ID != learningCard {
		t.Errorf("after both daily caps, queue = %#v, want only learning card %d", queue, learningCard)
	}

	if _, err := f.service.Queue(context.Background(), f.userID, int64Pointer(f.otherDeck), 10); err != nil {
		t.Fatalf("owned deck queue: %v", err)
	}
	if _, err := f.service.Queue(context.Background(), f.otherID, &deckID, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("Queue for another user's deck error = %v, want ErrNotFound", err)
	}
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestUserDayBoundsTimezoneAndDST(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, time.March, 9, 10, 30, 0, 0, time.UTC)
	start, end, err := userDayBounds(now, location, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2025, time.March, 8, 12, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Errorf("day start = %s, want %s", start, want)
	}
	if want := time.Date(2025, time.March, 9, 11, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Errorf("next day = %s, want %s", end, want)
	}
	if got := end.Sub(start); got != 23*time.Hour {
		t.Errorf("DST study day length = %s, want 23h", got)
	}
}

func TestReviewIdempotencyOutOfOrderReplayUndoAndAccess(t *testing.T) {
	now := time.Date(2025, time.January, 5, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	cardID := f.card(t, f.userID, f.childID, srs.New, now)
	otherCardID := f.card(t, f.otherID, f.deck(t, f.otherID, "Other owner", nil), srs.New, now)
	firstAt := time.Date(2025, time.January, 2, 12, 0, 0, 0, time.UTC)
	laterAt := firstAt.Add(24 * time.Hour)
	laterID := "018f0f3e-7b00-7000-8000-000000000011"
	earlierID := "018f0f3e-7b00-7000-8000-000000000012"

	first, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: laterID, CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt, DurationMS: 1200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.State != srs.Learning {
		t.Fatalf("first review state = %d, want Learning", first.State)
	}
	// This earlier timestamp is inserted after the later answer. Replay sorts it first.
	second, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: earlierID, CardID: cardID, Rating: srs.Easy, Mode: "flip", ReviewedAt: firstAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.State != srs.Review {
		t.Fatalf("out-of-order replay state = %d, want Review", second.State)
	}

	duplicate, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: laterID, CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt, DurationMS: 1200,
	})
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if duplicate.State != second.State || duplicate.Due != second.Due {
		t.Errorf("idempotent retry changed card: got %#v, want %#v", duplicate, second)
	}
	// The event timestamp is an instant, so a retry in another location is
	// identical; changing any immutable event field while reusing the UUID is
	// a conflict.
	zone := time.FixedZone("UTC+02", 2*60*60)
	if _, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: laterID, CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt.In(zone), DurationMS: 1200,
	}); err != nil {
		t.Errorf("retry with equivalent UTC timestamp: %v", err)
	}
	for _, test := range []struct {
		name   string
		userID int64
		input  ReviewInput
	}{
		{name: "user", userID: f.otherID, input: ReviewInput{ID: laterID, CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt, DurationMS: 1200}},
		{name: "card", userID: f.userID, input: ReviewInput{ID: laterID, CardID: cardID + 1, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt, DurationMS: 1200}},
		{name: "rating", userID: f.userID, input: ReviewInput{ID: laterID, CardID: cardID, Rating: srs.Hard, Mode: "flip", ReviewedAt: laterAt, DurationMS: 1200}},
		{name: "reviewed_at", userID: f.userID, input: ReviewInput{ID: laterID, CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt.Add(time.Nanosecond), DurationMS: 1200}},
		{name: "duration_ms", userID: f.userID, input: ReviewInput{ID: laterID, CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: laterAt, DurationMS: 1201}},
	} {
		t.Run("duplicate mismatch "+test.name, func(t *testing.T) {
			if _, err := f.service.Review(context.Background(), test.userID, test.input); !errors.Is(err, ErrConflict) {
				t.Errorf("duplicate with mismatched %s error = %v, want ErrConflict", test.name, err)
			}
		})
	}
	var logCount int
	if err := f.database.SQL.QueryRow("SELECT COUNT(*) FROM review_logs WHERE card_id = ?", cardID).Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if logCount != 2 {
		t.Fatalf("log count after retry = %d, want 2", logCount)
	}

	if _, err := f.service.Review(context.Background(), f.otherID, ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000013", CardID: cardID, Rating: srs.Good,
		Mode: "flip", ReviewedAt: now,
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user review error = %v, want ErrNotFound", err)
	}
	if _, err := f.service.Undo(context.Background(), f.otherID, laterID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user undo error = %v, want ErrNotFound", err)
	}
	if _, err := f.service.Review(context.Background(), f.otherID, ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000015", CardID: otherCardID, Rating: srs.Good,
		Mode: "arcade", ReviewedAt: now,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unsupported mode error = %v, want ErrInvalidInput", err)
	}

	afterUndo, err := f.service.Undo(context.Background(), f.userID, earlierID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUndo.State != srs.Learning {
		t.Errorf("state after undoing earlier log = %d, want Learning after replay", afterUndo.State)
	}
	reset, err := f.service.Undo(context.Background(), f.userID, laterID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.State != srs.New || reset.Stability != nil || reset.Difficulty != nil {
		t.Errorf("state after undoing all reviews = %#v, want a clean New card", reset)
	}
	if _, err := f.service.Undo(context.Background(), f.userID, laterID); err != nil {
		t.Errorf("repeated undo should be idempotent: %v", err)
	}

	for _, statement := range []string{
		"DELETE FROM review_logs WHERE id = ?",
		"UPDATE review_logs SET rating = 2 WHERE id = ?",
	} {
		if _, err := f.database.SQL.Exec(statement, laterID); err == nil {
			t.Errorf("append-only trigger allowed %q", statement)
		}
	}
	var total, undone int
	if err := f.database.SQL.QueryRow("SELECT COUNT(*), SUM(CASE WHEN undone = 1 THEN 1 ELSE 0 END) FROM review_logs WHERE card_id = ?", cardID).Scan(&total, &undone); err != nil {
		t.Fatal(err)
	}
	if total != 2 || undone != 2 {
		t.Errorf("logs after undo = total %d, undone %d; want 2, 2", total, undone)
	}
}

func TestReviewRejectsMalformedUUIDRatingAndMode(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	cardID := f.card(t, f.userID, f.childID, srs.New, now)
	valid := ReviewInput{ID: "018f0f3e-7b00-7000-8000-000000000021", CardID: cardID, Rating: srs.Good, Mode: "flip", ReviewedAt: now}
	tests := []struct {
		name string
		edit func(*ReviewInput)
	}{
		{name: "uuid version", edit: func(input *ReviewInput) { input.ID = "018f0f3e-7b00-4000-8000-000000000021" }},
		{name: "uuid variant", edit: func(input *ReviewInput) { input.ID = "018f0f3e-7b00-7000-0000-000000000021" }},
		{name: "invalid rating", edit: func(input *ReviewInput) { input.Rating = 5 }},
		{name: "invalid mode", edit: func(input *ReviewInput) { input.Mode = "unsupported" }},
		{name: "missing timestamp", edit: func(input *ReviewInput) { input.ReviewedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.edit(&input)
			if _, err := f.service.Review(context.Background(), f.userID, input); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Review error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestMigrationReviewLogConstraints(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	cardID := f.card(t, f.userID, f.childID, srs.New, now)
	_, err := f.database.SQL.Exec(`INSERT INTO review_logs (
		id, card_id, user_id, rating, mode, state_before, stability_before, difficulty_before,
		stability_after, difficulty_after, scheduled_days, reviewed_at, received_at
	) VALUES ('018f0f3e-7b00-4000-8000-000000000031', ?, ?, 3, 'flip', 0, NULL, NULL, 1, 5, 0, ?, ?)`, cardID, f.userID, now, now)
	if err == nil {
		t.Fatal("review_logs accepted a UUID that is not version 7")
	}
	_, err = f.database.SQL.Exec(`INSERT INTO review_logs (
		id, card_id, user_id, rating, mode, state_before, stability_before, difficulty_before,
		stability_after, difficulty_after, scheduled_days, reviewed_at, received_at
	) VALUES ('018f0f3e-7b00-7000-8000-000000000032', ?, ?, 5, 'flip', 0, NULL, NULL, 1, 5, 0, ?, ?)`, cardID, f.userID, now, now)
	if err == nil {
		t.Fatal("review_logs accepted an invalid rating")
	}
	_, err = f.database.SQL.Exec(`INSERT INTO review_logs (
		id, card_id, user_id, rating, mode, state_before, stability_before, difficulty_before,
		stability_after, difficulty_after, scheduled_days, reviewed_at, received_at
	) VALUES ('018f0f3e-7b00-7000-8000-000000000033', ?, ?, 3, 'flip', 0, NULL, NULL, 1, 5, 0, ?, ?)`, cardID, f.otherID, now, now)
	if err == nil {
		t.Fatal("review_logs accepted a user/card mismatch")
	}
}

func TestReviewUsesThreeCustomLearningStepsAndCustomRelearningSteps(t *testing.T) {
	start := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, start.Add(2*time.Hour))
	if _, err := f.database.SQL.Exec(`UPDATE user_settings
		SET learning_steps = '2m 8m 30m', relearning_steps = '3m 12m' WHERE user_id = ?`, f.userID); err != nil {
		t.Fatal(err)
	}
	cardID := f.card(t, f.userID, f.childID, srs.New, start)
	review := func(id string, rating srs.Rating, at time.Time) Card {
		t.Helper()
		card, err := f.service.Review(context.Background(), f.userID, ReviewInput{
			ID: id, CardID: cardID, Rating: rating, Mode: "flip", ReviewedAt: at,
		})
		if err != nil {
			t.Fatalf("Review(%d at %s): %v", rating, at, err)
		}
		return card
	}

	first := review("018f0f3e-7b00-7000-8000-000000000051", srs.Good, start)
	if first.State != srs.Learning || first.Step != 1 || !first.Due.Equal(start.Add(8*time.Minute)) {
		t.Fatalf("New Good = state %d step %d due %s, want Learning step 1 at +8m", first.State, first.Step, first.Due)
	}
	secondAt := start.Add(8 * time.Minute)
	second := review("018f0f3e-7b00-7000-8000-000000000052", srs.Good, secondAt)
	if second.State != srs.Learning || second.Step != 2 || !second.Due.Equal(secondAt.Add(30*time.Minute)) {
		t.Fatalf("step-1 Good = state %d step %d due %s, want Learning step 2 at +30m", second.State, second.Step, second.Due)
	}
	thirdAt := secondAt.Add(30 * time.Minute)
	third := review("018f0f3e-7b00-7000-8000-000000000053", srs.Good, thirdAt)
	if third.State != srs.Review || third.Step != 0 {
		t.Fatalf("step-2 Good = state %d step %d, want graduated Review", third.State, third.Step)
	}

	againAt := thirdAt.Add(time.Hour)
	again := review("018f0f3e-7b00-7000-8000-000000000054", srs.Again, againAt)
	if again.State != srs.Relearning || again.Step != 0 || !again.Due.Equal(againAt.Add(3*time.Minute)) {
		t.Fatalf("Review Again = state %d step %d due %s, want Relearning step 0 at +3m", again.State, again.Step, again.Due)
	}
	relearnAt := againAt.Add(3 * time.Minute)
	relearn := review("018f0f3e-7b00-7000-8000-000000000055", srs.Good, relearnAt)
	if relearn.State != srs.Relearning || relearn.Step != 1 || !relearn.Due.Equal(relearnAt.Add(12*time.Minute)) {
		t.Fatalf("relearning step-0 Good = state %d step %d due %s, want step 1 at +12m", relearn.State, relearn.Step, relearn.Due)
	}
	graduated := review("018f0f3e-7b00-7000-8000-000000000056", srs.Good, relearnAt.Add(12*time.Minute))
	if graduated.State != srs.Review || graduated.Step != 0 {
		t.Errorf("relearning final Good = state %d step %d, want Review", graduated.State, graduated.Step)
	}
}

func TestQueuePreviewsUsePersistedLearningAndRelearningSteps(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	if _, err := f.database.SQL.Exec(`UPDATE user_settings
		SET learning_steps = '30s 2m', relearning_steps = '45s' WHERE user_id = ?`, f.userID); err != nil {
		t.Fatal(err)
	}
	newCard := f.card(t, f.userID, f.childID, srs.New, now)
	reviewCard := f.card(t, f.userID, f.childID, srs.Review, now.Add(-time.Minute))
	queue, err := f.service.Queue(context.Background(), f.userID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]QueueCard, len(queue))
	for _, item := range queue {
		byID[item.ID] = item
	}
	if preview := byID[newCard].Previews[srs.Good]; preview.State != srs.Learning || preview.Interval != "2m0s" {
		t.Errorf("New Good preview = %#v, want 2m learning step", preview)
	}
	if preview := byID[reviewCard].Previews[srs.Again]; preview.State != srs.Relearning || preview.Interval != "45s" {
		t.Errorf("Review Again preview = %#v, want 45s relearning step", preview)
	}
}

func TestInvalidPersistedSettingsFailClosed(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	f := newStudyFixture(t, now)
	cardID := f.card(t, f.userID, f.childID, srs.New, now)
	if _, err := f.database.SQL.Exec("UPDATE user_settings SET timezone = 'not/a-timezone' WHERE user_id = ?", f.userID); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Queue(context.Background(), f.userID, nil, 10)
	if !errors.Is(err, ErrInternal) {
		t.Errorf("Queue with invalid stored timezone = %v, want ErrInternal", err)
	}
	if _, err := f.service.Review(context.Background(), f.userID, ReviewInput{
		ID: "018f0f3e-7b00-7000-8000-000000000041", CardID: cardID, Rating: srs.Good,
		Mode: "flip", ReviewedAt: now,
	}); !errors.Is(err, ErrInternal) {
		t.Errorf("Review with invalid persisted settings = %v, want ErrInternal", err)
	}
}
