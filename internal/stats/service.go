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
	"github.com/pestit/retain/internal/studyday"
)

var (
	ErrInvalidInput = errors.New("invalid stats input")
	ErrNotFound     = errors.New("stats resource not found")
	ErrInternal     = errors.New("stats service error")
)

const (
	cacheTTL        = 5 * time.Minute
	maxCacheItems   = 128
	maxForecastDay  = 90
	streakChunkDays = 30
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
			NowAt: databaseTimeBoundary(now), NowUnix: unixTime(now), UserID: userID,
			DayStartUnix: unixTime(dayStart), DayEndUnix: unixTime(dayEnd),
		})
		if err != nil {
			return nil, ErrInternal
		}
		known, err := s.knownRightNow(ctx, userID, env.scheduler, now)
		if err != nil {
			return nil, err
		}
		streak, err := s.streak(ctx, userID, dayStart, now, env.location, env.dayStartHour)
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
		now := s.now()
		buckets := retentionBuckets(now, env.location, env.dayStartHour, period, periods)
		points := make([]RetentionPoint, 0, len(buckets))
		for _, bucket := range buckets {
			activity, err := s.activityBucket(ctx, userID, bucket, now)
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
	now := s.now()
	currentYear := now.In(env.location).Year()
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
			UserID: userID, NowAt: databaseTimeBoundary(now),
			StartAt: databaseTimeBoundary(buckets[0].start), EndAt: databaseTimeBoundary(buckets[len(buckets)-1].end),
		})
		if err != nil {
			return nil, ErrInternal
		}
		days := groupActivityRows(activityRows, buckets)
		growthBuckets := makeMonthBuckets(startDate, endDate, env.dayStartHour, env.location)
		growth := make([]VocabularyGrowth, 0)
		for _, bucket := range growthBuckets {
			rows, err := s.database.Queries().GetStatsVocabularyGrowth(ctx, gen.GetStatsVocabularyGrowthParams{
				UserID: userID, NowAt: databaseTimeBoundary(now),
				StartAt: databaseTimeBoundary(bucket.start), EndAt: databaseTimeBoundary(bucket.end),
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
		now := s.now()
		logs, err := queries.ListStatsCardCurveLogs(ctx, gen.ListStatsCardCurveLogsParams{
			CardID: cardID, UserID: userID,
		})
		if err != nil {
			return nil, ErrInternal
		}
		cardState, state, stability, difficulty, lastReview, scheduledDays, pastCount := curveCardSnapshot(card, logs, now)
		curve := Curve{
			CardID: cardID, Term: card.Term, Direction: card.Direction, State: state,
			Stability: stability, Difficulty: difficulty, LastReview: lastReview,
			Reviews: make([]CurveReview, 0, pastCount),
			Curve:   make([]CurvePoint, 0),
		}
		curve.Retrievability = env.scheduler.Retrievability(cardState, now)
		for index, log := range logs[:pastCount] {
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
			if scheduledEnd := cardState.LastReview.Add(time.Duration(scheduledDays) * 24 * time.Hour); scheduledEnd.After(end) {
				end = scheduledEnd
			}
			if end.Before(cardState.LastReview) {
				end = cardState.LastReview
			}
			span := end.Sub(cardState.LastReview)
			if span == 0 {
				pointCard := cardState
				pointCard.LastReview = cardState.LastReview
				curve.Curve = append(curve.Curve, CurvePoint{
					At:             cardState.LastReview.UTC(),
					Retrievability: env.scheduler.Retrievability(pointCard, cardState.LastReview),
				})
			} else {
				steps := int((span + 24*time.Hour - 1) / (24 * time.Hour))
				if steps > 90 {
					steps = 90
				}
				step := span / time.Duration(steps)
				for index := range steps {
					at := cardState.LastReview.Add(step * time.Duration(index))
					pointCard := cardState
					curve.Curve = append(curve.Curve, CurvePoint{
						At: at.UTC(), Retrievability: env.scheduler.Retrievability(pointCard, at),
					})
				}
				pointCard := cardState
				curve.Curve = append(curve.Curve, CurvePoint{
					At: end.UTC(), Retrievability: env.scheduler.Retrievability(pointCard, end),
				})
			}
		}
		return curve, nil
	})
	return result, err
}

func curveCardSnapshot(
	card gen.GetStatsCardCurveCardRow,
	logs []gen.ListStatsCardCurveLogsRow,
	now time.Time,
) (srs.Card, int64, *float64, *float64, *time.Time, int64, int) {
	pastCount := 0
	for pastCount < len(logs) && !logs[pastCount].ReviewedAt.After(now) {
		pastCount++
	}

	state := card.State
	stability := nullableFloatPointer(card.Stability)
	difficulty := nullableFloatPointer(card.Difficulty)
	lastReview := nullableTimePointer(card.LastReview)
	scheduledDays := max64(card.ScheduledDays, 0)
	if pastCount > 0 {
		latest := logs[pastCount-1]
		if pastCount < len(logs) {
			// Projection state_before of the next chronological event is the
			// exact state after the latest event at or before now.
			state = logs[pastCount].StateBefore
		}
		stabilityValue := latest.StabilityAfter
		difficultyValue := latest.DifficultyAfter
		stability = &stabilityValue
		difficulty = &difficultyValue
		last := latest.ReviewedAt.UTC()
		lastReview = &last
		scheduledDays = max64(latest.ScheduledDays, 0)
	} else if len(logs) > 0 {
		// With no review yet at now, the first future event still stores its
		// replay-derived before-state. Do not use its future last_review.
		state = logs[0].StateBefore
		stability = nullableFloatPointer(logs[0].StabilityBefore)
		difficulty = nullableFloatPointer(logs[0].DifficultyBefore)
		if lastReview != nil && lastReview.After(now) {
			lastReview = nil
		}
		scheduledDays = 0
	} else if lastReview != nil && lastReview.After(now) {
		lastReview = nil
	}

	cardState := srs.Card{State: srs.State(state), ScheduledDays: uint64(scheduledDays)}
	if stability != nil {
		cardState.Stability = *stability
	}
	if difficulty != nil {
		cardState.Difficulty = *difficulty
	}
	if lastReview != nil {
		cardState.LastReview = *lastReview
	}
	return cardState, state, stability, difficulty, lastReview, scheduledDays, pastCount
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
	rows, err := s.database.Queries().GetStatsRetrievabilityCards(ctx, gen.GetStatsRetrievabilityCardsParams{
		UserID: userID, NowAt: databaseTimeBoundary(now),
	})
	if err != nil {
		return 0, ErrInternal
	}
	known := 0.0
	for _, row := range rows {
		if row.State != int64(srs.Review) {
			continue
		}
		lastReview, err := parseDatabaseTime(row.LastReviewText)
		if err != nil {
			return 0, ErrInternal
		}
		known += scheduler.Retrievability(srs.Card{
			State: srs.Review, Stability: row.Stability, Difficulty: row.Difficulty,
			LastReview: lastReview, Due: row.Due,
			ElapsedDays: uint64(max64(row.ElapsedDays, 0)), ScheduledDays: uint64(max64(row.ScheduledDays, 0)),
			Step: uint64(max64(row.Step, 0)), Reps: uint64(max64(row.Reps, 0)), Lapses: uint64(max64(row.Lapses, 0)),
		}, now)
	}
	return known, nil
}

func (s *Service) streak(ctx context.Context, userID int64, currentStart, now time.Time, location *time.Location, dayStartHour int) (int, error) {
	currentDate := calendarDateFromLocal(currentStart, location)
	streak := 0
	started := false
	expectedDate := currentDate
	boundaryCache := make(map[string]time.Time)
	chunkEnd := addCalendarDays(currentDate, 1)
	for {
		chunkStart := addCalendarDays(chunkEnd, -streakChunkDays)
		rows, err := s.database.Queries().ListStatsReviewTimestamps(ctx, gen.ListStatsReviewTimestampsParams{
			UserID:  userID,
			StartAt: databaseTimeBoundary(calendarDayBoundary(chunkStart, dayStartHour, location)),
			EndAt:   databaseTimeBoundary(calendarDayBoundary(chunkEnd, dayStartHour, location)),
			NowAt:   databaseTimeBoundary(now),
		})
		if err != nil {
			return 0, ErrInternal
		}
		for _, value := range rows {
			reviewedAt := value.UTC()
			if reviewedAt.After(now) {
				continue
			}
			date := studyDate(reviewedAt, location, dayStartHour, boundaryCache)
			if date.After(currentDate) {
				continue
			}
			if !started {
				if date.Equal(currentDate) {
					started = true
					streak = 1
					expectedDate = addCalendarDays(currentDate, -1)
					continue
				}
				yesterday := addCalendarDays(currentDate, -1)
				if date.Equal(yesterday) {
					started = true
					streak = 1
					expectedDate = addCalendarDays(yesterday, -1)
					continue
				}
				if date.Before(yesterday) {
					return 0, nil
				}
				continue
			}
			if date.Equal(expectedDate) {
				streak++
				expectedDate = addCalendarDays(expectedDate, -1)
			} else if date.Before(expectedDate) {
				return streak, nil
			}
		}
		if !started || !expectedDate.Before(chunkStart) {
			return streak, nil
		}
		chunkEnd = chunkStart
	}
}

func studyDate(reviewedAt time.Time, location *time.Location, dayStartHour int, boundaryCache map[string]time.Time) time.Time {
	local := reviewedAt.In(location)
	date := calendarDateFromLocal(local, location)
	key := date.Format("2006-01-02")
	boundary, ok := boundaryCache[key]
	if !ok {
		boundary = calendarDayBoundary(date, dayStartHour, location)
		boundaryCache[key] = boundary
	}
	if local.Before(boundary) {
		return addCalendarDays(date, -1)
	}
	return date
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

func (s *Service) activityBucket(ctx context.Context, userID int64, bucket dateBucket, now time.Time) (activityAggregate, error) {
	row, err := s.database.Queries().GetStatsBucketsActivity(ctx, gen.GetStatsBucketsActivityParams{
		Label: bucket.label, UserID: userID, NowAt: databaseTimeBoundary(now),
		StartUnix: unixTime(bucket.start), EndUnix: unixTime(bucket.end),
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
	start, end, ok := studyday.Bounds(now, location, startHour)
	if !ok {
		return time.Time{}, time.Time{}
	}
	return start, end
}

func calendarDayBoundary(date time.Time, startHour int, location *time.Location) time.Time {
	return studyday.Boundary(date, startHour, location)
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
	// modernc SQLite stores UTC time.Time values in this exact format. For equal
	// seconds, the whole-second space sorts before a fractional dot, preserving
	// chronological text ordering including subsecond boundaries.
	return value.UTC().String()
}

func parseDatabaseTime(value string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", value)
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
