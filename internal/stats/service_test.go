package stats

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/srs"
)

type statsTestClock struct{ now time.Time }

func (c *statsTestClock) Now() time.Time { return c.now }

type statsFixture struct {
	database    *db.DB
	service     *Service
	clock       *statsTestClock
	userID      int64
	otherID     int64
	deckID      int64
	otherDeckID int64
	cards       []int64
}

func newStatsFixture(t *testing.T) *statsFixture {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})

	clock := &statsTestClock{now: time.Date(2026, time.April, 3, 10, 59, 0, 0, time.UTC)}
	queries := database.Queries()
	owner, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "stats-owner@example.test", Name: "Stats owner", PasswordHash: "unused", CreatedAt: clock.now,
	})
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	if _, err := queries.CreateUserSettings(ctx, owner.ID); err != nil {
		t.Fatalf("CreateUserSettings(owner) error = %v", err)
	}
	other, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email: "stats-other@example.test", Name: "Other", PasswordHash: "unused", CreatedAt: clock.now,
	})
	if err != nil {
		t.Fatalf("CreateUser(other) error = %v", err)
	}
	if _, err := queries.CreateUserSettings(ctx, other.ID); err != nil {
		t.Fatalf("CreateUserSettings(other) error = %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx,
		`UPDATE user_settings SET timezone = 'America/Los_Angeles', day_start_hour = 4, request_retention = 0.9 WHERE user_id = ?`, owner.ID,
	); err != nil {
		t.Fatalf("set owner settings: %v", err)
	}
	result, err := database.SQL.ExecContext(ctx, `
		INSERT INTO decks (owner_id, name, lang_target, lang_native, created_at, updated_at)
		VALUES (?, 'Stats parent', 'de', 'pl', ?, ?)
	`, owner.ID, clock.now, clock.now)
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	parentDeckID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read parent deck id: %v", err)
	}
	result, err = database.SQL.ExecContext(ctx, `
		INSERT INTO decks (owner_id, parent_id, name, lang_target, lang_native, created_at, updated_at)
		VALUES (?, ?, 'Stats child', NULL, NULL, ?, ?)
	`, owner.ID, parentDeckID, clock.now, clock.now)
	if err != nil {
		t.Fatalf("create child deck: %v", err)
	}
	deckID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read child deck id: %v", err)
	}
	otherDeckResult, err := database.SQL.ExecContext(ctx, `
		INSERT INTO decks (owner_id, name, lang_target, lang_native, created_at, updated_at)
		VALUES (?, 'Other deck', 'en', 'ru', ?, ?)
	`, other.ID, clock.now, clock.now)
	if err != nil {
		t.Fatalf("create other deck: %v", err)
	}
	otherDeckID, err := otherDeckResult.LastInsertId()
	if err != nil {
		t.Fatalf("read other deck id: %v", err)
	}
	fixture := &statsFixture{
		database: database, clock: clock, userID: owner.ID, otherID: other.ID, deckID: deckID,
		otherDeckID: otherDeckID, service: NewService(database, clock),
	}
	fixture.cards = []int64{
		fixture.addCard(t, owner.ID, 1, "gehen", "iść", "forward", int64(srs.Review), clock.now.Add(-time.Hour), 30, 5, clock.now.Add(-48*time.Hour), 9, false, false),
		fixture.addCard(t, owner.ID, 2, "Haus", "dom", "forward", int64(srs.New), clock.now, 0, 0, time.Time{}, 0, false, false),
		fixture.addCard(t, owner.ID, 3, "laufen", "biegać", "forward", int64(srs.Learning), clock.now.Add(48*time.Hour), 0.5, 5, clock.now.Add(-24*time.Hour), 0, false, false),
		fixture.addCard(t, owner.ID, 4, "sprechen", "mówić", "forward", int64(srs.Relearning), clock.now.Add(72*time.Hour), 10, 7, clock.now.Add(-24*time.Hour), 2, false, false),
		fixture.addCard(t, other.ID, 5, "secret", "secret", "forward", int64(srs.Review), clock.now, 50, 5, clock.now.Add(-24*time.Hour), 10, true, false),
	}
	fixture.addLog(t, fixture.cards[0], owner.ID, 1, 3, int64(srs.Review), 30, 5, time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC), 700)
	fixture.addLog(t, fixture.cards[0], owner.ID, 2, 3, int64(srs.Review), 32, 5, time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC), 1000)
	fixture.addLog(t, fixture.cards[0], owner.ID, 3, 1, int64(srs.Review), 35, 6, time.Date(2026, time.April, 3, 10, 30, 0, 0, time.UTC), 2000)
	fixture.addLog(t, fixture.cards[0], owner.ID, 4, 3, int64(srs.Review), 40, 5, time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC), 5000)
	if _, err := database.SQL.ExecContext(ctx, `UPDATE review_logs SET undone = 1 WHERE id = ?`, testLogID(4)); err != nil {
		t.Fatalf("undo test review: %v", err)
	}
	fixture.addLog(t, fixture.cards[0], owner.ID, 5, 2, 0, 1, 4, time.Date(2026, time.April, 3, 10, 45, 0, 0, time.UTC), 3000)
	fixture.addLog(t, fixture.cards[4], other.ID, 6, 3, int64(srs.Review), 50, 5, time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC), 9000)
	return fixture
}

func (f *statsFixture) addCard(t *testing.T, userID int64, noteID int64, term, translation, direction string, state int64, due time.Time, stability, difficulty float64, lastReview time.Time, lapses int64, leech, suspended bool) int64 {
	t.Helper()
	ctx := context.Background()
	deckID := f.deckID
	if userID == f.otherID {
		deckID = f.otherDeckID
	}
	result, err := f.database.SQL.ExecContext(ctx, `
		INSERT INTO notes (id, deck_id, term, term_key, translation, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, noteID, deckID, term, term, translation, f.clock.now, f.clock.now)
	if err != nil {
		t.Fatalf("create note %q: %v", term, err)
	}
	_ = result
	var stable, diff, last any
	if state != int64(srs.New) {
		stable, diff = stability, difficulty
		if !lastReview.IsZero() {
			last = lastReview
		}
	}
	result, err = f.database.SQL.ExecContext(ctx, `
		INSERT INTO cards (
			note_id, user_id, direction, state, due, stability, difficulty, elapsed_days,
			scheduled_days, step, reps, lapses, last_review, suspended, is_leech, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?, ?, ?, ?, ?)
	`, noteID, userID, direction, state, due, stable, diff, state, lapses, last, suspended, leech, f.clock.now)
	if err != nil {
		t.Fatalf("create card %q: %v", term, err)
	}
	cardID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read card id: %v", err)
	}
	return cardID
}

func (f *statsFixture) addLog(t *testing.T, cardID, userID int64, sequence int, rating int64, stateBefore int64, stability, difficulty float64, reviewedAt time.Time, duration int64) {
	t.Helper()
	stabilityBefore, difficultyBefore := any(nil), any(nil)
	if stateBefore != int64(srs.New) {
		stabilityBefore, difficultyBefore = stability, difficulty
	}
	_, err := f.database.SQL.ExecContext(context.Background(), `
		INSERT INTO review_logs (
			id, card_id, user_id, rating, mode, state_before, stability_before,
			difficulty_before, stability_after, difficulty_after, scheduled_days,
			reviewed_at, received_at, duration_ms
		) VALUES (?, ?, ?, ?, 'flip', ?, ?, ?, ?, ?, 1, ?, ?, ?)
	`, testLogID(sequence), cardID, userID, rating, stateBefore, stabilityBefore, difficultyBefore,
		stability, difficulty, reviewedAt, reviewedAt, duration)
	if err != nil {
		t.Fatalf("insert log %d: %v", sequence, err)
	}
}

func testLogID(sequence int) string {
	return fmt.Sprintf("10000000-0000-7000-8000-%012d", sequence)
}

func TestOverviewUsesUserDayBoundsExcludesUndoneAndScopesOwners(t *testing.T) {
	f := newStatsFixture(t)
	got, err := f.service.Overview(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if got.Timezone != "America/Los_Angeles" || got.DayStartHour != 4 {
		t.Fatalf("user date settings = %s/%d", got.Timezone, got.DayStartHour)
	}
	if got.Today.Reviews != 3 || got.Today.NewCards != 1 || got.Today.ReviewCards != 2 || got.Today.RecalledReviews != 1 || got.Today.DurationMs != 6000 {
		t.Errorf("today = %+v, want active owner logs inside local day only", got.Today)
	}
	if got.Today.Retention != 0.5 {
		t.Errorf("today retention = %v, want 0.5", got.Today.Retention)
	}
	if got.Cards.Total != 4 || got.Cards.New != 1 || got.Cards.Learning != 1 || got.Cards.Review != 1 || got.Cards.Relearning != 1 || got.Cards.Due != 1 || got.Cards.Leeches != 1 {
		t.Errorf("cards = %+v, want owner-only totals", got.Cards)
	}
	if got.Reviews.Total != 4 || got.Reviews.TotalDurationMs != 6700 || got.Reviews.AverageDurationMs != 1675 {
		t.Errorf("review totals = %+v, want four active owner logs", got.Reviews)
	}
	if got.KnownRightNow <= 0 || got.KnownRightNow >= 1 {
		t.Errorf("known_right_now = %v, want one review card's recall probability", got.KnownRightNow)
	}
	other, err := f.service.Overview(context.Background(), f.otherID)
	if err != nil {
		t.Fatalf("Overview(other user) error = %v", err)
	}
	if other.Cards.Total != 1 || other.Reviews.Total != 1 {
		t.Errorf("other user's cached overview = cards:%d reviews:%d, want only that user's data", other.Cards.Total, other.Reviews.Total)
	}
}

func TestFutureReviewTimestampsAreExcludedFromAnalytics(t *testing.T) {
	f := newStatsFixture(t)
	f.clock.now = time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC)

	overview, err := f.service.Overview(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if overview.Today.Reviews != 1 || overview.Today.DurationMs != 1000 || overview.Reviews.Total != 2 || overview.Reviews.TotalDurationMs != 1700 {
		t.Errorf("overview counted future review timestamps: today=%+v totals=%+v", overview.Today, overview.Reviews)
	}

	retention, err := f.service.Retention(context.Background(), f.userID, "week")
	if err != nil {
		t.Fatalf("Retention() error = %v", err)
	}
	lastRetention := retention.Points[len(retention.Points)-1]
	if lastRetention.ReviewCards != 2 || lastRetention.RecalledReviews != 2 {
		t.Errorf("current retention counted future reviews: %+v", lastRetention)
	}

	heatmap, err := f.service.Heatmap(context.Background(), f.userID, 2026)
	if err != nil {
		t.Fatalf("Heatmap() error = %v", err)
	}
	if got := activityForDate(t, heatmap.Days, "2026-04-02"); got.Reviews != 1 {
		t.Errorf("study-day activity = %+v, want only the review up to now", got)
	}
	if got := activityForDate(t, heatmap.Days, "2026-04-03"); got.Reviews != 0 {
		t.Errorf("future reviews appeared in the next study day: %+v", got)
	}

	curve, err := f.service.Curve(context.Background(), f.userID, f.cards[0])
	if err != nil {
		t.Fatalf("Curve() error = %v", err)
	}
	if len(curve.Reviews) != 2 {
		t.Errorf("curve reviews = %d, want future events excluded", len(curve.Reviews))
	}
}

func TestHeatmapRetentionAndGrowthUseLocalBucketsAndExcludeUndone(t *testing.T) {
	f := newStatsFixture(t)
	heatmap, err := f.service.Heatmap(context.Background(), f.userID, 2026)
	if err != nil {
		t.Fatalf("Heatmap() error = %v", err)
	}
	if len(heatmap.Days) != 365 {
		t.Fatalf("heatmap days = %d, want 365", len(heatmap.Days))
	}
	var apr2 ActivityDay
	for _, day := range heatmap.Days {
		if day.Date == "2026-04-02" {
			apr2 = day
			break
		}
	}
	if apr2.Reviews != 3 || apr2.DurationMs != 6000 {
		t.Errorf("local April 2 activity = %+v, want 3 active owner reviews", apr2)
	}
	if len(heatmap.VocabularyGrowth) != 1 || heatmap.VocabularyGrowth[0].Language != "de" || heatmap.VocabularyGrowth[0].Learned != 1 || heatmap.VocabularyGrowth[0].Month != "2026-04" {
		t.Errorf("vocabulary growth = %+v, want one forward note's first maturity by inherited language", heatmap.VocabularyGrowth)
	}

	retention, err := f.service.Retention(context.Background(), f.userID, "week")
	if err != nil {
		t.Fatalf("Retention() error = %v", err)
	}
	if len(retention.Points) != 12 {
		t.Fatalf("retention points = %d, want 12", len(retention.Points))
	}
	last := retention.Points[len(retention.Points)-1]
	if last.ReviewCards != 3 || last.RecalledReviews != 2 || last.ActualRetention != 2.0/3.0 {
		t.Errorf("current weekly retention = %+v, want review-card Again excluded from recall", last)
	}
}

func TestHeatmapGroupsActivityAcrossSpringGapAndFallFold(t *testing.T) {
	t.Run("spring gap", func(t *testing.T) {
		f := newStatsFixture(t)
		if _, err := f.database.SQL.Exec(`UPDATE user_settings SET day_start_hour = 2 WHERE user_id = ?`, f.userID); err != nil {
			t.Fatalf("set spring day start: %v", err)
		}
		f.addLog(t, f.cards[0], f.userID, 30, 3, int64(srs.Review), 30, 5, time.Date(2026, time.March, 8, 9, 59, 0, 0, time.UTC), 100)
		f.addLog(t, f.cards[0], f.userID, 31, 3, int64(srs.Review), 30, 5, time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC), 200)
		f.addLog(t, f.cards[0], f.userID, 32, 3, int64(srs.Review), 30, 5, time.Date(2026, time.March, 8, 10, 1, 0, 0, time.UTC), 300)

		heatmap, err := f.service.Heatmap(context.Background(), f.userID, 2026)
		if err != nil {
			t.Fatalf("Heatmap() error = %v", err)
		}
		if got := activityForDate(t, heatmap.Days, "2026-03-07"); got.Reviews != 1 || got.DurationMs != 100 {
			t.Errorf("March 7 bucket = %+v, want only the 09:59 UTC review before the spring boundary", got)
		}
		if got := activityForDate(t, heatmap.Days, "2026-03-08"); got.Reviews != 2 || got.DurationMs != 500 {
			t.Errorf("March 8 bucket = %+v, want reviews at and after the first valid 03:00 local boundary", got)
		}
	})

	t.Run("fall fold", func(t *testing.T) {
		f := newStatsFixture(t)
		f.clock.now = time.Date(2026, time.November, 2, 10, 0, 0, 0, time.UTC)
		if _, err := f.database.SQL.Exec(`UPDATE user_settings SET day_start_hour = 1 WHERE user_id = ?`, f.userID); err != nil {
			t.Fatalf("set fall day start: %v", err)
		}
		f.addLog(t, f.cards[0], f.userID, 30, 3, int64(srs.Review), 30, 5, time.Date(2026, time.November, 1, 8, 30, 0, 0, time.UTC), 100)
		f.addLog(t, f.cards[0], f.userID, 31, 3, int64(srs.Review), 30, 5, time.Date(2026, time.November, 1, 9, 30, 0, 0, time.UTC), 200)
		f.addLog(t, f.cards[0], f.userID, 32, 3, int64(srs.Review), 30, 5, time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC), 300)

		heatmap, err := f.service.Heatmap(context.Background(), f.userID, 2026)
		if err != nil {
			t.Fatalf("Heatmap() error = %v", err)
		}
		if got := activityForDate(t, heatmap.Days, "2026-11-01"); got.Reviews != 2 || got.DurationMs != 300 {
			t.Errorf("November 1 bucket = %+v, want both occurrences of 01:30 local", got)
		}
		if got := activityForDate(t, heatmap.Days, "2026-11-02"); got.Reviews != 1 || got.DurationMs != 300 {
			t.Errorf("November 2 bucket = %+v, want review at its 01:00 local boundary", got)
		}
	})
}

func TestForecastRebuildsEachSpringCalendarBoundary(t *testing.T) {
	f := newStatsFixture(t)
	f.clock.now = time.Date(2026, time.March, 8, 10, 30, 0, 0, time.UTC)
	if _, err := f.database.SQL.Exec(`UPDATE user_settings SET day_start_hour = 2 WHERE user_id = ?`, f.userID); err != nil {
		t.Fatalf("set spring day start: %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET due = ? WHERE id = ?`, time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC), f.cards[0]); err != nil {
		t.Fatalf("set first spring due date: %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET due = ? WHERE id = ?`, time.Date(2026, time.March, 9, 9, 0, 0, 0, time.UTC), f.cards[2]); err != nil {
		t.Fatalf("set next spring due date: %v", err)
	}

	forecast, err := f.service.Forecast(context.Background(), f.userID, 3)
	if err != nil {
		t.Fatalf("Forecast() error = %v", err)
	}
	want := []DueDay{{Date: "2026-03-08", Cards: 1}, {Date: "2026-03-09", Cards: 1}, {Date: "2026-03-10", Cards: 0}}
	if len(forecast.Due) != len(want) {
		t.Fatalf("forecast = %+v, want %+v", forecast.Due, want)
	}
	for index := range want {
		if forecast.Due[index] != want[index] {
			t.Errorf("forecast[%d] = %+v, want %+v", index, forecast.Due[index], want[index])
		}
	}
}

func TestHeatmapActivityRangeUsesCompositeIndex(t *testing.T) {
	f := newStatsFixture(t)
	rows, err := f.database.SQL.Query(`
		EXPLAIN QUERY PLAN
		SELECT reviewed_at, duration_ms, state_before, rating
		FROM review_logs
		WHERE user_id = ? AND undone = 0 AND reviewed_at >= CAST(? AS TEXT) AND reviewed_at < CAST(? AS TEXT)
		ORDER BY reviewed_at, id
	`, f.userID, databaseTimeBoundary(time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)), databaseTimeBoundary(time.Date(2027, time.January, 1, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	if !strings.Contains(strings.Join(details, "\n"), "review_logs_user_reviewed_idx") {
		t.Errorf("activity range query plan = %v, want composite user/reviewed_at index", details)
	}
}

func activityForDate(t *testing.T, days []ActivityDay, date string) ActivityDay {
	t.Helper()
	for _, day := range days {
		if day.Date == date {
			return day
		}
	}
	t.Fatalf("activity day %q not found", date)
	return ActivityDay{}
}

func TestForecastDistributionAndForeignCurveNotFound(t *testing.T) {
	f := newStatsFixture(t)
	forecast, err := f.service.Forecast(context.Background(), f.userID, 4)
	if err != nil {
		t.Fatalf("Forecast() error = %v", err)
	}
	if len(forecast.Due) != 4 || forecast.Due[0].Cards != 1 || forecast.Due[1].Cards != 0 || forecast.Due[2].Cards != 1 || forecast.Due[3].Cards != 1 {
		t.Errorf("forecast due = %+v, want overdue today and active future cards", forecast.Due)
	}

	distribution, err := f.service.Distribution(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Distribution() error = %v", err)
	}
	if len(distribution.States) != 4 || len(distribution.Leeches) != 1 || distribution.Leeches[0].CardID != f.cards[0] {
		t.Errorf("distribution states/leeches = %d/%+v", len(distribution.States), distribution.Leeches)
	}
	if len(distribution.DeckMedianStability) != 1 || distribution.DeckMedianStability[0].MedianStability != 30 {
		t.Errorf("deck medians = %+v, want SQL median stability 30", distribution.DeckMedianStability)
	}
	curve, err := f.service.Curve(context.Background(), f.userID, f.cards[0])
	if err != nil {
		t.Fatalf("Curve(owned) error = %v", err)
	}
	if len(curve.Reviews) != 4 {
		t.Errorf("curve review count = %d, want undone and foreign logs excluded", len(curve.Reviews))
	}
	if len(curve.Reviews) > 1 && (curve.Reviews[1].Retrievability <= 0 || curve.Reviews[1].Retrievability >= 1) {
		t.Errorf("past-review retrievability = %v, want a forgetting-curve point between zero and one", curve.Reviews[1].Retrievability)
	}
	_, err = f.service.Curve(context.Background(), f.userID, f.cards[4])
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Curve(foreign) error = %v, want ErrNotFound", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET deleted_at = ?, deletion_batch = 'stats-test-delete' WHERE id = ?`, f.clock.now, f.cards[0]); err != nil {
		t.Fatalf("soft-delete owned card: %v", err)
	}
	if _, err := f.service.Curve(context.Background(), f.userID, f.cards[0]); !errors.Is(err, ErrNotFound) {
		t.Errorf("Curve(deleted cached card) error = %v, want ErrNotFound", err)
	}
}

func TestForecastBoundsAndTimezoneValidation(t *testing.T) {
	f := newStatsFixture(t)
	if _, err := f.service.Forecast(context.Background(), f.userID, 0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Forecast(days=0) error = %v, want ErrInvalidInput", err)
	}
	if _, err := f.service.Retention(context.Background(), f.userID, "quarter"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Retention(period=quarter) error = %v, want ErrInvalidInput", err)
	}
	for _, year := range []int{1969, f.clock.now.Year() + 1} {
		if _, err := f.service.Heatmap(context.Background(), f.userID, year); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Heatmap(year=%d) error = %v, want ErrInvalidInput", year, err)
		}
	}
	if _, err := f.database.SQL.Exec(`UPDATE user_settings SET timezone = 'Not/AZone' WHERE user_id = ?`, f.userID); err != nil {
		t.Fatalf("set invalid timezone: %v", err)
	}
	if _, err := f.service.Heatmap(context.Background(), f.userID, 2026); !errors.Is(err, ErrInternal) {
		t.Errorf("Heatmap(invalid timezone) error = %v, want ErrInternal", err)
	}
}

func TestStatsCacheIsBoundedByInjectedFiveMinuteClock(t *testing.T) {
	f := newStatsFixture(t)
	first, err := f.service.Overview(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("first Overview() error = %v", err)
	}
	if _, err := f.database.SQL.Exec(`UPDATE cards SET state = 0 WHERE id = ?`, f.cards[0]); err != nil {
		t.Fatalf("mutate card: %v", err)
	}
	second, err := f.service.Overview(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("cached Overview() error = %v", err)
	}
	if second.Cards.Total != first.Cards.Total || second.Cards.Review != first.Cards.Review {
		t.Fatalf("cache result changed before TTL: before=%+v after=%+v", first.Cards, second.Cards)
	}
	f.clock.now = f.clock.now.Add(cacheTTL + time.Second)
	third, err := f.service.Overview(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("expired Overview() error = %v", err)
	}
	if third.Cards.Review != first.Cards.Review-1 || math.Abs(third.KnownRightNow) > 1e-12 {
		t.Errorf("expired cache did not refresh: %+v", third.Cards)
	}
}

func TestCalendarDayBoundarySpringGapUsesFirstValidWallTime(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	dates := []time.Time{
		calendarDate(2026, time.March, 7),
		calendarDate(2026, time.March, 8),
		calendarDate(2026, time.March, 9),
	}
	want := []time.Time{
		time.Date(2026, time.March, 7, 10, 0, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC), // 03:00 PDT is first valid time after missing 02:00.
		time.Date(2026, time.March, 9, 9, 0, 0, 0, time.UTC),
		time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC),
	}
	assertCalendarBoundaries(t, dates, 2, location, want)

	bounds, next := userDayBounds(time.Date(2026, time.March, 8, 9, 59, 0, 0, time.UTC), location, 2)
	if !bounds.Equal(want[0]) || !next.Equal(want[1]) {
		t.Errorf("pre-gap user day bounds = [%s,%s), want [%s,%s)", bounds, next, want[0], want[1])
	}
	bounds, next = userDayBounds(time.Date(2026, time.March, 8, 10, 1, 0, 0, time.UTC), location, 2)
	if !bounds.Equal(want[1]) || !next.Equal(want[2]) {
		t.Errorf("post-gap user day bounds = [%s,%s), want [%s,%s)", bounds, next, want[1], want[2])
	}

	week := retentionBuckets(time.Date(2026, time.March, 8, 10, 30, 0, 0, time.UTC), location, 2, "week", 12)
	last := week[len(week)-1]
	if got := last.end.Sub(last.start); got != 167*time.Hour {
		t.Errorf("spring-transition retention week = %s, want 167h and calendar-aligned boundaries", got)
	}
}

func TestCalendarDayBoundaryFallFoldUsesEarliestOccurrence(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	dates := []time.Time{
		calendarDate(2026, time.October, 31),
		calendarDate(2026, time.November, 1),
		calendarDate(2026, time.November, 2),
	}
	want := []time.Time{
		time.Date(2026, time.October, 31, 8, 0, 0, 0, time.UTC),
		time.Date(2026, time.November, 1, 8, 0, 0, 0, time.UTC), // First 01:00, still PDT.
		time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC),
		time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC),
	}
	assertCalendarBoundaries(t, dates, 1, location, want)

	for _, now := range []time.Time{
		time.Date(2026, time.November, 1, 8, 30, 0, 0, time.UTC), // First 01:30.
		time.Date(2026, time.November, 1, 9, 30, 0, 0, time.UTC), // Repeated 01:30.
	} {
		start, end := userDayBounds(now, location, 1)
		if !start.Equal(want[1]) || !end.Equal(want[2]) {
			t.Errorf("fold user day bounds at %s = [%s,%s), want [%s,%s)", now, start, end, want[1], want[2])
		}
	}

	week := retentionBuckets(time.Date(2026, time.November, 1, 10, 0, 0, 0, time.UTC), location, 1, "week", 12)
	last := week[len(week)-1]
	if got := last.end.Sub(last.start); got != 169*time.Hour {
		t.Errorf("fall-transition retention week = %s, want 169h and calendar-aligned boundaries", got)
	}
}

func TestUserDayBoundsResolveBerlinFoldToFirstOccurrence(t *testing.T) {
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{
		time.Date(2025, time.October, 26, 0, 30, 0, 0, time.UTC), // First 02:30.
		time.Date(2025, time.October, 26, 1, 30, 0, 0, time.UTC), // Repeated 02:30.
	} {
		wantStart := time.Date(2025, time.October, 26, 0, 0, 0, 0, time.UTC)
		wantEnd := time.Date(2025, time.October, 27, 1, 0, 0, 0, time.UTC)

		gotStart, gotEnd := userDayBounds(now, location, 2)
		if !gotStart.Equal(wantStart) || !gotEnd.Equal(wantEnd) {
			t.Errorf("Berlin user day bounds at %s = [%s,%s), want study bounds [%s,%s)",
				now, gotStart, gotEnd, wantStart, wantEnd)
		}
	}
}

func TestStreakUsesCalendarBoundariesAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("spring gap", func(t *testing.T) {
		f := newStatsFixture(t)
		f.addLog(t, f.cards[0], f.userID, 30, 3, int64(srs.Review), 30, 5, time.Date(2026, time.March, 8, 9, 59, 0, 0, time.UTC), 100)
		f.addLog(t, f.cards[0], f.userID, 31, 3, int64(srs.Review), 30, 5, time.Date(2026, time.March, 8, 10, 1, 0, 0, time.UTC), 200)
		current := calendarDayBoundary(calendarDate(2026, time.March, 8), 2, location)
		got, err := f.service.streak(context.Background(), f.userID, current, f.clock.now, location, 2)
		if err != nil {
			t.Fatalf("streak() error = %v", err)
		}
		if got != 2 {
			t.Errorf("spring streak = %d, want two consecutive local days across the gap", got)
		}
	})

	t.Run("fall fold", func(t *testing.T) {
		f := newStatsFixture(t)
		f.clock.now = time.Date(2026, time.November, 2, 10, 0, 0, 0, time.UTC)
		f.addLog(t, f.cards[0], f.userID, 30, 3, int64(srs.Review), 30, 5, time.Date(2026, time.November, 1, 8, 30, 0, 0, time.UTC), 100)
		f.addLog(t, f.cards[0], f.userID, 31, 3, int64(srs.Review), 30, 5, time.Date(2026, time.November, 1, 9, 30, 0, 0, time.UTC), 200)
		f.addLog(t, f.cards[0], f.userID, 32, 3, int64(srs.Review), 30, 5, time.Date(2026, time.November, 2, 9, 30, 0, 0, time.UTC), 300)
		current := calendarDayBoundary(calendarDate(2026, time.November, 2), 1, location)
		got, err := f.service.streak(context.Background(), f.userID, current, f.clock.now, location, 1)
		if err != nil {
			t.Fatalf("streak() error = %v", err)
		}
		if got != 2 {
			t.Errorf("fall streak = %d, want two consecutive local days across the fold", got)
		}
	})
}

func TestStreakUsesStudyDayAcrossBerlinFold(t *testing.T) {
	f := newStatsFixture(t)
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.now = time.Date(2025, time.October, 26, 0, 30, 0, 0, time.UTC)
	if _, err := f.database.SQL.Exec(`UPDATE user_settings SET timezone = 'Europe/Berlin', day_start_hour = 2 WHERE user_id = ?`, f.userID); err != nil {
		t.Fatalf("set Berlin study day: %v", err)
	}
	f.addLog(t, f.cards[0], f.userID, 30, 3, int64(srs.Review), 30, 5, time.Date(2025, time.October, 25, 12, 0, 0, 0, time.UTC), 100)
	f.addLog(t, f.cards[0], f.userID, 31, 3, int64(srs.Review), 30, 5, f.clock.now, 200)
	currentStart, _ := userDayBounds(f.clock.now, location, 2)

	got, err := f.service.streak(context.Background(), f.userID, currentStart, f.clock.now, location, 2)
	if err != nil {
		t.Fatalf("streak() error = %v", err)
	}
	if got != 2 {
		t.Errorf("streak during first occurrence of Berlin 02:30 = %d, want both consecutive study days", got)
	}
}

func TestStreakTimestampQueryUsesCompositeIndex(t *testing.T) {
	f := newStatsFixture(t)
	rows, err := f.database.SQL.Query(`
		EXPLAIN QUERY PLAN
		SELECT logs.reviewed_at
		FROM review_logs AS logs
		JOIN review_log_projection AS projection ON projection.review_id = logs.id
		WHERE logs.user_id = ? AND logs.undone = 0
		  AND logs.reviewed_at >= CAST(? AS TEXT) AND logs.reviewed_at < CAST(? AS TEXT)
		  AND logs.reviewed_at <= CAST(? AS TEXT)
		ORDER BY logs.reviewed_at DESC
	`, f.userID,
		databaseTimeBoundary(time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)),
		databaseTimeBoundary(time.Date(2026, time.April, 4, 0, 0, 0, 0, time.UTC)),
		time.Date(2026, time.April, 3, 12, 0, 0, 0, time.UTC).String())
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	if !strings.Contains(strings.Join(details, "\n"), "review_logs_user_reviewed_idx") {
		t.Errorf("streak timestamp query plan = %v, want composite user/reviewed_at index", details)
	}
}

func TestCurveIncludesScheduledEndAndCurrentTimeWithBoundedSampling(t *testing.T) {
	tests := []struct {
		name          string
		lastReview    time.Time
		scheduledDays int
		wantEnd       func(now time.Time, lastReview time.Time, scheduledDays int) time.Time
	}{
		{
			name: "long future interval reaches next review",
			wantEnd: func(_ time.Time, lastReview time.Time, scheduledDays int) time.Time {
				return lastReview.Add(time.Duration(scheduledDays) * 24 * time.Hour)
			},
		},
		{
			name:          "overdue card reaches now",
			lastReview:    time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC),
			scheduledDays: 30,
			wantEnd: func(now time.Time, _ time.Time, _ int) time.Time {
				return now
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newStatsFixture(t)
			lastReview := tt.lastReview
			if lastReview.IsZero() {
				lastReview = f.clock.now.Add(-24 * time.Hour)
			}
			scheduledDays := tt.scheduledDays
			if scheduledDays == 0 {
				scheduledDays = 365
			}
			if _, err := f.database.SQL.Exec(`
				UPDATE cards SET last_review = ?, stability = 30, difficulty = 5, scheduled_days = ?
				WHERE id = ?
			`, lastReview, scheduledDays, f.cards[0]); err != nil {
				t.Fatalf("set curve card schedule: %v", err)
			}

			curve, err := f.service.Curve(context.Background(), f.userID, f.cards[0])
			if err != nil {
				t.Fatalf("Curve() error = %v", err)
			}
			if len(curve.Curve) < 2 || len(curve.Curve) > 91 {
				t.Fatalf("curve point count = %d, want 2..91", len(curve.Curve))
			}
			if !curve.Curve[0].At.Equal(lastReview) {
				t.Errorf("first curve point = %s, want last review %s", curve.Curve[0].At, lastReview)
			}
			wantEnd := tt.wantEnd(f.clock.now, lastReview, scheduledDays)
			if !curve.Curve[len(curve.Curve)-1].At.Equal(wantEnd) {
				t.Errorf("last curve point = %s, want %s", curve.Curve[len(curve.Curve)-1].At, wantEnd)
			}
		})
	}
}

func assertCalendarBoundaries(t *testing.T, dates []time.Time, startHour int, location *time.Location, want []time.Time) {
	t.Helper()
	if len(want) != len(dates)+1 {
		t.Fatalf("dates/want boundary lengths = %d/%d, want one trailing end boundary", len(dates), len(want))
	}
	for index, date := range dates {
		got := calendarDayBoundary(date, startHour, location)
		if !got.Equal(want[index]) {
			t.Errorf("boundary for %s at hour %d = %s, want %s", date.Format("2006-01-02"), startHour, got, want[index])
		}
		if index > 0 && !got.After(want[index-1]) {
			t.Errorf("boundary for %s did not advance: previous %s, current %s", date.Format("2006-01-02"), want[index-1], got)
		}
	}
	buckets := makeDayBuckets(dates[0], addCalendarDays(dates[0], len(dates)), startHour, location)
	if len(buckets) != len(dates) {
		t.Fatalf("daily bucket count = %d, want %d", len(buckets), len(dates))
	}
	for index, bucket := range buckets {
		if bucket.label != dates[index].Format("2006-01-02") || !bucket.start.Equal(want[index]) || !bucket.end.Equal(want[index+1]) {
			t.Errorf("daily bucket[%d] = %+v, want label=%s start=%s end=%s", index, bucket, dates[index].Format("2006-01-02"), want[index], want[index+1])
		}
		if index > 0 && !bucket.start.Equal(buckets[index-1].end) {
			t.Errorf("daily bucket[%d] start %s is not prior end %s", index, bucket.start, buckets[index-1].end)
		}
	}
}
