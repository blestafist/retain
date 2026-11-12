// Package stats provides owner-scoped learning analytics.
package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/srs"
)

var (
	ErrInvalidInput = errors.New("invalid stats input")
	ErrNotFound     = errors.New("stats resource not found")
	ErrInternal     = errors.New("stats service error")
)

const (
	cacheTTL       = 5 * time.Minute
	maxCacheItems  = 128
	maxForecastDay = 90
)

// Service computes analytics from SQL aggregates and the server FSRS scheduler.
type Service struct {
	database *db.DB
	clock    clock.Clock

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

func NewService(database *db.DB, c clock.Clock) *Service {
	if c == nil {
		c = clock.Real{}
	}
	return &Service{database: database, clock: c, cache: make(map[string]cacheEntry)}
}

// Overview contains daily and lifetime totals plus the current known-right-now estimate.
type Overview struct {
	Timezone         string       `json:"timezone"`
	DayStartHour     int          `json:"day_start_hour"`
	StreakDays       int          `json:"streak_days"`
	Today            TodayMetrics `json:"today"`
	Cards            CardTotals   `json:"cards"`
	Reviews          ReviewTotals `json:"reviews"`
	KnownRightNow    float64      `json:"known_right_now"`
	DesiredRetention float64      `json:"desired_retention"`
}

type TodayMetrics struct {
	Reviews         int64   `json:"reviews"`
	NewCards        int64   `json:"new_cards"`
	ReviewCards     int64   `json:"review_cards"`
	RecalledReviews int64   `json:"recalled_reviews"`
	Retention       float64 `json:"retention"`
	DurationMs      int64   `json:"duration_ms"`
}

type CardTotals struct {
	Total      int64 `json:"total"`
	New        int64 `json:"new"`
	Learning   int64 `json:"learning"`
	Review     int64 `json:"review"`
	Relearning int64 `json:"relearning"`
	Due        int64 `json:"due"`
	Young      int64 `json:"young"`
	Mature     int64 `json:"mature"`
	Leeches    int64 `json:"leeches"`
}

type ReviewTotals struct {
	Total             int64 `json:"total"`
	TotalDurationMs   int64 `json:"total_duration_ms"`
	AverageDurationMs int64 `json:"average_duration_ms"`
}

type Forecast struct {
	Days int      `json:"days"`
	Due  []DueDay `json:"due"`
}

type DueDay struct {
	Date  string `json:"date"`
	Cards int64  `json:"cards"`
}

type Retention struct {
	Period           string           `json:"period"`
	DesiredRetention float64          `json:"desired_retention"`
	Points           []RetentionPoint `json:"points"`
}

type RetentionPoint struct {
	Period          string  `json:"period"`
	ReviewCards     int64   `json:"review_cards"`
	RecalledReviews int64   `json:"recalled_reviews"`
	ActualRetention float64 `json:"actual_retention"`
}

type Heatmap struct {
	Year             int                `json:"year"`
	Days             []ActivityDay      `json:"days"`
	VocabularyGrowth []VocabularyGrowth `json:"vocabulary_growth"`
}

type ActivityDay struct {
	Date       string `json:"date"`
	Reviews    int64  `json:"reviews"`
	DurationMs int64  `json:"duration_ms"`
}

type VocabularyGrowth struct {
	Month    string `json:"month"`
	Language string `json:"language"`
	Learned  int64  `json:"learned"`
}

type Curve struct {
	CardID         int64         `json:"card_id"`
	Term           string        `json:"term"`
	Direction      string        `json:"direction"`
	State          int64         `json:"state"`
	Stability      *float64      `json:"stability,omitempty"`
	Difficulty     *float64      `json:"difficulty,omitempty"`
	LastReview     *time.Time    `json:"last_review,omitempty"`
	Retrievability float64       `json:"retrievability"`
	Reviews        []CurveReview `json:"reviews"`
	Curve          []CurvePoint  `json:"curve"`
}

type CurveReview struct {
	ReviewedAt      time.Time `json:"reviewed_at"`
	Rating          int64     `json:"rating"`
	StateBefore     int64     `json:"state_before"`
	Retrievability  float64   `json:"retrievability"`
	StabilityAfter  float64   `json:"stability_after"`
	DifficultyAfter float64   `json:"difficulty_after"`
	ScheduledDays   int64     `json:"scheduled_days"`
}

type CurvePoint struct {
	At             time.Time `json:"at"`
	Retrievability float64   `json:"retrievability"`
}

type Distribution struct {
	States              []StateDistribution `json:"states"`
	Stability           []HistogramBin      `json:"stability"`
	Difficulty          []HistogramBin      `json:"difficulty"`
	Leeches             []Leech             `json:"leeches"`
	DeckMedianStability []DeckMedian        `json:"deck_median_stability"`
}

type StateDistribution struct {
	State  int64 `json:"state"`
	Total  int64 `json:"total"`
	Young  int64 `json:"young"`
	Mature int64 `json:"mature"`
}

type HistogramBin struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}

type Leech struct {
	CardID    int64  `json:"card_id"`
	Term      string `json:"term"`
	Direction string `json:"direction"`
	Lapses    int64  `json:"lapses"`
}

type DeckMedian struct {
	DeckID          int64   `json:"deck_id"`
	DeckName        string  `json:"deck_name"`
	MedianStability float64 `json:"median_stability"`
}

type userEnvironment struct {
	location         *time.Location
	dayStartHour     int
	desiredRetention float64
	scheduler        srs.Scheduler
}

func (s *Service) Overview(ctx context.Context, userID int64) (Overview, error) {
	var result Overview
	err := s.cached(fmt.Sprintf("overview:%d", userID), &result, func() (any, error) {
		env, err := s.environment(ctx, userID)
		if err != nil {
			return nil, err
		}
		now := s.now()
		dayStart, dayEnd := userDayBounds(now, env.location, env.dayStartHour)
		row, err := s.database.Queries().GetStatsOverview(ctx, gen.GetStatsOverviewParams{
			NowUnix: unixTime(now), UserID: userID,
			DayStartUnix: unixTime(dayStart), DayEndUnix: unixTime(dayEnd),
		})
		if err != nil {
			return nil, ErrInternal
		}
		known, err := s.knownRightNow(ctx, userID, env.scheduler, now)
		if err != nil {
			return nil, err
		}
		streak, err := s.streak(ctx, userID, dayStart, env.location, env.dayStartHour)
		if err != nil {
			return nil, err
		}
		retention := 0.0
		if row.ReviewCardsToday > 0 {
			retention = float64(row.RecalledReviewsToday) / float64(row.ReviewCardsToday)
		}
		return Overview{
			Timezone: env.location.String(), DayStartHour: env.dayStartHour,
			StreakDays: streak,
			Today: TodayMetrics{
				Reviews: row.ReviewsToday, NewCards: row.NewReviewsToday,
				ReviewCards: row.ReviewCardsToday, RecalledReviews: row.RecalledReviewsToday,
				Retention: retention, DurationMs: row.DurationTodayMs,
			},
			Cards: CardTotals{
				Total: row.TotalCards, New: row.NewCards, Learning: row.LearningCards,
				Review: row.ReviewCards, Relearning: row.RelearningCards, Due: row.DueCards,
				Young: row.YoungCards, Mature: row.MatureCards, Leeches: row.LeechCards,
			},
			Reviews: ReviewTotals{
				Total: row.TotalReviews, TotalDurationMs: row.TotalDurationMs,
				AverageDurationMs: row.AverageDurationMs,
			},
			KnownRightNow: known, DesiredRetention: env.desiredRetention,
		}, nil
	})
	return result, err
}

func (s *Service) Forecast(ctx context.Context, userID int64, days int) (Forecast, error) {
	if days < 1 || days > maxForecastDay {
		return Forecast{}, ErrInvalidInput
	}
	var result Forecast
	err := s.cached(fmt.Sprintf("forecast:%d:%d", userID, days), &result, func() (any, error) {
		env, err := s.environment(ctx, userID)
		if err != nil {
			return nil, err
		}
		first, _ := userDayBounds(s.now(), env.location, env.dayStartHour)
		firstDate := calendarDateFromLocal(first, env.location)
		result := Forecast{Days: days, Due: make([]DueDay, 0, days)}
		for index := range days {
			date := addCalendarDays(firstDate, index)
			start := calendarDayBoundary(date, env.dayStartHour, env.location)
			end := calendarDayBoundary(addCalendarDays(date, 1), env.dayStartHour, env.location)
			queryStart := start
			if index == 0 {
				queryStart = time.Unix(-62135596800, 0).UTC()
			}
			row, err := s.database.Queries().GetStatsForecast(ctx, gen.GetStatsForecastParams{
				Label: start.In(env.location).Format("2006-01-02"), UserID: userID,
				StartUnix: unixTime(queryStart), EndUnix: unixTime(end.UTC()),
			})
			if err != nil {
				return nil, ErrInternal
			}
			result.Due = append(result.Due, DueDay{Date: row.Label, Cards: row.DueCards})
		}
		return result, nil
	})
	return result, err
}

func (s *Service) Retention(ctx context.Context, userID int64, period string) (Retention, error) {
	periods, ok := retentionPeriodCount(period)
	if !ok {
		return Retention{}, ErrInvalidInput
	}
	var result Retention
	err := s.cached(fmt.Sprintf("retention:%d:%s", userID, period), &result, func() (any, error) {
		env, err := s.environment(ctx, userID)
		if err != nil {
			return nil, err
		}
		buckets := retentionBuckets(s.now(), env.location, env.dayStartHour, period, periods)
		points := make([]RetentionPoint, 0, len(buckets))
		for _, bucket := range buckets {
			activity, err := s.activityBucket(ctx, userID, bucket)
			if err != nil {
				return nil, err
			}
			actual := 0.0
			if activity.ReviewCards > 0 {
				actual = float64(activity.RecalledReviews) / float64(activity.ReviewCards)
			}
			points = append(points, RetentionPoint{
				Period: bucket.label, ReviewCards: activity.ReviewCards,
				RecalledReviews: activity.RecalledReviews, ActualRetention: actual,
			})
		}
		return Retention{Period: period, DesiredRetention: env.desiredRetention, Points: points}, nil
	})
	return result, err
}

func (s *Service) Heatmap(ctx context.Context, userID int64, year int) (Heatmap, error) {
	env, err := s.environment(ctx, userID)
	if err != nil {
		return Heatmap{}, err
	}
	currentYear := s.now().In(env.location).Year()
	if year == 0 {
		year = currentYear
	}
	if year < 1970 || year > currentYear {
		return Heatmap{}, ErrInvalidInput
	}
	var result Heatmap
	err = s.cached(fmt.Sprintf("heatmap:%d:%d", userID, year), &result, func() (any, error) {
		startDate := calendarDate(year, time.January, 1)
		endDate := calendarDate(year+1, time.January, 1)
		buckets := makeDayBuckets(startDate, endDate, env.dayStartHour, env.location)
		if len(buckets) == 0 {
			return nil, ErrInternal
		}
		activityRows, err := s.database.Queries().ListStatsActivityRange(ctx, gen.ListStatsActivityRangeParams{
			UserID:  userID,
			StartAt: databaseTimeBoundary(buckets[0].start),
			EndAt:   databaseTimeBoundary(buckets[len(buckets)-1].end),
		})
		if err != nil {
			return nil, ErrInternal
		}
		days := groupActivityRows(activityRows, buckets)
		growthBuckets := makeMonthBuckets(startDate, endDate, env.dayStartHour, env.location)
		growth := make([]VocabularyGrowth, 0)
		for _, bucket := range growthBuckets {
			rows, err := s.database.Queries().GetStatsVocabularyGrowth(ctx, gen.GetStatsVocabularyGrowthParams{
				UserID: userID, StartAt: databaseTimeBoundary(bucket.start), EndAt: databaseTimeBoundary(bucket.end),
			})
			if err != nil {
				return nil, ErrInternal
			}
			for _, row := range rows {
				growth = append(growth, VocabularyGrowth{Month: bucket.label, Language: row.LangTarget, Learned: row.Learned})
			}
		}
		return Heatmap{Year: year, Days: days, VocabularyGrowth: growth}, nil
	})
	return result, err
}

func (s *Service) Curve(ctx context.Context, userID, cardID int64) (Curve, error) {
	if cardID <= 0 || userID <= 0 {
		return Curve{}, ErrInvalidInput
	}
	if !s.ready() {
		return Curve{}, ErrInternal
	}
	card, err := s.database.Queries().GetStatsCardCurveCard(ctx, gen.GetStatsCardCurveCardParams{CardID: cardID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return Curve{}, ErrNotFound
	}
	if err != nil {
		return Curve{}, ErrInternal
	}
	var result Curve
	err = s.cached(fmt.Sprintf("curve:%d:%d", userID, cardID), &result, func() (any, error) {
		env, err := s.environment(ctx, userID)
		if err != nil {
			return nil, err
		}
		queries := s.database.Queries()
		logs, err := queries.ListStatsCardCurveLogs(ctx, gen.ListStatsCardCurveLogsParams{CardID: cardID, UserID: userID})
		if err != nil {
			return nil, ErrInternal
		}
		now := s.now()
		cardState := srs.Card{State: srs.State(card.State)}
		if card.Stability.Valid {
			cardState.Stability = card.Stability.Float64
		}
		if card.Difficulty.Valid {
			cardState.Difficulty = card.Difficulty.Float64
		}
		if card.LastReview.Valid {
			cardState.LastReview = card.LastReview.Time
		}
		curve := Curve{
			CardID: cardID, Term: card.Term, Direction: card.Direction, State: card.State,
			Stability: nullableFloatPointer(card.Stability), Difficulty: nullableFloatPointer(card.Difficulty),
			LastReview: nullableTimePointer(card.LastReview), Reviews: make([]CurveReview, 0, len(logs)),
			Curve: make([]CurvePoint, 0),
		}
		curve.Retrievability = env.scheduler.Retrievability(cardState, now)
		for index, log := range logs {
			retrievabilityBefore := 0.0
			if log.StateBefore == int64(srs.Review) && index > 0 {
				previous := logs[index-1]
				retrievabilityBefore = env.scheduler.Retrievability(srs.Card{
					State: srs.Review, Stability: previous.StabilityAfter,
					Difficulty: previous.DifficultyAfter, LastReview: previous.ReviewedAt,
				}, log.ReviewedAt)
			}
			curve.Reviews = append(curve.Reviews, CurveReview{
				ReviewedAt: log.ReviewedAt.UTC(), Rating: log.Rating, StateBefore: log.StateBefore,
				Retrievability: retrievabilityBefore,
				StabilityAfter: log.StabilityAfter, DifficultyAfter: log.DifficultyAfter,
				ScheduledDays: log.ScheduledDays,
			})
		}
		if cardState.State == srs.Review && cardState.Stability > 0 && !cardState.LastReview.IsZero() {
			end := now
			scheduledDays := max64(card.ScheduledDays, 0)
			if scheduledDays > 90 {
				scheduledDays = 90
			}
			if scheduledEnd := cardState.LastReview.Add(time.Duration(scheduledDays) * 24 * time.Hour); scheduledEnd.After(end) {
				end = scheduledEnd
			}
			if span := end.Sub(cardState.LastReview); span > 90*24*time.Hour {
				end = cardState.LastReview.Add(90 * 24 * time.Hour)
			}
			for at := cardState.LastReview; !at.After(end); at = at.Add(24 * time.Hour) {
				pointCard := cardState
				pointCard.LastReview = cardState.LastReview
				retrievability := env.scheduler.Retrievability(pointCard, at)
				curve.Curve = append(curve.Curve, CurvePoint{At: at.UTC(), Retrievability: retrievability})
			}
		}
		return curve, nil
	})
	return result, err
}

func (s *Service) Distribution(ctx context.Context, userID int64) (Distribution, error) {
	if userID <= 0 {
		return Distribution{}, ErrInvalidInput
	}
	var result Distribution
	err := s.cached(fmt.Sprintf("distribution:%d", userID), &result, func() (any, error) {
		queries := s.database.Queries()
		states, err := queries.GetStatsStateDistribution(ctx, userID)
		if err != nil {
			return nil, ErrInternal
		}
		stability, err := queries.GetStatsStabilityDistribution(ctx, userID)
		if err != nil {
			return nil, ErrInternal
		}
		difficulty, err := queries.GetStatsDifficultyDistribution(ctx, userID)
		if err != nil {
			return nil, ErrInternal
		}
		leeches, err := queries.ListStatsLeeches(ctx, userID)
		if err != nil {
			return nil, ErrInternal
		}
		deckMedians, err := queries.ListStatsDeckMedianStability(ctx, userID)
		if err != nil {
			return nil, ErrInternal
		}
		result := Distribution{
			States:              make([]StateDistribution, 0, len(states)),
			Stability:           make([]HistogramBin, 0, len(stability)),
			Difficulty:          make([]HistogramBin, 0, len(difficulty)),
			Leeches:             make([]Leech, 0, len(leeches)),
			DeckMedianStability: make([]DeckMedian, 0, len(deckMedians)),
		}
		for _, row := range states {
			result.States = append(result.States, StateDistribution{State: row.State, Total: row.Total, Young: row.Young, Mature: row.Mature})
		}
		for _, row := range stability {
			result.Stability = append(result.Stability, HistogramBin{Bucket: row.Bucket, Count: row.Count})
		}
		for _, row := range difficulty {
			result.Difficulty = append(result.Difficulty, HistogramBin{Bucket: row.Bucket, Count: row.Count})
		}
		for _, row := range leeches {
			result.Leeches = append(result.Leeches, Leech{CardID: row.CardID, Term: row.Term, Direction: row.Direction, Lapses: row.Lapses})
		}
		for _, row := range deckMedians {
			if row.MedianStability.Valid {
				result.DeckMedianStability = append(result.DeckMedianStability, DeckMedian{DeckID: row.DeckID, DeckName: row.DeckName, MedianStability: row.MedianStability.Float64})
			}
		}
		return result, nil
	})
	return result, err
}

func (s *Service) environment(ctx context.Context, userID int64) (userEnvironment, error) {
	if !s.ready() {
		return userEnvironment{}, ErrInternal
	}
	if userID <= 0 {
		return userEnvironment{}, ErrInvalidInput
	}
	settings, err := s.database.Queries().GetStatsSettings(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return userEnvironment{}, ErrNotFound
	}
	if err != nil {
		return userEnvironment{}, ErrInternal
	}
	location, err := time.LoadLocation(settings.Timezone)
	if err != nil || settings.DayStartHour < 0 || settings.DayStartHour > 23 {
		return userEnvironment{}, ErrInternal
	}
	parameters := srs.DefaultParameters()
	parameters.RequestRetention = settings.RequestRetention
	if parameters.RequestRetention >= 1 {
		// Retrievability does not use the desired retention value; the scheduler
		// requires a strict probability below one.
		parameters.RequestRetention = srs.DefaultParameters().RequestRetention
	}
	if settings.MaximumInterval <= 0 {
		return userEnvironment{}, ErrInternal
	}
	parameters.MaximumInterval = uint64(settings.MaximumInterval)
	if settings.FsrsWeights.Valid {
		var weights []float64
		if err := json.Unmarshal([]byte(settings.FsrsWeights.String), &weights); err != nil || (len(weights) != 17 && len(weights) != len(parameters.Weights)) {
			return userEnvironment{}, ErrInternal
		}
		copy(parameters.Weights[:], weights)
	}
	scheduler, err := srs.NewScheduler(parameters)
	if err != nil {
		return userEnvironment{}, ErrInternal
	}
	return userEnvironment{
		location: location, dayStartHour: int(settings.DayStartHour),
		desiredRetention: settings.RequestRetention, scheduler: scheduler,
	}, nil
}

func (s *Service) knownRightNow(ctx context.Context, userID int64, scheduler srs.Scheduler, now time.Time) (float64, error) {
	rows, err := s.database.Queries().GetStatsRetrievabilityCards(ctx, userID)
	if err != nil {
		return 0, ErrInternal
	}
	known := 0.0
	for _, row := range rows {
		if !row.Stability.Valid || !row.Difficulty.Valid || !row.LastReview.Valid || row.State != int64(srs.Review) {
			continue
		}
		known += scheduler.Retrievability(srs.Card{
			State: srs.Review, Stability: row.Stability.Float64, Difficulty: row.Difficulty.Float64,
			LastReview: row.LastReview.Time, Due: row.Due,
			ElapsedDays: uint64(max64(row.ElapsedDays, 0)), ScheduledDays: uint64(max64(row.ScheduledDays, 0)),
			Step: uint64(max64(row.Step, 0)), Reps: uint64(max64(row.Reps, 0)), Lapses: uint64(max64(row.Lapses, 0)),
		}, now)
	}
	return known, nil
}

func (s *Service) streak(ctx context.Context, userID int64, currentStart time.Time, location *time.Location, dayStartHour int) (int, error) {
	date := calendarDateFromLocal(currentStart, location)
	start := calendarDayBoundary(date, dayStartHour, location)
	end := calendarDayBoundary(addCalendarDays(date, 1), dayStartHour, location)
	activity, err := s.activityBucket(ctx, userID, dateBucket{label: "", start: start, end: end})
	if err != nil {
		return 0, err
	}
	streak := 0
	if activity.Reviews > 0 {
		streak++
	}
	date = addCalendarDays(date, -1)
	for {
		start := calendarDayBoundary(date, dayStartHour, location)
		end := calendarDayBoundary(addCalendarDays(date, 1), dayStartHour, location)
		activity, err := s.activityBucket(ctx, userID, dateBucket{label: "", start: start, end: end})
		if err != nil {
			return 0, err
		}
		if activity.Reviews == 0 {
			break
		}
		streak++
		date = addCalendarDays(date, -1)
	}
	return streak, nil
}

type activityAggregate struct {
	Reviews         int64
	DurationMs      int64
	ReviewCards     int64
	RecalledReviews int64
}

type dateBucket struct {
	label string
	start time.Time
	end   time.Time
}

func (s *Service) activityBucket(ctx context.Context, userID int64, bucket dateBucket) (activityAggregate, error) {
	row, err := s.database.Queries().GetStatsBucketsActivity(ctx, gen.GetStatsBucketsActivityParams{
		Label: bucket.label, UserID: userID, StartUnix: unixTime(bucket.start), EndUnix: unixTime(bucket.end),
	})
	if err != nil {
		return activityAggregate{}, ErrInternal
	}
	return activityAggregate{
		Reviews: row.Reviews, DurationMs: row.DurationMs,
		ReviewCards: row.ReviewCards, RecalledReviews: row.RecalledReviews,
	}, nil
}

func retentionBuckets(now time.Time, location *time.Location, dayStartHour int, period string, count int) []dateBucket {
	day, _ := userDayBounds(now, location, dayStartHour)
	localDate := calendarDateFromLocal(day, location)
	var currentDate time.Time
	switch period {
	case "week":
		weekday := int(localDate.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		currentDate = addCalendarDays(localDate, 1-weekday)
	case "month":
		currentDate = calendarDate(localDate.Year(), localDate.Month(), 1)
	case "year":
		currentDate = calendarDate(localDate.Year(), time.January, 1)
	}
	buckets := make([]dateBucket, 0, count)
	for index := count - 1; index >= 0; index-- {
		var startDate, endDate time.Time
		switch period {
		case "week":
			startDate = addCalendarDays(currentDate, -7*index)
			endDate = addCalendarDays(startDate, 7)
		case "month":
			startDate = addCalendarMonths(currentDate, -index)
			endDate = addCalendarMonths(startDate, 1)
		case "year":
			startDate = calendarDate(currentDate.Year()-index, time.January, 1)
			endDate = calendarDate(startDate.Year()+1, time.January, 1)
		}
		label := startDate.Format("2006")
		if period == "month" {
			label = startDate.Format("2006-01")
		} else if period == "week" {
			year, week := startDate.ISOWeek()
			label = fmt.Sprintf("%04d-W%02d", year, week)
		}
		buckets = append(buckets, dateBucket{
			label: label,
			start: calendarDayBoundary(startDate, dayStartHour, location),
			end:   calendarDayBoundary(endDate, dayStartHour, location),
		})
	}
	return buckets
}

func retentionPeriodCount(period string) (int, bool) {
	switch period {
	case "week":
		return 12, true
	case "month":
		return 12, true
	case "year":
		return 5, true
	default:
		return 0, false
	}
}

func makeDayBuckets(startDate, endDate time.Time, dayStartHour int, location *time.Location) []dateBucket {
	var result []dateBucket
	for date := startDate; date.Before(endDate); date = addCalendarDays(date, 1) {
		nextDate := addCalendarDays(date, 1)
		result = append(result, dateBucket{
			label: date.Format("2006-01-02"),
			start: calendarDayBoundary(date, dayStartHour, location),
			end:   calendarDayBoundary(nextDate, dayStartHour, location),
		})
	}
	return result
}

func makeMonthBuckets(startDate, endDate time.Time, dayStartHour int, location *time.Location) []dateBucket {
	var result []dateBucket
	for date := startDate; date.Before(endDate); date = addCalendarMonths(date, 1) {
		nextDate := addCalendarMonths(date, 1)
		result = append(result, dateBucket{
			label: date.Format("2006-01"),
			start: calendarDayBoundary(date, dayStartHour, location),
			end:   calendarDayBoundary(nextDate, dayStartHour, location),
		})
	}
	return result
}

func userDayBounds(now time.Time, location *time.Location, startHour int) (time.Time, time.Time) {
	localNow := now.In(location)
	date := calendarDateFromLocal(localNow, location)
	start := calendarDayBoundary(date, startHour, location)
	if localNow.Before(start) {
		date = addCalendarDays(date, -1)
		start = calendarDayBoundary(date, startHour, location)
	}
	end := calendarDayBoundary(addCalendarDays(date, 1), startHour, location)
	return start, end
}

// calendarDayBoundary resolves a local start-hour without relying on time.Date's
// unspecified handling of nonexistent or ambiguous wall times. In a fold, the
// earliest UTC occurrence is selected. In a gap, the transition instant is the
// first valid wall time after the configured hour.
func calendarDayBoundary(date time.Time, startHour int, location *time.Location) time.Time {
	year, month, day := date.UTC().Date()
	targetWall := time.Date(year, month, day, startHour, 0, 0, 0, time.UTC)
	if location == nil || startHour < 0 || startHour > 23 {
		return targetWall
	}

	const scanStep = 15 * time.Minute
	scanStart := targetWall.Add(-48 * time.Hour)
	scanEnd := targetWall.Add(48 * time.Hour)
	offsets := make(map[int]struct{})
	transitions := make([]zoneTransition, 0, 2)
	previous := scanStart
	_, previousOffset := previous.In(location).Zone()
	offsets[previousOffset] = struct{}{}
	for sample := scanStart.Add(scanStep); !sample.After(scanEnd); sample = sample.Add(scanStep) {
		_, offset := sample.In(location).Zone()
		offsets[offset] = struct{}{}
		if offset != previousOffset {
			at := findOffsetTransition(previous, sample, previousOffset, location)
			_, before := at.Add(-time.Second).In(location).Zone()
			_, after := at.In(location).Zone()
			transitions = append(transitions, zoneTransition{at: at, before: before, after: after})
			offsets[before] = struct{}{}
			offsets[after] = struct{}{}
		}
		previous, previousOffset = sample, offset
	}

	var earliest time.Time
	for offset := range offsets {
		candidate := targetWall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if sameLocalDate(local, year, month, day) && local.Hour() == startHour && local.Minute() == 0 && local.Second() == 0 {
			if earliest.IsZero() || candidate.Before(earliest) {
				earliest = candidate
			}
		}
	}
	if !earliest.IsZero() {
		return earliest.UTC()
	}

	for _, transition := range transitions {
		if transition.after <= transition.before {
			continue
		}
		gapStart := transition.at.Add(time.Duration(transition.before) * time.Second)
		gapEnd := transition.at.Add(time.Duration(transition.after) * time.Second)
		if !targetWall.Before(gapStart) && targetWall.Before(gapEnd) {
			return transition.at.UTC()
		}
	}

	// The gap transition should cover every nonexistent wall time. Keep a
	// deterministic fallback for unusual historical zone data: select the
	// closest representable wall time after the requested one on that date.
	var bestInstant, bestWall time.Time
	for offset := range offsets {
		candidate := targetWall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if !sameLocalDate(local, year, month, day) {
			continue
		}
		wall := time.Date(year, month, day, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
		if wall.Before(targetWall) {
			continue
		}
		if bestWall.IsZero() || wall.Before(bestWall) || (wall.Equal(bestWall) && candidate.Before(bestInstant)) {
			bestInstant, bestWall = candidate, wall
		}
	}
	if !bestInstant.IsZero() {
		return bestInstant.UTC()
	}
	return time.Date(year, month, day, startHour, 0, 0, 0, location).UTC()
}

type zoneTransition struct {
	at     time.Time
	before int
	after  int
}

func findOffsetTransition(low, high time.Time, oldOffset int, location *time.Location) time.Time {
	lowUnix, highUnix := low.Unix(), high.Unix()
	for highUnix-lowUnix > 1 {
		middle := lowUnix + (highUnix-lowUnix)/2
		_, offset := time.Unix(middle, 0).In(location).Zone()
		if offset == oldOffset {
			lowUnix = middle
		} else {
			highUnix = middle
		}
	}
	return time.Unix(highUnix, 0).UTC()
}

func sameLocalDate(local time.Time, year int, month time.Month, day int) bool {
	localYear, localMonth, localDay := local.Date()
	return localYear == year && localMonth == month && localDay == day
}

func calendarDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
}

func calendarDateFromLocal(value time.Time, location *time.Location) time.Time {
	year, month, day := value.In(location).Date()
	return calendarDate(year, month, day)
}

func addCalendarDays(date time.Time, days int) time.Time {
	year, month, day := date.UTC().Date()
	return calendarDate(year, month, day+days)
}

func addCalendarMonths(date time.Time, months int) time.Time {
	year, month, _ := date.UTC().Date()
	return calendarDate(year, month+time.Month(months), 1)
}

func databaseTimeBoundary(value time.Time) string {
	// modernc SQLite stores time.Time values in UTC using time.Time.String(),
	// whose sortable prefix is YYYY-MM-DD HH:MM:SS. Omitting the suffix here
	// keeps direct text comparisons indexable and correctly includes/excludes
	// fractional timestamps at an exact second boundary.
	return value.UTC().Format("2006-01-02 15:04:05")
}

func groupActivityRows(rows []gen.ListStatsActivityRangeRow, buckets []dateBucket) []ActivityDay {
	days := make([]ActivityDay, len(buckets))
	for index, bucket := range buckets {
		days[index].Date = bucket.label
	}
	bucketIndex := 0
	for _, row := range rows {
		reviewedAt := row.ReviewedAt.UTC()
		for bucketIndex < len(buckets) && !reviewedAt.Before(buckets[bucketIndex].end) {
			bucketIndex++
		}
		if bucketIndex == len(buckets) {
			break
		}
		if reviewedAt.Before(buckets[bucketIndex].start) {
			continue
		}
		days[bucketIndex].Reviews++
		days[bucketIndex].DurationMs += row.DurationMs
	}
	return days
}

func unixTime(value time.Time) int64 { return value.UTC().Unix() }

func nullableFloatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	copy := value.Float64
	return &copy
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time.UTC()
	return &copy
}

func max64(value int64, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil && s.clock != nil
}

func (s *Service) cached(key string, destination any, load func() (any, error)) error {
	if !s.ready() {
		return ErrInternal
	}
	now := s.now()
	s.cacheMu.Lock()
	if entry, ok := s.cache[key]; ok {
		if now.Before(entry.expiresAt) {
			data := append([]byte(nil), entry.data...)
			s.cacheMu.Unlock()
			return json.Unmarshal(data, destination)
		}
		delete(s.cache, key)
	}
	s.cacheMu.Unlock()

	value, err := load()
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ErrInternal
	}
	completedAt := s.now()
	s.cacheMu.Lock()
	for cacheKey, entry := range s.cache {
		if !completedAt.Before(entry.expiresAt) {
			delete(s.cache, cacheKey)
		}
	}
	if len(s.cache) >= maxCacheItems {
		var oldestKey string
		var oldest time.Time
		for cacheKey, entry := range s.cache {
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey, oldest = cacheKey, entry.expiresAt
			}
		}
		delete(s.cache, oldestKey)
	}
	s.cache[key] = cacheEntry{data: append([]byte(nil), data...), expiresAt: completedAt.Add(cacheTTL)}
	s.cacheMu.Unlock()
	return json.Unmarshal(data, destination)
}
