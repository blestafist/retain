package llmservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/jobs"
	"github.com/pestit/retain/internal/llmstore"
	"github.com/pestit/retain/internal/notes"
)

const (
	DeckEnrichJobKind       = "deck_enrich"
	MaxDeckEnrichCandidates = 100
	deckEnrichBatchSize     = 20
	deckEnrichPayloadMax    = 32 << 10
	deckEnrichResultMax     = 1 << 20
)

var (
	ErrDeckEnrichCandidateLimit = errors.New("deck enrichment candidate limit exceeded")
	ErrDeckEnrichQueueFull      = errors.New("deck enrichment queue limit reached")
	ErrDeckEnrichInternal       = errors.New("deck enrichment service error")
)

type deckEnrichDeckLister interface {
	List(context.Context, int64) ([]decks.Deck, error)
}

type deckEnrichNoteReader interface {
	List(context.Context, int64, int64) ([]notes.Note, error)
	Get(context.Context, int64, int64) (notes.Note, error)
}

type deckEnrichJobStore interface {
	CreateJob(context.Context, llmstore.Job) (llmstore.Job, error)
	UpdateJob(context.Context, llmstore.JobUpdate) (llmstore.Job, error)
}

type deckEnricher interface {
	EnrichForBulk(context.Context, int64, Request, string) (Response, error)
}

// DeckEnrichConfig contains trusted configuration used only by the background
// bulk workflow. The value is never read from HTTP input or job payloads.
type DeckEnrichConfig struct {
	FastModel string
}

// DeckEnrichService queues owner-scoped, read-only enrichment work for one deck.
// LLM calls happen only when its job handler is run by the background worker.
type DeckEnrichService struct {
	decks     deckEnrichDeckLister
	notes     deckEnrichNoteReader
	jobs      deckEnrichJobStore
	enricher  deckEnricher
	clock     clock.Clock
	fastModel string
}

// NewDeckEnrichService constructs the bulk enrichment workflow. FastModel is
// trusted server configuration; a blank value lets EnrichForBulk resolve the
// currently selected model. A nil clock selects the real UTC clock.
func NewDeckEnrichService(deckService deckEnrichDeckLister, noteService deckEnrichNoteReader, jobStore deckEnrichJobStore, enrichment deckEnricher, c clock.Clock, config DeckEnrichConfig) *DeckEnrichService {
	if c == nil {
		c = clock.Real{}
	}
	return &DeckEnrichService{
		decks: deckService, notes: noteService, jobs: jobStore, enricher: enrichment,
		clock: c, fastModel: strings.TrimSpace(config.FastModel),
	}
}

// Submit snapshots the IDs of active notes in an owned deck which currently
// lack at least one field supported by enrichment, then persists a queued job.
// It never calls the enricher or writes to notes.
func (s *DeckEnrichService) Submit(ctx context.Context, ownerID, deckID int64) (string, error) {
	if ctx == nil || ownerID <= 0 || deckID <= 0 {
		return "", ErrInvalidInput
	}
	if s == nil || s.decks == nil || s.notes == nil || s.jobs == nil || s.enricher == nil || s.clock == nil {
		return "", ErrDeckEnrichInternal
	}
	if enabled, ok := s.enricher.(interface{ Enabled() bool }); ok && !enabled.Enabled() {
		return "", ErrFeatureDisabled
	}

	deckTree, err := s.decks.List(ctx, ownerID)
	if err != nil {
		return "", ErrDeckEnrichInternal
	}
	if _, _, found := findDeckLanguagePair(deckTree, deckID); !found {
		return "", ErrDeckNotFound
	}
	deckNotes, err := s.notes.List(ctx, ownerID, deckID)
	if errors.Is(err, notes.ErrNotFound) {
		return "", ErrDeckNotFound
	}
	if err != nil {
		return "", ErrDeckEnrichInternal
	}

	noteIDs := make([]int64, 0, len(deckNotes))
	for _, note := range deckNotes {
		if len(missingDeckEnrichFields(note)) == 0 {
			continue
		}
		noteIDs = append(noteIDs, note.ID)
		if len(noteIDs) > MaxDeckEnrichCandidates {
			return "", ErrDeckEnrichCandidateLimit
		}
	}

	payload, err := json.Marshal(deckEnrichPayload{DeckID: deckID, NoteIDs: noteIDs})
	if err != nil {
		return "", ErrDeckEnrichInternal
	}
	jobID, err := randomJobID()
	if err != nil {
		return "", ErrDeckEnrichInternal
	}
	now := s.clock.Now().UTC()
	_, err = s.jobs.CreateJob(ctx, llmstore.Job{
		ID: jobID, UserID: ownerID, Kind: DeckEnrichJobKind, Payload: payload,
		Status: "queued", Total: int64(len(noteIDs)), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		if errors.Is(err, llmstore.ErrJobCapacityReached) {
			return "", ErrDeckEnrichQueueFull
		}
		return "", ErrDeckEnrichInternal
	}
	return jobID, nil
}

// Handler returns the jobs worker callback for jobs of kind deck_enrich.
// Register it as map[string]jobs.Handler{llmservice.DeckEnrichJobKind: service.Handler()}.
func (s *DeckEnrichService) Handler() jobs.Handler { return s.HandleJob }

// HandleJob reloads each snapshotted note under the job owner, asks the normal
// enrichment service for proposals, and checkpoints progress after each group
// of twenty notes. A retry always walks candidates from the beginning because
// proposals aren't persisted until the job completes. It returns proposals
// only; it never changes note data.
func (s *DeckEnrichService) HandleJob(ctx context.Context, job llmstore.Job) (json.RawMessage, error) {
	if ctx == nil || s == nil || s.notes == nil || s.jobs == nil || s.enricher == nil || s.clock == nil {
		return nil, ErrDeckEnrichInternal
	}
	payload, err := decodeDeckEnrichPayload(job.Payload)
	if err != nil || job.Kind != DeckEnrichJobKind || job.UserID <= 0 || job.ID == "" ||
		job.Attempt <= 0 || job.Status != "running" || job.Total != int64(len(payload.NoteIDs)) ||
		job.Progress < 0 || job.Progress > job.Total {
		return nil, ErrDeckEnrichInternal
	}
	items := make([]deckEnrichResultItem, 0, len(payload.NoteIDs))
	var attemptProgress int64
	var omitted int
	quotaReached := false
	checkpoint := func() error {
		progress := attemptProgress
		if progress < job.Progress {
			progress = job.Progress
		}
		_, err := s.jobs.UpdateJob(ctx, llmstore.JobUpdate{
			ID: job.ID, UserID: job.UserID, Attempt: job.Attempt,
			Status: "running", Progress: progress, Total: job.Total,
			UpdatedAt: s.clock.Now().UTC(),
		})
		if err != nil {
			return ErrDeckEnrichInternal
		}
		return nil
	}
	for start := 0; start < len(payload.NoteIDs); start += deckEnrichBatchSize {
		end := start + deckEnrichBatchSize
		if end > len(payload.NoteIDs) {
			end = len(payload.NoteIDs)
		}
		for index, noteID := range payload.NoteIDs[start:end] {
			note, err := s.notes.Get(ctx, job.UserID, noteID)
			if errors.Is(err, notes.ErrNotFound) {
				attemptProgress++
				omitted++
				continue
			}
			if err != nil {
				return nil, ErrDeckEnrichInternal
			}
			if note.DeckID != payload.DeckID {
				attemptProgress++
				omitted++
				continue
			}
			missing := missingDeckEnrichFields(note)
			if len(missing) == 0 {
				attemptProgress++
				continue
			}
			suggestion, err := s.enricher.EnrichForBulk(ctx, job.UserID, Request{DeckID: payload.DeckID, Word: note.Term}, s.fastModel)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ErrDeckEnrichInternal
				}
				attemptProgress++
				if errors.Is(err, ErrDailyLimit) {
					// The current candidate and every remaining snapshot candidate are
					// omitted once the user's quota is exhausted.
					omitted += len(payload.NoteIDs) - (start + index)
					quotaReached = true
					break
				}
				if isFatalDeckEnrichError(err) {
					return nil, ErrDeckEnrichInternal
				}
				// Other per-note provider failures don't prevent independent notes
				// from being enriched in this attempt.
				omitted++
				continue
			}
			items = append(items, deckEnrichResultItem{
				NoteID: note.ID, Term: note.Term, MissingFields: missing, Suggestions: suggestion,
			})
			attemptProgress++
		}

		if err := checkpoint(); err != nil {
			return nil, err
		}
		if quotaReached {
			break
		}
	}

	if omitted > 0 && len(items) == 0 {
		return nil, ErrDeckEnrichInternal
	}
	result, err := encodeDeckEnrichResult(items, omitted)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type deckEnrichPayload struct {
	DeckID  int64   `json:"deck_id"`
	NoteIDs []int64 `json:"note_ids"`
}

type deckEnrichResultItem struct {
	NoteID        int64    `json:"note_id"`
	Term          string   `json:"term"`
	MissingFields []string `json:"missing_fields"`
	Suggestions   Response `json:"suggestions"`
}

type deckEnrichResult struct {
	Items        []deckEnrichResultItem `json:"items"`
	Partial      bool                   `json:"partial"`
	OmittedCount int                    `json:"omitted_count"`
}

func encodeDeckEnrichResult(items []deckEnrichResultItem, omitted int) (json.RawMessage, error) {
	for {
		result, err := json.Marshal(deckEnrichResult{
			Items: items, Partial: omitted > 0, OmittedCount: omitted,
		})
		if err != nil {
			return nil, ErrDeckEnrichInternal
		}
		if len(result) <= deckEnrichResultMax {
			return result, nil
		}
		if len(items) == 0 {
			return nil, ErrDeckEnrichInternal
		}
		// Keep the persisted result under the cap while retaining earlier
		// proposals. Dropped proposals are represented as generic omissions.
		items = items[:len(items)-1]
		omitted++
		if len(items) == 0 {
			return nil, ErrDeckEnrichInternal
		}
	}
}

func isFatalDeckEnrichError(err error) bool {
	return errors.Is(err, ErrInternal) || errors.Is(err, ErrFeatureDisabled) ||
		errors.Is(err, ErrDeckNotFound) || errors.Is(err, ErrUnsupportedPair) ||
		errors.Is(err, ErrDeckEnrichInternal)
}

func decodeDeckEnrichPayload(raw json.RawMessage) (deckEnrichPayload, error) {
	if len(raw) == 0 || len(raw) > deckEnrichPayloadMax || !json.Valid(raw) {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	values := make(map[string]json.RawMessage, 2)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return deckEnrichPayload{}, ErrInvalidInput
		}
		key, ok := token.(string)
		if !ok || (key != "deck_id" && key != "note_ids") {
			return deckEnrichPayload{}, ErrInvalidInput
		}
		if _, duplicate := values[key]; duplicate {
			return deckEnrichPayload{}, ErrInvalidInput
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return deckEnrichPayload{}, ErrInvalidInput
		}
		values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	deckIDRaw, hasDeckID := values["deck_id"]
	noteIDsRaw, hasNoteIDs := values["note_ids"]
	if !hasDeckID || !hasNoteIDs {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	var payload deckEnrichPayload
	if err := json.Unmarshal(deckIDRaw, &payload.DeckID); err != nil || payload.DeckID <= 0 {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	if err := json.Unmarshal(noteIDsRaw, &payload.NoteIDs); err != nil || payload.NoteIDs == nil || len(payload.NoteIDs) > MaxDeckEnrichCandidates {
		return deckEnrichPayload{}, ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(payload.NoteIDs))
	for _, id := range payload.NoteIDs {
		if id <= 0 {
			return deckEnrichPayload{}, ErrInvalidInput
		}
		if _, duplicate := seen[id]; duplicate {
			return deckEnrichPayload{}, ErrInvalidInput
		}
		seen[id] = struct{}{}
	}
	return payload, nil
}

func missingDeckEnrichFields(note notes.Note) []string {
	missing := make([]string, 0, 10)
	if emptyDeckEnrichString(note.Transcription) {
		missing = append(missing, "transcription")
	}
	if emptyDeckEnrichString(note.Example) {
		missing = append(missing, "example")
	}
	if emptyDeckEnrichString(note.ExampleTranslation) {
		missing = append(missing, "example_translation")
	}
	for _, field := range []string{"pos", "article", "plural", "verb_forms", "cefr"} {
		if emptyDeckEnrichMetadata(note.Fields[field]) {
			missing = append(missing, "fields."+field)
		}
	}
	if !hasDeckEnrichAlternative(note.Alternatives) {
		missing = append(missing, "alternatives")
	}
	return missing
}

func emptyDeckEnrichString(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

func emptyDeckEnrichMetadata(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		for _, entry := range typed {
			if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
				return false
			}
		}
		return true
	case []string:
		for _, entry := range typed {
			if strings.TrimSpace(entry) != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func hasDeckEnrichAlternative(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func randomJobID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate job ID: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(id[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

var _ deckEnrichDeckLister = (*decks.Service)(nil)
var _ deckEnrichNoteReader = (*notes.Service)(nil)
