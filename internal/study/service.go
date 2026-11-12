// Package study implements the user-scoped review queue and append-only review
// workflow. Scheduling transitions are delegated to internal/srs.
package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/lang"
	"github.com/pestit/retain/internal/srs"
	"github.com/pestit/retain/internal/studyday"
)

var (
	ErrInvalidInput      = errors.New("invalid study input")
	ErrNotFound          = errors.New("study resource not found")
	ErrConflict          = errors.New("study id conflicts with another review")
	ErrChoiceUnavailable = errors.New("multiple-choice options are unavailable")
	ErrInternal          = errors.New("internal study service error")
)

const maxQueueLimit = 500
const maxQueueExcludedCards = 10000

// Service provides transactional, user-scoped study operations.
type Service struct {
	database *db.DB
	clock    clock.Clock
}

func NewService(database *db.DB, c clock.Clock) *Service {
	if c == nil {
		c = clock.Real{}
	}
	return &Service{database: database, clock: c}
}

// Card is the scheduling state returned for a study card.
type Card struct {
	ID            int64      `json:"id"`
	NoteID        int64      `json:"note_id"`
	DeckID        int64      `json:"deck_id,omitempty"`
	Direction     string     `json:"direction"`
	State         srs.State  `json:"state"`
	Due           time.Time  `json:"due"`
	Stability     *float64   `json:"stability,omitempty"`
	Difficulty    *float64   `json:"difficulty,omitempty"`
	ElapsedDays   uint64     `json:"elapsed_days"`
	ScheduledDays uint64     `json:"scheduled_days"`
	Step          uint64     `json:"step"`
	Reps          uint64     `json:"reps"`
	Lapses        uint64     `json:"lapses"`
	LastReview    *time.Time `json:"last_review,omitempty"`
}

// Preview describes one possible rating transition.
type Preview struct {
	State         srs.State `json:"state"`
	Due           time.Time `json:"due"`
	ScheduledDays uint64    `json:"scheduled_days"`
	Interval      string    `json:"interval"`
}

// QueueCard includes prompt text and per-rating interval previews.
type QueueCard struct {
	Card
	Front    string                 `json:"front"`
	Back     string                 `json:"back"`
	Previews map[srs.Rating]Preview `json:"previews"`
}

// AnswerCard contains the owner-scoped note and direction for an active card.
type AnswerCard struct {
	NoteID    int64
	Direction string
}

// CheckAnswerInput contains the card identifier and learner-entered response.
// Expected answers and language metadata are always resolved by the server.
type CheckAnswerInput struct {
	CardID int64  `json:"card_id"`
	Answer string `json:"answer"`
	Mode   string `json:"mode,omitempty"`
}

// AnswerResult reports a match classification and a suggested FSRS rating.
// It does not create a review; the learner still chooses a rating explicitly.
type AnswerResult struct {
	Match           string     `json:"match"`
	SuggestedRating srs.Rating `json:"suggested_rating"`
}

// ReviewInput contains the client-generated UUIDv7 and answer metadata.
type ReviewInput struct {
	ID         string     `json:"id"`
	CardID     int64      `json:"card_id"`
	Rating     srs.Rating `json:"rating"`
	Mode       string     `json:"mode"`
	ReviewedAt time.Time  `json:"reviewed_at"`
	DurationMS int64      `json:"duration_ms"`
}

type userOptions struct {
	scheduler       srs.Scheduler
	schedulerConfig string
	newPerDay       int64
	maxReviews      int64
	timezone        *time.Location
	dayStartHour    int
}

// Queue returns due cards in Review, Learning/Relearning, New order. A parent
// deck includes all active descendants; daily counters are user-wide.
func (s *Service) Queue(ctx context.Context, userID int64, deckID *int64, limit int) ([]QueueCard, error) {
	return s.QueueExcluding(ctx, userID, deckID, limit, nil)
}

// QueueExcluding returns a batch of due cards while skipping IDs already
// present in the caller's active session. Exclusions are applied before daily
// limits so a continuation batch does not consume allowance for prior cards.
func (s *Service) QueueExcluding(ctx context.Context, userID int64, deckID *int64, limit int, excludedCardIDs []int64) ([]QueueCard, error) {
	if !s.ready() || userID <= 0 {
		return nil, ErrInternal
	}
	if limit <= 0 || limit > maxQueueLimit || (deckID != nil && *deckID <= 0) || len(excludedCardIDs) > maxQueueExcludedCards {
		return nil, ErrInvalidInput
	}
	excluded := make(map[int64]struct{}, len(excludedCardIDs))
	for _, cardID := range excludedCardIDs {
		if cardID <= 0 {
			return nil, ErrInvalidInput
		}
		if _, duplicate := excluded[cardID]; duplicate {
			return nil, ErrInvalidInput
		}
		excluded[cardID] = struct{}{}
	}

	tx, queries, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if deckID != nil {
		if _, err := queries.GetStudyDeck(ctx, gen.GetStudyDeckParams{DeckID: *deckID, UserID: userID}); errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, ErrInternal
		}
	}
	settings, err := queries.GetUserSettings(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrInternal
	}
	options, err := optionsFromSettings(settings)
	if err != nil {
		return nil, ErrInternal
	}
	now := s.now()
	dayStart, nextDay, err := userDayBounds(now, options.timezone, options.dayStartHour)
	if err != nil {
		return nil, ErrInternal
	}
	counts, err := queries.GetDailyReviewCounts(ctx, gen.GetDailyReviewCountsParams{
		UserID: userID, ReviewedAt: dayStart, ReviewedAt2: nextDay,
	})
	if err != nil {
		return nil, ErrInternal
	}
	newCount, okNew := integerCount(counts.NewCount)
	reviewCount, okReviews := integerCount(counts.ReviewCount)
	if !okNew || !okReviews {
		return nil, ErrInternal
	}
	rows, err := queries.ListStudyCards(ctx, gen.ListStudyCardsParams{
		UserID: userID, DueBefore: now, DeckID: nullableInt64(deckID),
	})
	if err != nil {
		return nil, ErrInternal
	}

	reviewRemaining := remaining(options.maxReviews, reviewCount)
	newRemaining := remaining(options.newPerDay, newCount)
	result := make([]QueueCard, 0, limit)
	for _, row := range rows {
		if _, skip := excluded[row.CardID]; skip {
			continue
		}
		card, err := srsCardFromQueueRow(row)
		if err != nil {
			return nil, ErrInternal
		}
		if card.State == srs.Review && int64(len(result)) >= reviewRemaining {
			continue
		}
		if card.State == srs.New && newRemaining <= 0 {
			continue
		}
		item := QueueCard{Card: publicCardFromQueueRow(row), Previews: make(map[srs.Rating]Preview, 4)}
		if row.Direction == "reverse" {
			item.Front, item.Back = row.Translation, row.Term
		} else {
			item.Front, item.Back = row.Term, row.Translation
		}
		for rating, next := range options.scheduler.Preview(card, now) {
			item.Previews[rating] = Preview{
				State: next.State, Due: next.Due.UTC(), ScheduledDays: next.ScheduledDays,
				Interval: next.Due.Sub(now).String(),
			}
		}
		result = append(result, item)
		if card.State == srs.New {
			newRemaining--
		}
		if len(result) == limit {
			break
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrInternal
	}
	return result, nil
}

// Review applies a rating, appends an idempotent log row, replays the active
// history in chronological order, and stores the resulting card state.
func (s *Service) Review(ctx context.Context, userID int64, input ReviewInput) (Card, error) {
	if !s.ready() || userID <= 0 {
		return Card{}, ErrInternal
	}
	if !validUUIDv7(input.ID) || input.CardID <= 0 || !input.Rating.Valid() ||
		!validMode(input.Mode) || input.ReviewedAt.IsZero() || input.DurationMS < 0 {
		return Card{}, ErrInvalidInput
	}

	tx, queries, err := s.begin(ctx)
	if err != nil {
		return Card{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, err := queries.GetReviewLog(ctx, input.ID); err == nil {
		if !reviewLogMatchesInput(existing, userID, input) {
			return Card{}, ErrConflict
		}
		current, err := queries.GetStudyCard(ctx, gen.GetStudyCardParams{ID: input.CardID, UserID: userID})
		if errors.Is(err, sql.ErrNoRows) {
			return Card{}, ErrNotFound
		}
		if err != nil {
			return Card{}, ErrInternal
		}
		if err := tx.Commit(); err != nil {
			return Card{}, ErrInternal
		}
		return publicCard(current, 0), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrInternal
	}

	current, err := queries.GetStudyCard(ctx, gen.GetStudyCardParams{ID: input.CardID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, ErrInternal
	}
	if current.Suspended {
		return Card{}, ErrInvalidInput
	}
	settings, err := queries.GetUserSettings(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, ErrInternal
	}
	options, err := optionsFromSettings(settings)
	if err != nil {
		return Card{}, ErrInternal
	}
	logs, err := queries.ListActiveReviewLogs(ctx, gen.ListActiveReviewLogsParams{CardID: input.CardID, UserID: userID})
	if err != nil {
		return Card{}, ErrInternal
	}
	proposed := gen.ReviewLog{
		ID: input.ID, CardID: input.CardID, UserID: userID,
		Rating: int64(input.Rating), ReviewedAt: input.ReviewedAt.UTC(),
		SchedulerParameters: sql.NullString{String: options.schedulerConfig, Valid: true},
	}
	sequence := append(append([]gen.ReviewLog(nil), logs...), proposed)
	sortReviewLogs(sequence)
	_, metadata, found := replay(sequence, input.ID, options)
	if !found {
		return Card{}, ErrInternal
	}
	if input.Mode == "choice" && metadata.stateBefore != srs.New {
		return Card{}, ErrInvalidInput
	}
	if _, err := queries.InsertReviewLog(ctx, gen.InsertReviewLogParams{
		ID: input.ID, CardID: input.CardID, UserID: userID, Rating: int64(input.Rating),
		Mode: input.Mode, StateBefore: int64(metadata.stateBefore),
		StabilityBefore: metadata.stabilityBefore, DifficultyBefore: metadata.difficultyBefore,
		StabilityAfter: metadata.cardAfter.Stability, DifficultyAfter: metadata.cardAfter.Difficulty,
		ScheduledDays:       int64(metadata.cardAfter.ScheduledDays),
		SchedulerParameters: sql.NullString{String: options.schedulerConfig, Valid: true},
		ReviewedAt:          input.ReviewedAt.UTC(),
		ReceivedAt:          s.now(), DurationMs: input.DurationMS,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Handle the narrow race where another transaction inserted the UUID first.
			existing, lookupErr := queries.GetReviewLog(ctx, input.ID)
			if lookupErr != nil || !reviewLogMatchesInput(existing, userID, input) {
				return Card{}, ErrConflict
			}
			current, lookupErr := queries.GetStudyCard(ctx, gen.GetStudyCardParams{ID: input.CardID, UserID: userID})
			if lookupErr != nil {
				return Card{}, ErrInternal
			}
			if err := tx.Commit(); err != nil {
				return Card{}, ErrInternal
			}
			return publicCard(current, 0), nil
		}
		return Card{}, ErrInternal
	}

	activeLogs, err := queries.ListActiveReviewLogs(ctx, gen.ListActiveReviewLogsParams{CardID: input.CardID, UserID: userID})
	if err != nil {
		return Card{}, ErrInternal
	}
	finalCard, metadataByLog, ok := replayWithMetadata(activeLogs, options)
	if !ok {
		return Card{}, ErrInternal
	}
	if err := s.persistReviewLogProjection(ctx, queries, userID, input.CardID, activeLogs, metadataByLog); err != nil {
		return Card{}, err
	}
	updated, err := queries.UpdateStudyCard(ctx, cardUpdateParams(input.CardID, userID, finalCard, s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return Card{}, ErrInternal
	}
	return publicCard(updated, 0), nil
}

func reviewLogMatchesInput(existing gen.ReviewLog, userID int64, input ReviewInput) bool {
	return existing.UserID == userID &&
		existing.CardID == input.CardID &&
		existing.Rating == int64(input.Rating) &&
		existing.Mode == input.Mode &&
		existing.ReviewedAt.UTC().Equal(input.ReviewedAt.UTC()) &&
		existing.DurationMs == input.DurationMS
}

// CheckAnswer compares an answer with server-side note and deck data. It never
// trusts language, direction, expected answers, or article policy from client input.
func (s *Service) CheckAnswer(ctx context.Context, userID int64, input CheckAnswerInput) (AnswerResult, error) {
	if !s.ready() || userID <= 0 {
		return AnswerResult{}, ErrInternal
	}
	if input.CardID <= 0 || !utf8.ValidString(input.Answer) || strings.TrimSpace(input.Answer) == "" ||
		(input.Mode != "" && input.Mode != "typing" && input.Mode != "choice") {
		return AnswerResult{}, ErrInvalidInput
	}
	if input.Mode == "" {
		input.Mode = "typing"
	}

	row, err := s.database.Queries().GetStudyAnswerCard(ctx, gen.GetStudyAnswerCardParams{
		CardID: input.CardID, UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return AnswerResult{}, ErrNotFound
	}
	if err != nil {
		return AnswerResult{}, ErrInternal
	}
	if input.Mode == "choice" && (row.State != int64(srs.New) || row.Suspended) {
		return AnswerResult{}, ErrChoiceUnavailable
	}
	answerInput, err := answerInputForRow(row, input.Answer)
	if err != nil {
		return AnswerResult{}, err
	}
	if input.Mode == "choice" {
		correct := strings.TrimSpace(strings.Split(answerInput.Expected, ";")[0])
		if input.Answer != correct {
			return AnswerResult{Match: lang.MatchWrong, SuggestedRating: srs.Again}, nil
		}
		return AnswerResult{Match: lang.MatchExact, SuggestedRating: srs.Good}, nil
	}
	result, err := lang.MatchAnswer(answerInput)
	if err != nil {
		return AnswerResult{}, ErrInternal
	}
	return AnswerResult{Match: result.Match, SuggestedRating: srs.Rating(result.SuggestedRating)}, nil
}

const choiceDistractorPageSize int64 = 128

// ResolveAnswerCard looks up an active card owned by userID and returns its
// note and actual direction. Missing, deleted, and other users' cards are
// reported uniformly as ErrNotFound.
func (s *Service) ResolveAnswerCard(ctx context.Context, userID, cardID int64) (AnswerCard, error) {
	if !s.ready() || userID <= 0 {
		return AnswerCard{}, ErrInternal
	}
	if cardID <= 0 || ctx == nil {
		return AnswerCard{}, ErrInvalidInput
	}
	row, err := s.database.Queries().GetStudyAnswerCard(ctx, gen.GetStudyAnswerCardParams{
		CardID: cardID,
		UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return AnswerCard{}, ErrNotFound
	}
	if err != nil {
		return AnswerCard{}, ErrInternal
	}
	return AnswerCard{NoteID: row.NoteID, Direction: row.Direction}, nil
}

// ChoiceOptions returns one correct answer and three distinct distractors from
// the note's actual deck and card direction. Only active New cards can use it.
func (s *Service) ChoiceOptions(ctx context.Context, userID, cardID int64) ([]string, error) {
	if !s.ready() || userID <= 0 {
		return nil, ErrInternal
	}
	if cardID <= 0 {
		return nil, ErrInvalidInput
	}
	queries := s.database.Queries()
	row, err := queries.GetStudyAnswerCard(ctx, gen.GetStudyAnswerCardParams{CardID: cardID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrInternal
	}
	if row.State != int64(srs.New) || row.Suspended {
		return nil, ErrChoiceUnavailable
	}
	answerInput, err := answerInputForRow(row, "")
	if err != nil {
		return nil, err
	}
	correct := strings.TrimSpace(strings.Split(answerInput.Expected, ";")[0])
	if correct == "" {
		return nil, ErrChoiceUnavailable
	}
	matcher, err := lang.NewAnswerMatcher(lang.AnswerMatcherInput{
		Language: answerInput.Language, PartOfSpeech: answerInput.PartOfSpeech, Expected: answerInput.Expected,
		Alternatives: answerInput.Alternatives, GermanArticle: answerInput.GermanArticle,
		WrongArticleRating: answerInput.WrongArticleRating,
	})
	if err != nil {
		return nil, ErrInternal
	}

	distractors := make([]string, 0, 3)
	seen := map[string]struct{}{choiceIdentity(correct): {}}
	var afterNoteID int64
	considerCandidate := func(candidate string) (bool, error) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return true, nil
		}
		match, matchErr := matcher.MatchExactOrArticle(candidate)
		if matchErr != nil {
			return false, matchErr
		}
		if match.Match == lang.MatchExact || match.Match == lang.MatchArticle {
			return true, nil
		}
		identity := choiceIdentity(candidate)
		if _, exists := seen[identity]; exists {
			return true, nil
		}
		seen[identity] = struct{}{}
		distractors = append(distractors, candidate)
		return len(distractors) < 3, nil
	}

collectDistractors:
	for len(distractors) < 3 {
		rows, queryErr := queries.ListStudyChoiceDistractors(ctx, gen.ListStudyChoiceDistractorsParams{
			UserID: userID, Direction: row.Direction, DeckID: row.DeckID, NoteID: row.NoteID,
			AfterNoteID: afterNoteID, PageSize: choiceDistractorPageSize,
		})
		if queryErr != nil {
			return nil, ErrInternal
		}
		if len(rows) == 0 {
			break
		}
		for _, candidateRow := range rows {
			afterNoteID = candidateRow.NoteID
			more, candidateErr := forEachChoiceVariant(candidateRow.Answer, considerCandidate)
			if candidateErr != nil {
				return nil, ErrInternal
			}
			if !more {
				break collectDistractors
			}
			if row.Direction == "forward" {
				more, candidateErr = forEachChoiceAlternative(candidateRow.Alternatives, considerCandidate)
				if candidateErr != nil {
					return nil, ErrInternal
				}
				if !more {
					break collectDistractors
				}
			}
		}
		if len(rows) < int(choiceDistractorPageSize) {
			break
		}
	}
	if len(distractors) < 3 {
		return nil, ErrChoiceUnavailable
	}
	rand.Shuffle(len(distractors), func(i, j int) {
		distractors[i], distractors[j] = distractors[j], distractors[i]
	})
	choices := append([]string{correct}, distractors[:3]...)
	rand.Shuffle(len(choices), func(i, j int) {
		choices[i], choices[j] = choices[j], choices[i]
	})
	return choices, nil
}

func forEachChoiceVariant(value string, visit func(string) (bool, error)) (bool, error) {
	for {
		candidate, remainder, found := strings.Cut(value, ";")
		more, err := visit(candidate)
		if err != nil || !more {
			return more, err
		}
		if !found {
			return true, nil
		}
		value = remainder
	}
}

func forEachChoiceAlternative(raw string, visit func(string) (bool, error)) (bool, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	delimiter, isDelimiter := token.(json.Delim)
	if err != nil || !isDelimiter || delimiter != '[' {
		return false, fmt.Errorf("invalid choice alternatives array")
	}
	for decoder.More() {
		var candidate string
		if err := decoder.Decode(&candidate); err != nil {
			return false, err
		}
		more, err := visit(candidate)
		if err != nil || !more {
			return more, err
		}
	}
	token, err = decoder.Token()
	delimiter, isDelimiter = token.(json.Delim)
	if err != nil || !isDelimiter || delimiter != ']' {
		return false, fmt.Errorf("invalid choice alternatives array")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return false, fmt.Errorf("unexpected trailing choice alternatives data")
		}
		return false, err
	}
	return true, nil
}

func answerInputForRow(row gen.GetStudyAnswerCardRow, answer string) (lang.AnswerInput, error) {
	language := row.LangNative
	expected := row.Translation
	var alternatives []string
	if err := json.Unmarshal([]byte(row.Alternatives), &alternatives); err != nil {
		return lang.AnswerInput{}, ErrInternal
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal([]byte(row.Fields), &fields); err != nil {
		return lang.AnswerInput{}, ErrInternal
	}
	article := ""
	partOfSpeech := ""
	if raw, exists := fields["article"]; exists {
		if err := json.Unmarshal(raw, &article); err != nil {
			return lang.AnswerInput{}, ErrInternal
		}
	}
	if row.Direction == "reverse" {
		language = row.LangTarget
		expected = row.Term
		alternatives = nil // alternatives describe translations, not terms.
		if raw, exists := fields["pos"]; exists {
			// Only a valid string POS can authorize the planned English infinitive
			// normalization; unknown free-form field values remain untouched.
			_ = json.Unmarshal(raw, &partOfSpeech)
		}
		if language == "de" && article == "" {
			article = inferGermanArticle(expected)
		}
	} else if row.Direction != "forward" {
		return lang.AnswerInput{}, ErrInternal
	} else {
		article = "" // fields.article describes the target term, not its translation.
	}
	if language == "" {
		return lang.AnswerInput{}, ErrInvalidInput
	}
	article = strings.ToLower(strings.TrimSpace(article))
	if language != "de" {
		article = ""
	}
	wrongArticleRating := srs.Hard
	if row.WrongArticleRating == int64(srs.Again) {
		wrongArticleRating = srs.Again
	}
	return lang.AnswerInput{
		Language: language, PartOfSpeech: partOfSpeech,
		Expected: expected, Alternatives: alternatives, Answer: answer,
		GermanArticle: article, WrongArticleRating: int(wrongArticleRating),
	}, nil
}

func choiceIdentity(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func inferGermanArticle(term string) string {
	words := strings.Fields(term)
	if len(words) < 2 {
		return ""
	}
	article := strings.ToLower(strings.Trim(words[0], ".,;:!?\"'()[]{}"))
	switch article {
	case "der", "die", "das":
		return article
	default:
		return ""
	}
}

// Undo marks a review as undone and rebuilds its card from all remaining active
// logs. Repeating an undo is idempotent.
func (s *Service) Undo(ctx context.Context, userID int64, reviewID string) (Card, error) {
	if !s.ready() || userID <= 0 {
		return Card{}, ErrInternal
	}
	if !validUUIDv7(reviewID) {
		return Card{}, ErrInvalidInput
	}
	tx, queries, err := s.begin(ctx)
	if err != nil {
		return Card{}, err
	}
	defer func() { _ = tx.Rollback() }()

	log, err := queries.GetReviewLog(ctx, reviewID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && log.UserID != userID) {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, ErrInternal
	}
	if _, err := queries.GetStudyCard(ctx, gen.GetStudyCardParams{ID: log.CardID, UserID: userID}); errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	} else if err != nil {
		return Card{}, ErrInternal
	}
	if log.Undone {
		current, err := queries.GetStudyCard(ctx, gen.GetStudyCardParams{ID: log.CardID, UserID: userID})
		if err != nil {
			return Card{}, ErrInternal
		}
		if err := tx.Commit(); err != nil {
			return Card{}, ErrInternal
		}
		return publicCard(current, 0), nil
	}
	if _, err := queries.MarkReviewLogUndone(ctx, gen.MarkReviewLogUndoneParams{ID: reviewID, UserID: userID}); err != nil {
		return Card{}, ErrInternal
	}
	settings, err := queries.GetUserSettings(ctx, userID)
	if err != nil {
		return Card{}, ErrInternal
	}
	options, err := optionsFromSettings(settings)
	if err != nil {
		return Card{}, ErrInternal
	}
	activeLogs, err := queries.ListActiveReviewLogs(ctx, gen.ListActiveReviewLogsParams{CardID: log.CardID, UserID: userID})
	if err != nil {
		return Card{}, ErrInternal
	}
	finalCard, metadataByLog, ok := replayWithMetadata(activeLogs, options)
	if !ok {
		return Card{}, ErrInternal
	}
	if err := s.persistReviewLogProjection(ctx, queries, userID, log.CardID, activeLogs, metadataByLog); err != nil {
		return Card{}, err
	}
	if len(activeLogs) == 0 {
		finalCard.Due = s.now()
	}
	updated, err := queries.UpdateStudyCard(ctx, cardUpdateParams(log.CardID, userID, finalCard, s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return Card{}, ErrInternal
	}
	return publicCard(updated, 0), nil
}

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil && s.clock != nil
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

func (s *Service) begin(ctx context.Context) (*sql.Tx, *gen.Queries, error) {
	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, ErrInternal
	}
	return tx, s.database.Queries().WithTx(tx), nil
}

func optionsFromSettings(settings gen.UserSetting) (userOptions, error) {
	location, err := time.LoadLocation(settings.Timezone)
	if err != nil || settings.DayStartHour < 0 || settings.DayStartHour > 23 {
		return userOptions{}, fmt.Errorf("%w: invalid user time zone settings", ErrInternal)
	}
	learning, err := srs.ParseSteps(settings.LearningSteps)
	if err != nil {
		return userOptions{}, fmt.Errorf("%w: invalid learning steps", ErrInternal)
	}
	relearning, err := srs.ParseSteps(settings.RelearningSteps)
	if err != nil {
		return userOptions{}, fmt.Errorf("%w: invalid relearning steps", ErrInternal)
	}
	parameters := srs.DefaultParameters()
	parameters.RequestRetention = settings.RequestRetention
	parameters.LearningSteps = learning
	parameters.RelearningSteps = relearning
	if settings.MaximumInterval <= 0 {
		return userOptions{}, fmt.Errorf("%w: invalid maximum interval", ErrInternal)
	}
	parameters.MaximumInterval = uint64(settings.MaximumInterval)
	if settings.FsrsWeights.Valid {
		var weights []float64
		if err := json.Unmarshal([]byte(settings.FsrsWeights.String), &weights); err != nil {
			return userOptions{}, fmt.Errorf("%w: invalid FSRS weights", ErrInternal)
		}
		if len(weights) != 17 && len(weights) != len(parameters.Weights) {
			return userOptions{}, fmt.Errorf("%w: invalid FSRS weight count", ErrInternal)
		}
		copy(parameters.Weights[:], weights)
	}
	scheduler, err := srs.NewScheduler(parameters)
	if err != nil {
		return userOptions{}, fmt.Errorf("%w: invalid FSRS parameters", ErrInternal)
	}
	schedulerConfig, err := json.Marshal(parameters)
	if err != nil {
		return userOptions{}, fmt.Errorf("%w: encode FSRS parameters", ErrInternal)
	}
	return userOptions{
		scheduler: scheduler, schedulerConfig: string(schedulerConfig),
		newPerDay: settings.NewPerDay, maxReviews: settings.MaxReviewsPerDay,
		timezone: location, dayStartHour: int(settings.DayStartHour),
	}, nil
}

func userDayBounds(now time.Time, location *time.Location, startHour int) (time.Time, time.Time, error) {
	start, end, ok := studyday.Bounds(now, location, startHour)
	if !ok {
		return time.Time{}, time.Time{}, ErrInvalidInput
	}
	return start, end, nil
}

func integerCount(value interface{}) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case int:
		return int64(number), true
	default:
		return 0, false
	}
}

func remaining(limit, used int64) int64 {
	if used >= limit {
		return 0
	}
	return limit - used
}

func validMode(mode string) bool { return mode == "flip" || mode == "typing" || mode == "choice" }

func validUUIDv7(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 7 && id.Variant() == uuid.RFC4122 && value == id.String()
}

// ValidReviewID reports whether value is a canonical RFC 9562 UUIDv7 review ID.
func ValidReviewID(value string) bool { return validUUIDv7(value) }

type replayMetadata struct {
	stateBefore      srs.State
	stabilityBefore  sql.NullFloat64
	difficultyBefore sql.NullFloat64
	cardAfter        srs.Card
}

func replay(logs []gen.ReviewLog, targetID string, options userOptions) (srs.Card, replayMetadata, bool) {
	card, metadata, ok := replayWithMetadata(logs, options)
	if !ok {
		return srs.Card{}, replayMetadata{}, false
	}
	for index, log := range logs {
		if log.ID == targetID {
			return card, metadata[index], true
		}
	}
	return card, replayMetadata{}, targetID == ""
}

func replayWithMetadata(logs []gen.ReviewLog, options userOptions) (srs.Card, []replayMetadata, bool) {
	card := srs.Card{State: srs.New}
	metadata := make([]replayMetadata, len(logs))
	for index, log := range logs {
		before := card
		rating := srs.Rating(log.Rating)
		if !rating.Valid() {
			return srs.Card{}, nil, false
		}
		scheduler := options.scheduler
		if log.SchedulerParameters.Valid && log.SchedulerParameters.String != "" {
			var parameters srs.Parameters
			if err := json.Unmarshal([]byte(log.SchedulerParameters.String), &parameters); err != nil {
				return srs.Card{}, nil, false
			}
			var err error
			scheduler, err = srs.NewScheduler(parameters)
			if err != nil {
				return srs.Card{}, nil, false
			}
		}
		next, _ := scheduler.Review(before, rating, log.ReviewedAt)
		metadata[index] = replayMetadata{
			stateBefore:      before.State,
			stabilityBefore:  nullableFloat(before.Stability),
			difficultyBefore: nullableFloat(before.Difficulty),
			cardAfter:        next,
		}
		card = next
	}
	return card, metadata, true
}

func (s *Service) persistReviewLogProjection(
	ctx context.Context,
	queries *gen.Queries,
	userID, cardID int64,
	logs []gen.ReviewLog,
	metadata []replayMetadata,
) error {
	if len(logs) != len(metadata) {
		return ErrInternal
	}
	if err := queries.DeleteReviewLogProjectionsForCard(ctx, gen.DeleteReviewLogProjectionsForCardParams{
		UserID: userID, CardID: cardID,
	}); err != nil {
		return ErrInternal
	}
	for index, log := range logs {
		derived := metadata[index]
		if err := queries.UpsertReviewLogProjection(ctx, gen.UpsertReviewLogProjectionParams{
			ReviewID: log.ID, CardID: cardID, UserID: userID, StateBefore: int64(derived.stateBefore),
			StabilityBefore: derived.stabilityBefore, DifficultyBefore: derived.difficultyBefore,
			StabilityAfter: derived.cardAfter.Stability, DifficultyAfter: derived.cardAfter.Difficulty,
			ScheduledDays: int64(derived.cardAfter.ScheduledDays),
		}); err != nil {
			return ErrInternal
		}
	}
	return nil
}

func sortReviewLogs(logs []gen.ReviewLog) {
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].ReviewedAt.Equal(logs[j].ReviewedAt) {
			return logs[i].ID < logs[j].ID
		}
		return logs[i].ReviewedAt.Before(logs[j].ReviewedAt)
	})
}

func srsCardFromQueueRow(row gen.ListStudyCardsRow) (srs.Card, error) {
	card := srs.Card{
		State: srs.State(row.State), Due: row.Due, ElapsedDays: uint64(row.ElapsedDays),
		ScheduledDays: uint64(row.ScheduledDays), Step: uint64(row.Step), Reps: uint64(row.Reps),
		Lapses: uint64(row.Lapses),
	}
	if row.State < int64(srs.New) || row.State > int64(srs.Relearning) || row.ElapsedDays < 0 || row.ScheduledDays < 0 || row.Step < 0 || row.Reps < 0 || row.Lapses < 0 {
		return srs.Card{}, ErrInternal
	}
	if row.Stability.Valid {
		card.Stability = row.Stability.Float64
	}
	if row.Difficulty.Valid {
		card.Difficulty = row.Difficulty.Float64
	}
	if row.LastReview.Valid {
		card.LastReview = row.LastReview.Time
	}
	if card.State != srs.New && (card.Stability <= 0 || card.Difficulty < 1 || card.Difficulty > 10 || card.LastReview.IsZero()) {
		return srs.Card{}, ErrInternal
	}
	return card, nil
}

func publicCardFromQueueRow(row gen.ListStudyCardsRow) Card {
	return Card{
		ID: row.CardID, NoteID: row.NoteID, DeckID: row.DeckID, Direction: row.Direction,
		State: srs.State(row.State), Due: row.Due.UTC(), Stability: floatPointer(row.Stability),
		Difficulty: floatPointer(row.Difficulty), ElapsedDays: uint64(row.ElapsedDays),
		ScheduledDays: uint64(row.ScheduledDays), Step: uint64(row.Step), Reps: uint64(row.Reps),
		Lapses: uint64(row.Lapses), LastReview: timePointer(row.LastReview),
	}
}

func cardUpdateParams(cardID, userID int64, card srs.Card, updatedAt time.Time) gen.UpdateStudyCardParams {
	return gen.UpdateStudyCardParams{
		State: int64(card.State), Due: normalizedDue(card.Due, updatedAt),
		Stability: nullableFloat(card.Stability), Difficulty: nullableFloat(card.Difficulty),
		ElapsedDays: int64(card.ElapsedDays), ScheduledDays: int64(card.ScheduledDays),
		Step: int64(card.Step), Reps: int64(card.Reps), Lapses: int64(card.Lapses),
		LastReview: nullableTime(card.LastReview), UpdatedAt: updatedAt,
		CardID: cardID, UserID: userID,
	}
}

func normalizedDue(due, fallback time.Time) time.Time {
	if due.IsZero() {
		return fallback
	}
	return due.UTC()
}

func publicCard(card gen.Card, deckID int64) Card {
	return Card{
		ID: card.ID, NoteID: card.NoteID, DeckID: deckID, Direction: card.Direction,
		State: srs.State(card.State), Due: card.Due.UTC(), Stability: floatPointer(card.Stability),
		Difficulty: floatPointer(card.Difficulty), ElapsedDays: uint64(card.ElapsedDays),
		ScheduledDays: uint64(card.ScheduledDays), Step: uint64(card.Step), Reps: uint64(card.Reps),
		Lapses: uint64(card.Lapses), LastReview: timePointer(card.LastReview),
	}
}

func nullableFloat(value float64) sql.NullFloat64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: value, Valid: true}
}

func floatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	copy := value.Float64
	return &copy
}

func nullableTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}

func timePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time.UTC()
	return &copy
}

func nullableInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}
