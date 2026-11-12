// Package srs adapts the official go-fsrs implementation to Retain's card
// model. Scheduling time is always supplied by the caller; this package never
// reads the wall clock.
package srs

import (
	"fmt"
	"math"
	"strings"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
)

// Rating is the grade assigned to a review. Values intentionally match the
// public API and go-fsrs: Again=1, Hard=2, Good=3, Easy=4.
type Rating int8

const (
	Again Rating = iota + 1
	Hard
	Good
	Easy
)

func (r Rating) Valid() bool { return r >= Again && r <= Easy }

// State is the lifecycle state of a card.
type State int8

const (
	New State = iota
	Learning
	Review
	Relearning
)

// Card stores the scheduling data needed to replay reviews. Step is the
// zero-based index into the active learning or relearning steps.
type Card struct {
	Due           time.Time `json:"due"`
	Stability     float64   `json:"stability"`
	Difficulty    float64   `json:"difficulty"`
	ElapsedDays   uint64    `json:"elapsed_days"`
	ScheduledDays uint64    `json:"scheduled_days"`
	Step          uint64    `json:"step"`
	Reps          uint64    `json:"reps"`
	Lapses        uint64    `json:"lapses"`
	State         State     `json:"state"`
	LastReview    time.Time `json:"last_review"`
}

// ReviewLog is the scheduling metadata emitted for a single answer. State is
// the card state before the review, matching go-fsrs' ReviewLog.State.
type ReviewLog struct {
	Rating        Rating    `json:"rating"`
	State         State     `json:"state"`
	ScheduledDays uint64    `json:"scheduled_days"`
	ElapsedDays   uint64    `json:"elapsed_days"`
	ReviewedAt    time.Time `json:"reviewed_at"`
}

// Weights contains the 19 FSRS model parameters.
type Weights [19]float64

// Parameters contains the user-configurable FSRS and short-term scheduling
// settings. Empty step slices select the PLAN.md defaults; other zero-valued
// FSRS fields select the library defaults. EnableFuzz's zero value disables
// fuzz.
type Parameters struct {
	RequestRetention float64         `json:"request_retention"`
	Weights          Weights         `json:"weights"`
	MaximumInterval  uint64          `json:"maximum_interval"`
	EnableFuzz       bool            `json:"enable_fuzz"`
	LearningSteps    []time.Duration `json:"learning_steps,omitempty"`
	RelearningSteps  []time.Duration `json:"relearning_steps,omitempty"`
}

var (
	defaultLearningSteps   = []time.Duration{time.Minute, 10 * time.Minute}
	defaultRelearningSteps = []time.Duration{10 * time.Minute}
)

// DefaultParameters returns the PLAN.md step defaults and official go-fsrs
// defaults (0.9 retention, default weights, 36500-day maximum, fuzz disabled).
func DefaultParameters() Parameters {
	defaults := fsrs.DefaultParam()
	return Parameters{
		RequestRetention: defaults.RequestRetention,
		Weights:          Weights(defaults.W),
		MaximumInterval:  uint64(defaults.MaximumInterval),
		EnableFuzz:       defaults.EnableFuzz,
		LearningSteps:    append([]time.Duration(nil), defaultLearningSteps...),
		RelearningSteps:  append([]time.Duration(nil), defaultRelearningSteps...),
	}
}

// ParseSteps parses a whitespace-separated list of positive, strictly
// increasing Go durations such as "1m 10m". Empty input returns nil so a
// Parameters value can inherit the PLAN.md defaults in NewScheduler.
func ParseSteps(value string) ([]time.Duration, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil, nil
	}
	steps := make([]time.Duration, 0, len(fields))
	for _, field := range fields {
		step, err := time.ParseDuration(field)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", field, err)
		}
		steps = append(steps, step)
	}
	if err := validateSteps("steps", steps); err != nil {
		return nil, err
	}
	return steps, nil
}

// Scheduler applies FSRS transitions with explicit caller-supplied times.
type Scheduler interface {
	Preview(card Card, now time.Time) map[Rating]Card
	Review(card Card, rating Rating, now time.Time) (Card, ReviewLog)
	Retrievability(card Card, now time.Time) float64
}

// fsrsScheduler is safe for concurrent use: each operation creates an
// isolated library scheduler because go-fsrs/v3 stores its fuzz seed in
// Parameters.
type fsrsScheduler struct {
	parameters Parameters
	native     fsrs.Parameters
}

var _ Scheduler = (*fsrsScheduler)(nil)

// NewScheduler validates parameters and returns an FSRS scheduler.
func NewScheduler(parameters Parameters) (Scheduler, error) {
	defaults := DefaultParameters()
	if parameters.RequestRetention == 0 {
		parameters.RequestRetention = defaults.RequestRetention
	}
	if parameters.MaximumInterval == 0 {
		parameters.MaximumInterval = defaults.MaximumInterval
	}
	if parameters.Weights == (Weights{}) {
		parameters.Weights = defaults.Weights
	}
	if len(parameters.LearningSteps) == 0 {
		parameters.LearningSteps = defaults.LearningSteps
	} else {
		parameters.LearningSteps = append([]time.Duration(nil), parameters.LearningSteps...)
	}
	if len(parameters.RelearningSteps) == 0 {
		parameters.RelearningSteps = defaults.RelearningSteps
	} else {
		parameters.RelearningSteps = append([]time.Duration(nil), parameters.RelearningSteps...)
	}

	if math.IsNaN(parameters.RequestRetention) || math.IsInf(parameters.RequestRetention, 0) || parameters.RequestRetention <= 0 || parameters.RequestRetention >= 1 {
		return nil, fmt.Errorf("request retention must be finite and between 0 and 1")
	}
	if parameters.MaximumInterval > maxDurationDays {
		return nil, fmt.Errorf("maximum interval must be at most %d days", maxDurationDays)
	}
	for i, weight := range parameters.Weights {
		if math.IsNaN(weight) || math.IsInf(weight, 0) {
			return nil, fmt.Errorf("weight %d must be finite", i)
		}
	}
	if err := validateSteps("learning_steps", parameters.LearningSteps); err != nil {
		return nil, err
	}
	if err := validateSteps("relearning_steps", parameters.RelearningSteps); err != nil {
		return nil, err
	}

	native := fsrs.DefaultParam()
	native.RequestRetention = parameters.RequestRetention
	native.MaximumInterval = float64(parameters.MaximumInterval)
	native.W = fsrs.Weights(parameters.Weights)
	native.EnableFuzz = parameters.EnableFuzz
	return &fsrsScheduler{parameters: parameters, native: native}, nil
}

func validateSteps(name string, steps []time.Duration) error {
	for i, step := range steps {
		if step <= 0 {
			return fmt.Errorf("%s[%d] must be a positive duration", name, i)
		}
		if i > 0 && step <= steps[i-1] {
			return fmt.Errorf("%s must be strictly increasing", name)
		}
	}
	return nil
}

const maxDurationDays = uint64(math.MaxInt64 / int64(24*time.Hour))

// Preview returns the resulting card for each valid rating. It is equivalent
// to four Review calls using the same input card and time.
func (s *fsrsScheduler) Preview(card Card, now time.Time) map[Rating]Card {
	s.validateCard(card, now)
	preview := make(map[Rating]Card, 4)
	for _, rating := range []Rating{Again, Hard, Good, Easy} {
		preview[rating], _ = s.transition(card, rating, now)
	}
	return preview
}

// Review applies one rating and returns the updated card and append-only log
// data. rating must be one of Again, Hard, Good, or Easy; invalid ratings and
// invalid card states are programmer/input errors and panic.
func (s *fsrsScheduler) Review(card Card, rating Rating, now time.Time) (Card, ReviewLog) {
	s.validateCard(card, now)
	if !rating.Valid() {
		panic(fmt.Sprintf("invalid FSRS rating %d", rating))
	}
	return s.transition(card, rating, now)
}

func (s *fsrsScheduler) transition(card Card, rating Rating, now time.Time) (Card, ReviewLog) {
	info := fsrs.NewFSRS(s.native).Next(toNativeCard(card), now, fsrs.Rating(rating))
	result := s.fromNativeCard(card, info.Card, now)
	log := ReviewLog{
		Rating:        rating,
		State:         State(info.ReviewLog.State),
		ScheduledDays: info.ReviewLog.ScheduledDays,
		ElapsedDays:   info.ReviewLog.ElapsedDays,
		ReviewedAt:    info.ReviewLog.Review,
	}

	switch card.State {
	case New:
		switch rating {
		case Again:
			scheduleStep(&result, Learning, 0, s.parameters.LearningSteps[0], now)
		case Hard:
			scheduleStep(&result, Learning, 0, hardStepInterval(s.parameters.LearningSteps, 0), now)
		case Good:
			// A new card starts at the first step. Good advances it to the
			// second step when present, matching the library's default 10m
			// first Good interval for the PLAN.md 1m/10m steps.
			step := uint64(0)
			if len(s.parameters.LearningSteps) > 1 {
				step = 1
			}
			scheduleStep(&result, Learning, step, s.parameters.LearningSteps[step], now)
		case Easy:
			result.Step = 0
		}
	case Learning, Relearning:
		steps := s.stepsFor(card.State)
		switch rating {
		case Again:
			scheduleStep(&result, card.State, 0, steps[0], now)
		case Hard:
			scheduleStep(&result, card.State, card.Step, hardStepInterval(steps, int(card.Step)), now)
		case Good:
			if card.Step+1 < uint64(len(steps)) {
				nextStep := card.Step + 1
				scheduleStep(&result, card.State, nextStep, steps[nextStep], now)
			}
			// At the final step, keep go-fsrs' Good stability/difficulty and
			// long-term interval unchanged; its Learning transition graduates.
		case Easy:
			result.Step = 0
		}
	case Review:
		if rating == Again {
			scheduleStep(&result, Relearning, 0, s.parameters.RelearningSteps[0], now)
		} else {
			result.Step = 0
		}
	}
	return result, log
}

func (s *fsrsScheduler) stepsFor(state State) []time.Duration {
	if state == Relearning {
		return s.parameters.RelearningSteps
	}
	return s.parameters.LearningSteps
}

func scheduleStep(card *Card, state State, step uint64, interval time.Duration, now time.Time) {
	card.State = state
	card.Step = step
	card.ScheduledDays = uint64(interval / (24 * time.Hour))
	card.Due = now.Add(interval)
}

// hardStepInterval places Hard midway to the next step. At the last step,
// where there is no next short-term step, it uses 1.5 times the current step.
func hardStepInterval(steps []time.Duration, step int) time.Duration {
	current := steps[step]
	if step+1 < len(steps) {
		next := steps[step+1]
		return current + (next-current)/2
	}
	half := current / 2
	if current > time.Duration(math.MaxInt64)-half {
		return time.Duration(math.MaxInt64)
	}
	return current + half
}

// Retrievability returns the current recall probability. New cards and cards
// without a valid stability or prior review time have no computable
// probability and return 0.
func (s *fsrsScheduler) Retrievability(card Card, now time.Time) float64 {
	if card.State == New || !validState(card.State) || !finitePositive(card.Stability) || card.LastReview.IsZero() || now.Before(card.LastReview) {
		return 0
	}
	return fsrs.NewFSRS(s.native).GetRetrievability(toNativeCard(card), now)
}

func (s *fsrsScheduler) validateCard(card Card, now time.Time) {
	if !validState(card.State) {
		panic(fmt.Sprintf("invalid FSRS card state %d", card.State))
	}
	if card.State != New {
		if !finitePositive(card.Stability) {
			panic("non-new FSRS cards must have finite positive stability")
		}
		if math.IsNaN(card.Difficulty) || math.IsInf(card.Difficulty, 0) || card.Difficulty < 1 || card.Difficulty > 10 {
			panic("non-new FSRS cards must have finite difficulty from 1 to 10")
		}
		if card.LastReview.IsZero() {
			panic("non-new FSRS cards must have a last review time")
		}
		if now.Before(card.LastReview) {
			panic("FSRS review time must not precede the card's last review")
		}
		if (card.State == Learning || card.State == Relearning) && card.Step >= uint64(len(s.stepsFor(card.State))) {
			panic("learning card step is outside its configured steps")
		}
	}
}

func validState(state State) bool { return state >= New && state <= Relearning }

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func toNativeCard(card Card) fsrs.Card {
	return fsrs.Card{
		Due:           card.Due,
		Stability:     card.Stability,
		Difficulty:    card.Difficulty,
		ElapsedDays:   card.ElapsedDays,
		ScheduledDays: card.ScheduledDays,
		Reps:          card.Reps,
		Lapses:        card.Lapses,
		State:         fsrs.State(card.State),
		LastReview:    card.LastReview,
	}
}

func (s *fsrsScheduler) fromNativeCard(previous Card, next fsrs.Card, now time.Time) Card {
	result := Card{
		Due:           next.Due,
		Stability:     next.Stability,
		Difficulty:    next.Difficulty,
		ElapsedDays:   next.ElapsedDays,
		ScheduledDays: next.ScheduledDays,
		Step:          previous.Step,
		Reps:          next.Reps,
		Lapses:        next.Lapses,
		State:         State(next.State),
		LastReview:    next.LastReview,
	}
	if result.State == Review || result.State == New {
		result.Step = 0
	}

	// The library applies its maximum before enforcing Hard < Good < Easy.
	// At a low cap those ordering adjustments can exceed the configured limit;
	// enforce the persisted user setting on the final schedule as well.
	if result.ScheduledDays > s.parameters.MaximumInterval {
		result.ScheduledDays = s.parameters.MaximumInterval
		result.Due = now.Add(time.Duration(result.ScheduledDays) * 24 * time.Hour)
	}
	return result
}
