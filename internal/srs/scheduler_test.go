package srs

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type goldenFile struct {
	Format     string           `json:"format"`
	Parameters goldenParameters `json:"parameters"`
	Sequences  []goldenSequence `json:"sequences"`
}

type goldenParameters struct {
	RequestRetention *float64 `json:"request_retention"`
	MaximumInterval  *uint64  `json:"maximum_interval"`
	EnableFuzz       *bool    `json:"enable_fuzz"`
	Weights          *Weights `json:"weights"`
	LearningSteps    []string `json:"learning_steps"`
	RelearningSteps  []string `json:"relearning_steps"`
}

type goldenSequence struct {
	Name       string           `json:"name"`
	Parameters goldenParameters `json:"parameters"`
	Initial    goldenCard       `json:"initial"`
	Reviews    []goldenReview   `json:"reviews"`
}

type goldenReview struct {
	At        string          `json:"at"`
	Rating    Rating          `json:"rating"`
	Expected  goldenCard      `json:"expected"`
	ReviewLog goldenReviewLog `json:"review_log"`
}

type goldenCard struct {
	State         string  `json:"state"`
	Due           string  `json:"due"`
	Stability     float64 `json:"stability"`
	Difficulty    float64 `json:"difficulty"`
	ElapsedDays   uint64  `json:"elapsed_days"`
	ScheduledDays uint64  `json:"scheduled_days"`
	Step          uint64  `json:"step"`
	Reps          uint64  `json:"reps"`
	Lapses        uint64  `json:"lapses"`
	LastReview    string  `json:"last_review"`
}

type goldenReviewLog struct {
	State         string `json:"state"`
	ScheduledDays uint64 `json:"scheduled_days"`
	ElapsedDays   uint64 `json:"elapsed_days"`
	ReviewedAt    string `json:"reviewed_at"`
}

func TestGoldenSequences(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fsrs_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var weightShape struct {
		Parameters struct {
			Weights []float64 `json:"weights"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &weightShape); err != nil {
		t.Fatalf("decode golden weight vector: %v", err)
	}
	if len(weightShape.Parameters.Weights) != 19 {
		t.Fatalf("golden weights has length %d, want official go-fsrs/v3 length 19", len(weightShape.Parameters.Weights))
	}
	var vectors goldenFile
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("decode golden vectors: %v", err)
	}
	if vectors.Format != "retain/fsrs-golden/v1" {
		t.Fatalf("unexpected golden format %q", vectors.Format)
	}
	if vectors.Parameters.Weights == nil || *vectors.Parameters.Weights != DefaultParameters().Weights {
		t.Fatal("golden weights do not match official go-fsrs/v3 default weights")
	}
	baseParameters, err := vectors.Parameters.apply(DefaultParameters())
	if err != nil {
		t.Fatalf("parse golden default parameters: %v", err)
	}
	for _, vector := range vectors.Sequences {
		t.Run(vector.Name, func(t *testing.T) {
			parameters, err := vector.Parameters.apply(baseParameters)
			if err != nil {
				t.Fatal(err)
			}
			scheduler, err := NewScheduler(parameters)
			if err != nil {
				t.Fatal(err)
			}
			card := vector.Initial.card(t)
			for i, review := range vector.Reviews {
				now := parseTime(t, review.At)
				nextCard, log := scheduler.Review(card, review.Rating, now)
				card = nextCard
				assertCard(t, card, review.Expected)
				if got, want := log.Rating, review.Rating; got != want {
					t.Errorf("review %d rating = %d, want %d", i, got, want)
				}
				if got, want := stateName(log.State), review.ReviewLog.State; got != want {
					t.Errorf("review %d log state = %q, want %q", i, got, want)
				}
				if got, want := log.ScheduledDays, review.ReviewLog.ScheduledDays; got != want {
					t.Errorf("review %d log scheduled days = %d, want %d", i, got, want)
				}
				if got, want := log.ElapsedDays, review.ReviewLog.ElapsedDays; got != want {
					t.Errorf("review %d log elapsed days = %d, want %d", i, got, want)
				}
				if got, want := log.ReviewedAt, parseTime(t, review.ReviewLog.ReviewedAt); !got.Equal(want) {
					t.Errorf("review %d log reviewed at = %s, want %s", i, got, want)
				}
			}
		})
	}
}

func TestPreviewContainsAllRatingsAndMatchesReview(t *testing.T) {
	parameters := DefaultParameters()
	parameters.EnableFuzz = true
	scheduler, err := NewScheduler(parameters)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	card := Card{State: Review, Stability: 24, Difficulty: 6, Reps: 8, LastReview: now.Add(-30 * 24 * time.Hour)}
	first := scheduler.Preview(card, now)
	second := scheduler.Preview(card, now)
	if len(first) != 4 {
		t.Fatalf("Preview returned %d ratings, want 4", len(first))
	}
	for _, rating := range []Rating{Again, Hard, Good, Easy} {
		got, ok := first[rating]
		if !ok {
			t.Fatalf("Preview omitted rating %d", rating)
		}
		if want := second[rating]; !reflect.DeepEqual(got, want) {
			t.Errorf("fuzzed preview for rating %d is not deterministic", rating)
		}
		reviewed, _ := scheduler.Review(card, rating, now)
		if !reflect.DeepEqual(got, reviewed) {
			t.Errorf("Preview rating %d differs from Review", rating)
		}
	}
}

func TestReviewUsesSuppliedClockAndPreservesLocation(t *testing.T) {
	scheduler, err := NewScheduler(DefaultParameters())
	if err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("test-offset", 3*60*60)
	now := time.Date(2025, 2, 3, 4, 5, 6, 0, location)
	card, log := scheduler.Review(Card{State: New}, Good, now)
	wantDue := now.Add(10 * time.Minute)
	if !card.Due.Equal(wantDue) || card.Due.Location() != location {
		t.Errorf("due = %s (%s), want %s (%s)", card.Due, card.Due.Location(), wantDue, location)
	}
	if !card.LastReview.Equal(now) || !log.ReviewedAt.Equal(now) {
		t.Errorf("review time was not bound to supplied clock: card=%s log=%s now=%s", card.LastReview, log.ReviewedAt, now)
	}
}

func TestPlanDefaultLearningStepDurations(t *testing.T) {
	scheduler, err := NewScheduler(DefaultParameters())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	newPreview := scheduler.Preview(Card{State: New}, now)
	assertPreviewMatchesReview(t, scheduler, Card{State: New}, now)
	for rating, duration := range map[Rating]time.Duration{Again: time.Minute, Hard: 5*time.Minute + 30*time.Second, Good: 10 * time.Minute} {
		if got := newPreview[rating].Due.Sub(now); got != duration {
			t.Errorf("new-card rating %d interval = %s, want %s", rating, got, duration)
		}
	}
	if newPreview[Good].Step != 1 {
		t.Errorf("new-card Good step = %d, want 1", newPreview[Good].Step)
	}

	learning, _ := scheduler.Review(Card{State: New}, Again, now)
	learningPreview := scheduler.Preview(learning, learning.Due)
	for rating, duration := range map[Rating]time.Duration{Again: time.Minute, Hard: 5*time.Minute + 30*time.Second, Good: 10 * time.Minute} {
		if got := learningPreview[rating].Due.Sub(learning.Due); got != duration {
			t.Errorf("learning-card rating %d interval = %s, want %s", rating, got, duration)
		}
	}
	if learningPreview[Good].State != Learning || learningPreview[Good].Step != 1 {
		t.Error("Good should advance the first Learning step to the second")
	}
	finalStepPreview := scheduler.Preview(learningPreview[Good], learningPreview[Good].Due)
	if finalStepPreview[Good].State != Review || finalStepPreview[Easy].State != Review {
		t.Error("Good at the final step and Easy should graduate the card to Review")
	}
}

func TestParseStepsAndValidation(t *testing.T) {
	steps, err := ParseSteps(" 1m\t10m ")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(steps, []time.Duration{time.Minute, 10 * time.Minute}) {
		t.Fatalf("ParseSteps = %v, want [1m 10m]", steps)
	}
	if empty, err := ParseSteps(" \t "); err != nil || len(empty) != 0 {
		t.Fatalf("ParseSteps(empty) = %v, %v, want empty without error", empty, err)
	}
	for _, value := range []string{"1m nope", "0s", "-1m", "10m 1m", "1m 1m"} {
		if _, err := ParseSteps(value); err == nil {
			t.Errorf("ParseSteps(%q) succeeded, want validation error", value)
		}
	}
	parameters := DefaultParameters()
	parameters.LearningSteps = []time.Duration{time.Minute, 0}
	if _, err := NewScheduler(parameters); err == nil {
		t.Fatal("NewScheduler accepted a non-positive custom step")
	}
}

func TestEmptyStepFieldsUsePlanDefaults(t *testing.T) {
	scheduler, err := NewScheduler(Parameters{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 4, 5, 6, 7, 8, 0, time.UTC)
	newPreview := scheduler.Preview(Card{State: New}, now)
	if got := newPreview[Again].Due.Sub(now); got != time.Minute {
		t.Errorf("default first learning step = %s, want 1m", got)
	}
	if got := newPreview[Good].Due.Sub(now); got != 10*time.Minute {
		t.Errorf("default Good due = %s, want 10m", got)
	}
	if newPreview[Good].Step != 1 {
		t.Errorf("default new Good step = %d, want 1", newPreview[Good].Step)
	}
	reviewCard := Card{State: Review, Stability: 20, Difficulty: 5, LastReview: now.Add(-24 * time.Hour)}
	relearning := scheduler.Preview(reviewCard, now)[Again]
	if got := relearning.Due.Sub(now); got != 10*time.Minute {
		t.Errorf("default first relearning step = %s, want 10m", got)
	}
}

func TestCustomStepTransitionsSharePreviewAndReviewLogic(t *testing.T) {
	parameters := DefaultParameters()
	parameters.LearningSteps = []time.Duration{2 * time.Minute, 8 * time.Minute, 30 * time.Minute}
	parameters.RelearningSteps = []time.Duration{3 * time.Minute, 12 * time.Minute}
	scheduler, err := NewScheduler(parameters)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	newPreview := scheduler.Preview(Card{State: New}, now)
	if got := newPreview[Hard].Due.Sub(now); got != 5*time.Minute {
		t.Errorf("custom Hard interval = %s, want midpoint 5m", got)
	}
	if newPreview[Hard].Step != 0 {
		t.Errorf("Hard changed first step to %d, want 0", newPreview[Hard].Step)
	}
	for _, rating := range []Rating{Again, Hard, Good, Easy} {
		card, _ := scheduler.Review(Card{State: New}, rating, now)
		if !reflect.DeepEqual(card, newPreview[rating]) {
			t.Errorf("Preview and Review differ for New rating %d", rating)
		}
	}
	good, _ := scheduler.Review(Card{State: New}, Good, now)
	if good.State != Learning || good.Step != 1 || !good.Due.Equal(now.Add(8*time.Minute)) {
		t.Errorf("New Good transition = state %d step %d due %s; want Learning step 1 at +8m", good.State, good.Step, good.Due)
	}
	secondGood, _ := scheduler.Review(good, Good, good.Due)
	if secondGood.State != Learning || secondGood.Step != 2 || !secondGood.Due.Equal(good.Due.Add(30*time.Minute)) {
		t.Errorf("second Good transition = state %d step %d due %s; want Learning step 2 at +30m", secondGood.State, secondGood.Step, secondGood.Due)
	}
	assertPreviewMatchesReview(t, scheduler, secondGood, secondGood.Due)
	lastStepPreview := scheduler.Preview(secondGood, secondGood.Due)
	if lastStepPreview[Hard].State != Learning || lastStepPreview[Hard].Step != 2 || !lastStepPreview[Hard].Due.Equal(secondGood.Due.Add(45*time.Minute)) {
		t.Errorf("last-step Hard should retain step 2 and schedule +45m: %+v", lastStepPreview[Hard])
	}
	again, _ := scheduler.Review(secondGood, Again, secondGood.Due)
	if again.State != Learning || again.Step != 0 || !again.Due.Equal(secondGood.Due.Add(2*time.Minute)) {
		t.Errorf("Again did not return to first custom step: %+v", again)
	}

	reviewCard := Card{State: Review, Stability: 20, Difficulty: 6, LastReview: now.Add(-30 * 24 * time.Hour)}
	relearning := scheduler.Preview(reviewCard, now)[Again]
	assertPreviewMatchesReview(t, scheduler, reviewCard, now)
	if relearning.State != Relearning || relearning.Step != 0 || !relearning.Due.Equal(now.Add(3*time.Minute)) {
		t.Errorf("Review Again transition = %+v, want Relearning step 0 at +3m", relearning)
	}
	advanced := scheduler.Preview(relearning, relearning.Due)[Good]
	if advanced.State != Relearning || advanced.Step != 1 || !advanced.Due.Equal(relearning.Due.Add(12*time.Minute)) {
		t.Errorf("Relearning Good transition = %+v, want step 1 at +12m", advanced)
	}
	assertPreviewMatchesReview(t, scheduler, relearning, relearning.Due)
	relearningAgain, _ := scheduler.Review(advanced, Again, advanced.Due)
	if relearningAgain.State != Relearning || relearningAgain.Step != 0 || !relearningAgain.Due.Equal(advanced.Due.Add(3*time.Minute)) {
		t.Errorf("Relearning Again did not return to first custom step: %+v", relearningAgain)
	}
	assertPreviewMatchesReview(t, scheduler, advanced, advanced.Due)
	graduated := scheduler.Preview(advanced, advanced.Due)[Good]
	if graduated.State != Review || graduated.Step != 0 {
		t.Errorf("final Relearning Good did not graduate: %+v", graduated)
	}
}

func assertPreviewMatchesReview(t *testing.T, scheduler Scheduler, card Card, now time.Time) {
	t.Helper()
	preview := scheduler.Preview(card, now)
	for _, rating := range []Rating{Again, Hard, Good, Easy} {
		got, _ := scheduler.Review(card, rating, now)
		if !reflect.DeepEqual(preview[rating], got) {
			t.Errorf("Preview and Review differ for state %d, step %d, rating %d", card.State, card.Step, rating)
		}
	}
}

func TestMaximumIntervalAppliesToEveryPreviewRating(t *testing.T) {
	parameters := DefaultParameters()
	parameters.MaximumInterval = 3
	scheduler, err := NewScheduler(parameters)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	card := Card{State: Review, Stability: 2500, Difficulty: 5, Reps: 4, LastReview: now.Add(-100 * 24 * time.Hour)}
	for rating, next := range scheduler.Preview(card, now) {
		if next.ScheduledDays > parameters.MaximumInterval {
			t.Errorf("rating %d scheduled %d days beyond maximum %d", rating, next.ScheduledDays, parameters.MaximumInterval)
		}
		if next.State == Review && !next.Due.Equal(now.Add(time.Duration(next.ScheduledDays)*24*time.Hour)) {
			t.Errorf("rating %d due time %s does not match %d scheduled days", rating, next.Due, next.ScheduledDays)
		}
	}
}

func TestRetrievabilityEdgeCases(t *testing.T) {
	scheduler, err := NewScheduler(DefaultParameters())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := scheduler.Retrievability(Card{State: New}, now); got != 0 {
		t.Errorf("new-card retrievability = %g, want 0", got)
	}
	if got := scheduler.Retrievability(Card{State: Review}, now); got != 0 {
		t.Errorf("zero-stability retrievability = %g, want 0", got)
	}
	if got := scheduler.Retrievability(Card{State: Review, Stability: 5}, now); got != 0 {
		t.Errorf("missing-last-review retrievability = %g, want 0", got)
	}
	card := Card{State: Review, Stability: 5, Difficulty: 5, LastReview: now}
	if got := scheduler.Retrievability(card, now); math.Abs(got-1) > 1e-12 {
		t.Errorf("retrievability at last review = %g, want 1", got)
	}
	if got := scheduler.Retrievability(card, now.Add(-time.Second)); got != 0 {
		t.Errorf("retrievability before last review = %g, want 0", got)
	}
}

func TestNewSchedulerValidatesParameters(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Parameters)
	}{
		{name: "retention nan", change: func(p *Parameters) { p.RequestRetention = math.NaN() }},
		{name: "retention out of range", change: func(p *Parameters) { p.RequestRetention = 1 }},
		{name: "infinite weight", change: func(p *Parameters) { p.Weights[0] = math.Inf(1) }},
		{name: "interval overflows time.Duration", change: func(p *Parameters) { p.MaximumInterval = maxDurationDays + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parameters := DefaultParameters()
			test.change(&parameters)
			if _, err := NewScheduler(parameters); err == nil {
				t.Fatal("NewScheduler succeeded with invalid parameters")
			}
		})
	}
	if _, err := NewScheduler(Parameters{}); err != nil {
		t.Fatalf("zero-valued settings should select defaults: %v", err)
	}
}

func TestReviewPanicsOnInvalidRating(t *testing.T) {
	scheduler, err := NewScheduler(DefaultParameters())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Review did not reject an invalid rating")
		}
	}()
	scheduler.Review(Card{State: New}, Rating(0), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
}

func TestReviewRejectsOutOfOrderTime(t *testing.T) {
	scheduler, err := NewScheduler(DefaultParameters())
	if err != nil {
		t.Fatal(err)
	}
	lastReview := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	defer func() {
		if recover() == nil {
			t.Fatal("Review accepted a time before LastReview")
		}
	}()
	scheduler.Review(Card{State: Review, Stability: 5, Difficulty: 5, LastReview: lastReview}, Good, lastReview.Add(-time.Second))
}

func (p goldenParameters) apply(base Parameters) (Parameters, error) {
	if p.RequestRetention != nil {
		base.RequestRetention = *p.RequestRetention
	}
	if p.MaximumInterval != nil {
		base.MaximumInterval = *p.MaximumInterval
	}
	if p.EnableFuzz != nil {
		base.EnableFuzz = *p.EnableFuzz
	}
	if p.Weights != nil {
		base.Weights = *p.Weights
	}
	if len(p.LearningSteps) != 0 {
		steps, err := ParseSteps(strings.Join(p.LearningSteps, " "))
		if err != nil {
			return Parameters{}, err
		}
		base.LearningSteps = steps
	}
	if len(p.RelearningSteps) != 0 {
		steps, err := ParseSteps(strings.Join(p.RelearningSteps, " "))
		if err != nil {
			return Parameters{}, err
		}
		base.RelearningSteps = steps
	}
	return base, nil
}

func (g goldenCard) card(t *testing.T) Card {
	t.Helper()
	state, err := parseState(g.State)
	if err != nil {
		t.Fatal(err)
	}
	return Card{
		State:         state,
		Due:           parseOptionalTime(t, g.Due),
		Stability:     g.Stability,
		Difficulty:    g.Difficulty,
		ElapsedDays:   g.ElapsedDays,
		ScheduledDays: g.ScheduledDays,
		Step:          g.Step,
		Reps:          g.Reps,
		Lapses:        g.Lapses,
		LastReview:    parseOptionalTime(t, g.LastReview),
	}
}

func assertCard(t *testing.T, got Card, want goldenCard) {
	t.Helper()
	if stateName(got.State) != want.State {
		t.Errorf("state = %q, want %q", stateName(got.State), want.State)
	}
	if expected := parseOptionalTime(t, want.Due); !got.Due.Equal(expected) {
		t.Errorf("due = %s, want %s", got.Due, expected)
	}
	if math.Abs(got.Stability-want.Stability) > 1e-6 {
		t.Errorf("stability = %.12g, want %.12g", got.Stability, want.Stability)
	}
	if math.Abs(got.Difficulty-want.Difficulty) > 1e-6 {
		t.Errorf("difficulty = %.12g, want %.12g", got.Difficulty, want.Difficulty)
	}
	if got.ElapsedDays != want.ElapsedDays {
		t.Errorf("elapsed days = %d, want %d", got.ElapsedDays, want.ElapsedDays)
	}
	if got.ScheduledDays != want.ScheduledDays {
		t.Errorf("scheduled days = %d, want %d", got.ScheduledDays, want.ScheduledDays)
	}
	if got.Step != want.Step {
		t.Errorf("step = %d, want %d", got.Step, want.Step)
	}
	if got.Reps != want.Reps {
		t.Errorf("reps = %d, want %d", got.Reps, want.Reps)
	}
	if got.Lapses != want.Lapses {
		t.Errorf("lapses = %d, want %d", got.Lapses, want.Lapses)
	}
	if expected := parseOptionalTime(t, want.LastReview); !got.LastReview.Equal(expected) {
		t.Errorf("last review = %s, want %s", got.LastReview, expected)
	}
}

func stateName(state State) string {
	return [...]string{"new", "learning", "review", "relearning"}[state]
}

func parseState(value string) (State, error) {
	for state := New; state <= Relearning; state++ {
		if stateName(state) == value {
			return state, nil
		}
	}
	return 0, errors.New("unknown FSRS state " + value)
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func parseOptionalTime(t *testing.T, value string) time.Time {
	t.Helper()
	if value == "" {
		return time.Time{}
	}
	return parseTime(t, value)
}
