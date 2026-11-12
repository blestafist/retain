package llmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/decks"
	"github.com/pestit/retain/internal/jobs"
	"github.com/pestit/retain/internal/llmstore"
	"github.com/pestit/retain/internal/notes"
)

type deckEnrichTestFake struct {
	response Response
	err      error
	errs     []error
	calls    []Request
	users    []int64
	models   []string
}

type deckEnrichTestNoteReader struct {
	deckEnrichNoteReader
	get func(context.Context, int64, int64) (notes.Note, error)
}

func (r deckEnrichTestNoteReader) Get(ctx context.Context, userID, noteID int64) (notes.Note, error) {
	return r.get(ctx, userID, noteID)
}

func (f *deckEnrichTestFake) Enabled() bool { return true }

func (f *deckEnrichTestFake) EnrichForBulk(_ context.Context, userID int64, request Request, fastModel string) (Response, error) {
	f.calls = append(f.calls, request)
	f.users = append(f.users, userID)
	f.models = append(f.models, fastModel)
	if index := len(f.calls) - 1; index < len(f.errs) && f.errs[index] != nil {
		return Response{}, f.errs[index]
	}
	return f.response, f.err
}

type recordingDeckEnrichJobStore struct {
	store   *llmstore.Store
	updates []llmstore.JobUpdate
}

func (s *recordingDeckEnrichJobStore) CreateJob(ctx context.Context, job llmstore.Job) (llmstore.Job, error) {
	return s.store.CreateJob(ctx, job)
}

func (s *recordingDeckEnrichJobStore) UpdateJob(ctx context.Context, update llmstore.JobUpdate) (llmstore.Job, error) {
	s.updates = append(s.updates, update)
	return s.store.UpdateJob(ctx, update)
}

func TestDeckEnrichSubmitSnapshotsOnlyMissingFieldsWithoutCallingLLM(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	complete, err := noteService.Create(ctx, fixture.userID, fixture.deckID, completeDeckEnrichNote("complete"))
	if err != nil {
		t.Fatal(err)
	}
	missing, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
		Term: "missing", Translation: "translation", Fields: map[string]any{
			"pos": "noun", "article": "die", "plural": "Dinge", "verb_forms": []string{}, "cefr": "A1",
		},
		Alternatives: []string{"variant"}, Transcription: stringValue("IPA"), ExampleTranslation: stringValue("translation"),
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := noteService.Get(ctx, fixture.userID, missing.ID)
	if err != nil {
		t.Fatal(err)
	}
	enricher := &deckEnrichTestFake{}
	service := NewDeckEnrichService(fixture.decks, noteService, fixture.store, enricher, fixture.clock, DeckEnrichConfig{})

	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if len(enricher.calls) != 0 {
		t.Fatalf("Enrich() calls on submit = %d, want 0", len(enricher.calls))
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != "queued" || job.Total != 1 || job.Progress != 0 || len(job.Result) != 0 {
		t.Fatalf("queued job = %+v, want queued total 1, progress 0, no result", job)
	}
	var payload deckEnrichPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("decode job payload: %v", err)
	}
	if payload.DeckID != fixture.deckID || !reflect.DeepEqual(payload.NoteIDs, []int64{missing.ID}) {
		t.Fatalf("payload = %+v, want only missing note %d (complete note %d)", payload, missing.ID, complete.ID)
	}
	after, err := noteService.Get(ctx, fixture.userID, missing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("Submit mutated the note: before=%+v after=%+v", before, after)
	}
}

func TestDeckEnrichSubmitConcealsForeignDeck(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	other, err := fixture.database.Queries().CreateUser(ctx, gen.CreateUserParams{
		Email: "deck-enrich-other@example.test", Name: "Other", PasswordHash: "unused", CreatedAt: fixture.clock.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, native := "de", "pl"
	foreignDeck, err := decks.NewService(fixture.database, fixture.clock).Create(ctx, other.ID, decks.CreateInput{
		Name: "Foreign", LangTarget: &target, LangNative: &native,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewDeckEnrichService(fixture.decks, notes.NewService(fixture.database, fixture.clock), fixture.store, &deckEnrichTestFake{}, fixture.clock, DeckEnrichConfig{})
	if _, err := service.Submit(ctx, fixture.userID, foreignDeck.ID); !errors.Is(err, ErrDeckNotFound) {
		t.Fatalf("Submit(foreign deck) error = %v, want ErrDeckNotFound", err)
	}
}

func TestDeckEnrichRejectsDeckOverCandidateLimit(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	for index := range MaxDeckEnrichCandidates {
		_, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: fmt.Sprintf("word-%03d", index), Translation: "translation",
		})
		if err != nil {
			t.Fatalf("Create(note %d): %v", index, err)
		}
	}
	service := NewDeckEnrichService(fixture.decks, noteService, fixture.store, &deckEnrichTestFake{}, fixture.clock, DeckEnrichConfig{})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatalf("Submit(%d candidates) error = %v", MaxDeckEnrichCandidates, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil || job.Total != MaxDeckEnrichCandidates {
		t.Fatalf("GetJob() = %+v, error=%v; want total %d", job, err, MaxDeckEnrichCandidates)
	}
	if _, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
		Term: "word-over-limit", Translation: "translation",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(ctx, fixture.userID, fixture.deckID); !errors.Is(err, ErrDeckEnrichCandidateLimit) {
		t.Fatalf("Submit(oversized deck) error = %v, want size-limit error", err)
	}
	var count int
	if err := fixture.database.SQL.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE user_id = ?`, fixture.userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("queued jobs = %d, want only the first 100-candidate job", count)
	}
}

func TestDeckEnrichWorkerPersistsSuggestionsAndDoesNotMutateNotes(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	created, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
		Term: "partial", Translation: "translation", Transcription: stringValue("IPA"),
		ExampleTranslation: stringValue("existing translation"), Alternatives: []string{"variant"},
		Fields: map[string]any{"pos": "noun", "article": "die", "plural": "Dinge", "verb_forms": []string{"machen"}, "cefr": "A1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := noteService.Get(ctx, fixture.userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	enricher := &deckEnrichTestFake{response: Response{
		Translation: "Vorschlag", Alternatives: []string{"option"}, Transcription: "ˈpaʁt",
		Example: "Ein Beispiel.", ExampleTranslation: "Przykład.",
		Fields: Fields{POS: "noun", Article: "der", Plural: "Teile", VerbForms: []string{}, CEFR: "A2"},
	}}
	service := NewDeckEnrichService(fixture.decks, noteService, fixture.store, enricher, fixture.clock, DeckEnrichConfig{FastModel: "bulk-fast-model"})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(fixture.store, fixture.clock, map[string]jobs.Handler{
		DeckEnrichJobKind: service.Handler(),
	}, jobs.Config{Workers: 1})
	if err != nil {
		t.Fatalf("jobs.New() error = %v", err)
	}
	claimed, err := worker.RunOne(ctx)
	if err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "done" || job.Progress != 1 || job.Total != 1 || len(job.Result) == 0 {
		t.Fatalf("completed job = %+v, want persisted completed result", job)
	}
	var result deckEnrichResult
	if err := json.Unmarshal(job.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].NoteID != created.ID || result.Items[0].Term != "partial" ||
		!reflect.DeepEqual(result.Items[0].MissingFields, []string{"example"}) || result.Items[0].Suggestions.Translation != "Vorschlag" ||
		result.Partial || result.OmittedCount != 0 {
		t.Fatalf("result = %+v, want one complete proposal for only the missing example", result)
	}
	var resultFields map[string]json.RawMessage
	if err := json.Unmarshal(job.Result, &resultFields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(resultFields), []string{"items", "omitted_count", "partial"}) {
		t.Errorf("result object keys = %v, want exactly items, partial, omitted_count", sortedKeys(resultFields))
	}
	if len(enricher.calls) != 1 || enricher.calls[0] != (Request{DeckID: fixture.deckID, Word: "partial"}) ||
		enricher.users[0] != fixture.userID || enricher.models[0] != "bulk-fast-model" {
		t.Errorf("bulk enrichment calls=%+v users=%v models=%v, want owned note once with configured fast model", enricher.calls, enricher.users, enricher.models)
	}
	after, err := noteService.Get(ctx, fixture.userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("worker mutated the note: before=%+v after=%+v", before, after)
	}
}

func TestDeckEnrichHandlerCheckpointsEveryTwentyWithClaimAttempt(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	for index := range 21 {
		if _, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: fmt.Sprintf("batch-%02d", index), Translation: "translation",
		}); err != nil {
			t.Fatalf("Create(note %d): %v", index, err)
		}
	}
	enricher := &deckEnrichTestFake{response: Response{
		Translation: "translation", Alternatives: []string{}, Fields: Fields{VerbForms: []string{}},
	}}
	jobID, err := NewDeckEnrichService(fixture.decks, noteService, fixture.store, enricher, fixture.clock, DeckEnrichConfig{}).
		Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.store.ClaimJob(ctx, fixture.userID, jobID, fixture.clock.now)
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	if claimed.Attempt <= 0 {
		t.Fatalf("claimed Attempt = %d, want positive fencing token", claimed.Attempt)
	}
	jobStore := &recordingDeckEnrichJobStore{store: fixture.store}
	service := NewDeckEnrichService(fixture.decks, noteService, jobStore, enricher, fixture.clock, DeckEnrichConfig{})
	result, err := service.HandleJob(ctx, claimed)
	if err != nil {
		t.Fatalf("HandleJob() error = %v", err)
	}
	if len(jobStore.updates) != 2 {
		t.Fatalf("progress updates = %+v, want checkpoints at 20 and 21", jobStore.updates)
	}
	wantProgress := []int64{20, 21}
	for index, update := range jobStore.updates {
		if update.Status != "running" || update.Progress != wantProgress[index] || update.Attempt != claimed.Attempt || update.Total != 21 {
			t.Errorf("update[%d] = %+v, want running progress=%d total=21 attempt=%d", index, update, wantProgress[index], claimed.Attempt)
		}
	}
	if !json.Valid(result) || len(result) > deckEnrichResultMax {
		t.Errorf("handler result has invalid or oversized JSON (size=%d)", len(result))
	}
}

func TestDeckEnrichWorkerPreservesSuggestionsOnQuotaFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	for index := range deckEnrichBatchSize + 1 {
		if _, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: fmt.Sprintf("quota-%02d", index), Translation: "limit",
		}); err != nil {
			t.Fatalf("Create(note %d): %v", index, err)
		}
	}
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	enrichment := fixture.service(generator, deckEnrichBatchSize)
	service := NewDeckEnrichService(fixture.decks, noteService, fixture.store, enrichment, fixture.clock, DeckEnrichConfig{})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(fixture.store, fixture.clock, map[string]jobs.Handler{
		DeckEnrichJobKind: service.Handler(),
	}, jobs.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.RunOne(ctx); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "done" || job.Progress != deckEnrichBatchSize+1 || job.Total != deckEnrichBatchSize+1 ||
		job.Error != nil || len(job.Result) == 0 {
		t.Fatalf("partial job = %+v, want completed result and progress accounting for all candidates", job)
	}
	if generator.calls != deckEnrichBatchSize {
		t.Errorf("provider calls = %d, want %d before quota rejection", generator.calls, deckEnrichBatchSize)
	}
	var result deckEnrichResult
	if err := json.Unmarshal(job.Result, &result); err != nil {
		t.Fatalf("decode partial result: %v", err)
	}
	if len(result.Items) != deckEnrichBatchSize || !result.Partial || result.OmittedCount != 1 {
		t.Fatalf("partial result = items:%d partial:%v omitted:%d; want 20, true, 1", len(result.Items), result.Partial, result.OmittedCount)
	}
	var resultFields map[string]json.RawMessage
	if err := json.Unmarshal(job.Result, &resultFields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(resultFields), []string{"items", "omitted_count", "partial"}) {
		t.Errorf("partial result keys = %v, want exact result object shape", sortedKeys(resultFields))
	}
	if strings.Contains(string(job.Result), "daily LLM usage limit reached") || strings.Contains(string(job.Result), "job handler failed") {
		t.Errorf("partial result exposed an internal/provider error: %s", job.Result)
	}
}

func TestDeckEnrichWorkerContinuesAfterProviderErrorWithoutExposingIt(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	for index := range 3 {
		if _, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: fmt.Sprintf("provider-%02d", index), Translation: "translation",
		}); err != nil {
			t.Fatalf("Create(note %d): %v", index, err)
		}
	}
	privateErr := errors.New("provider response contained secret-token-123")
	enricher := &deckEnrichTestFake{
		response: Response{Translation: "Vorschlag"},
		errs:     []error{nil, privateErr, nil},
	}
	service := NewDeckEnrichService(fixture.decks, noteService, fixture.store, enricher, fixture.clock, DeckEnrichConfig{})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(fixture.store, fixture.clock, map[string]jobs.Handler{DeckEnrichJobKind: service.Handler()}, jobs.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.RunOne(ctx); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var result deckEnrichResult
	if err := json.Unmarshal(job.Result, &result); err != nil {
		t.Fatalf("decode partial result: %v", err)
	}
	if job.Status != "done" || job.Progress != 3 || len(result.Items) != 2 || !result.Partial || result.OmittedCount != 1 {
		t.Fatalf("job/result = %+v / %+v, want two successful suggestions and one omission", job, result)
	}
	if len(enricher.calls) != 3 {
		t.Errorf("provider calls = %d, want continued attempt through all three candidates", len(enricher.calls))
	}
	if strings.Contains(string(job.Result), "secret-token-123") {
		t.Errorf("persisted result exposed provider error: %s", job.Result)
	}
}

func TestDeckEnrichWorkerOmitsMissingOrMovedNotesAndPreservesSuggestions(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	noteIDs := make([]int64, 0, 3)
	for _, term := range []string{"missing", "moved", "available"} {
		note, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: term, Translation: "translation",
		})
		if err != nil {
			t.Fatalf("Create(%q): %v", term, err)
		}
		noteIDs = append(noteIDs, note.ID)
	}
	reader := deckEnrichTestNoteReader{
		deckEnrichNoteReader: noteService,
		get: func(ctx context.Context, userID, noteID int64) (notes.Note, error) {
			if noteID == noteIDs[0] {
				return notes.Note{}, notes.ErrNotFound
			}
			note, err := noteService.Get(ctx, userID, noteID)
			if err == nil && noteID == noteIDs[1] {
				note.DeckID++
			}
			return note, err
		},
	}
	enricher := &deckEnrichTestFake{response: Response{Translation: "suggestion"}}
	service := NewDeckEnrichService(fixture.decks, reader, fixture.store, enricher, fixture.clock, DeckEnrichConfig{})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(fixture.store, fixture.clock, map[string]jobs.Handler{
		DeckEnrichJobKind: service.Handler(),
	}, jobs.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.RunOne(ctx); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var result deckEnrichResult
	if err := json.Unmarshal(job.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if job.Status != "done" || job.Progress != 3 || job.Total != 3 || len(result.Items) != 1 ||
		result.Items[0].NoteID != noteIDs[2] || result.Items[0].Term != "available" ||
		!result.Partial || result.OmittedCount != 2 {
		t.Fatalf("job/result = %+v / %+v, want one preserved proposal, two omissions, and complete progress", job, result)
	}
	if len(enricher.calls) != 1 || enricher.calls[0].Word != "available" {
		t.Errorf("enrichment calls = %+v, want only the available note", enricher.calls)
	}
}

func TestDeckEnrichWorkerFailsSafelyOnUnexpectedNoteStorageError(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	var failingNoteID int64
	for _, term := range []string{"available", "storage-error"} {
		note, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: term, Translation: "translation",
		})
		if err != nil {
			t.Fatalf("Create(%q): %v", term, err)
		}
		if term == "storage-error" {
			failingNoteID = note.ID
		}
	}
	privateErr := errors.New("private storage details")
	reader := deckEnrichTestNoteReader{
		deckEnrichNoteReader: noteService,
		get: func(ctx context.Context, userID, noteID int64) (notes.Note, error) {
			if noteID == failingNoteID {
				return notes.Note{}, privateErr
			}
			return noteService.Get(ctx, userID, noteID)
		},
	}
	enricher := &deckEnrichTestFake{response: Response{Translation: "suggestion"}}
	service := NewDeckEnrichService(fixture.decks, reader, fixture.store, enricher, fixture.clock, DeckEnrichConfig{})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(fixture.store, fixture.clock, map[string]jobs.Handler{
		DeckEnrichJobKind: service.Handler(),
	}, jobs.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.RunOne(ctx); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "failed" || job.Error == nil || *job.Error != "job handler failed" || len(job.Result) != 0 {
		t.Fatalf("job = %+v, want generic failure and no result", job)
	}
	if strings.Contains(*job.Error, privateErr.Error()) || len(enricher.calls) != 1 {
		t.Errorf("failure leaked storage details or continued processing: error=%q calls=%d", *job.Error, len(enricher.calls))
	}
}

func TestDeckEnrichWorkerFailsWhenNoSuggestionSucceeds(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	if _, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{Term: "only", Translation: "translation"}); err != nil {
		t.Fatal(err)
	}
	generator := &fakeGenerator{response: json.RawMessage(validSuggestionJSON)}
	service := NewDeckEnrichService(fixture.decks, noteService, fixture.store,
		fixture.service(generator, 0), fixture.clock, DeckEnrichConfig{})
	jobID, err := service.Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jobs.New(fixture.store, fixture.clock, map[string]jobs.Handler{DeckEnrichJobKind: service.Handler()}, jobs.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.RunOne(ctx); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	job, err := fixture.store.GetJob(ctx, fixture.userID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "failed" || job.Error == nil || *job.Error != "job handler failed" || len(job.Result) != 0 {
		t.Fatalf("job = %+v, want generic failure and no result when no suggestion succeeds", job)
	}
}

func TestDeckEnrichRetryRestartsWalkAndKeepsProgressMonotonic(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	noteService := notes.NewService(fixture.database, fixture.clock)
	for index := range 40 {
		if _, err := noteService.Create(ctx, fixture.userID, fixture.deckID, notes.CreateInput{
			Term: fmt.Sprintf("retry-%02d", index), Translation: "translation",
		}); err != nil {
			t.Fatalf("Create(note %d): %v", index, err)
		}
	}
	enricher := &deckEnrichTestFake{response: Response{Translation: "Vorschlag"}}
	jobID, err := NewDeckEnrichService(fixture.decks, noteService, fixture.store, enricher, fixture.clock, DeckEnrichConfig{}).
		Submit(ctx, fixture.userID, fixture.deckID)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim, err := fixture.store.ClaimJob(ctx, fixture.userID, jobID, fixture.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.UpdateJob(ctx, llmstore.JobUpdate{
		ID: jobID, UserID: fixture.userID, Attempt: firstClaim.Attempt,
		Status: "running", Progress: 30, Total: firstClaim.Total, UpdatedAt: fixture.clock.now,
	}); err != nil {
		t.Fatalf("checkpoint first attempt: %v", err)
	}
	if err := fixture.store.RequeueJob(ctx, jobID, firstClaim.Attempt, fixture.clock.now.Add(time.Second)); err != nil {
		t.Fatalf("RequeueJob() error = %v", err)
	}
	retry, err := fixture.store.ClaimJob(ctx, fixture.userID, jobID, fixture.clock.now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ClaimJob(retry) error = %v", err)
	}
	if retry.Attempt <= firstClaim.Attempt || retry.Progress != 30 {
		t.Fatalf("retry = attempt %d progress %d; want a new attempt retaining progress 30", retry.Attempt, retry.Progress)
	}
	jobStore := &recordingDeckEnrichJobStore{store: fixture.store}
	service := NewDeckEnrichService(fixture.decks, noteService, jobStore, enricher, fixture.clock, DeckEnrichConfig{})
	if _, err := service.HandleJob(ctx, retry); err != nil {
		t.Fatalf("HandleJob(retry) error = %v", err)
	}
	if len(enricher.calls) != 40 {
		t.Errorf("retry provider calls = %d, want full restart across 40 candidates", len(enricher.calls))
	}
	if len(jobStore.updates) != 2 || jobStore.updates[0].Progress != 30 || jobStore.updates[1].Progress != 40 {
		t.Errorf("retry checkpoints = %+v, want monotonic progress [30, 40]", jobStore.updates)
	}
}

func TestDeckEnrichResultRespectsSizeCapWithGenericOmission(t *testing.T) {
	_, err := encodeDeckEnrichResult([]deckEnrichResultItem{{
		NoteID: 1, Term: "large", Suggestions: Response{Translation: strings.Repeat("x", deckEnrichResultMax)},
	}}, 0)
	if !errors.Is(err, ErrDeckEnrichInternal) {
		t.Fatalf("encodeDeckEnrichResult(no fitting proposals) error = %v, want safe failure", err)
	}

	result, err := encodeDeckEnrichResult([]deckEnrichResultItem{
		{NoteID: 1, Term: "fits", Suggestions: Response{Translation: "small"}},
		{NoteID: 2, Term: "large", Suggestions: Response{Translation: strings.Repeat("x", deckEnrichResultMax)}},
	}, 0)
	if err != nil {
		t.Fatalf("encodeDeckEnrichResult(with fitting proposal) error = %v", err)
	}
	if len(result) > deckEnrichResultMax {
		t.Fatalf("encoded result size = %d, exceeds %d-byte cap", len(result), deckEnrichResultMax)
	}
	var decoded deckEnrichResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("decode capped result: %v", err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].NoteID != 1 || decoded.Items[0].Term != "fits" ||
		!decoded.Partial || decoded.OmittedCount != 1 {
		t.Errorf("capped result = %+v, want the fitting proposal and one generic omission", decoded)
	}
}

func TestDeckEnrichPayloadIsStrict(t *testing.T) {
	for _, raw := range []string{
		`{"deck_id":1,"note_ids":[1],"owner_id":2}`,
		`{"deck_id":1,"note_ids":[1],"note_ids":[2]}`,
		`{"deck_id":1,"note_ids":[1]} {}`,
		`{"deck_id":1,"note_ids":[1,1]}`,
		`{"deck_id":1,"note_ids":null}`,
	} {
		if _, err := decodeDeckEnrichPayload(json.RawMessage(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("decodeDeckEnrichPayload(%s) error = %v, want invalid input", raw, err)
		}
	}
}

func completeDeckEnrichNote(term string) notes.CreateInput {
	return notes.CreateInput{
		Term: term, Translation: "translation", Transcription: stringValue("IPA"),
		Example: stringValue("Example."), ExampleTranslation: stringValue("Przykład."),
		Alternatives: []string{"variant"},
		Fields:       map[string]any{"pos": "noun", "article": "die", "plural": "Dinge", "verb_forms": []string{"do"}, "cefr": "A1"},
	}
}

func stringValue(value string) *string { return &value }

func sortedKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
