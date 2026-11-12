package llmstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

var testDayStart = time.Date(2026, time.November, 10, 4, 0, 0, 0, time.UTC)

type storeFixture struct {
	database *db.DB
	store    *Store
	userID   int64
	otherID  int64
}

func newStoreFixture(t *testing.T) *storeFixture {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "llmstore.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	queries := database.Queries()
	owner, err := queries.CreateUser(context.Background(), gen.CreateUserParams{
		Email: "owner@example.test", Name: "Owner", PasswordHash: "hash", CreatedAt: testDayStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := queries.CreateUser(context.Background(), gen.CreateUserParams{
		Email: "other@example.test", Name: "Other", PasswordHash: "hash", CreatedAt: testDayStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &storeFixture{database: database, store: New(queries), userID: owner.ID, otherID: other.ID}
}

func (f *storeFixture) reserveInput(userID int64, createdAt time.Time, limit int64) ReserveUsageInput {
	return ReserveUsageInput{
		UserID: userID, Kind: "autofill", Model: "test-model", DailyLimit: limit,
		DayStart: testDayStart, DayEnd: testDayStart.Add(24 * time.Hour), CreatedAt: createdAt,
	}
}

func TestReserveUsageDailyLimit(t *testing.T) {
	tests := []struct {
		name          string
		priorRequests int
		limit         int64
		wantReached   bool
	}{
		{name: "under limit", priorRequests: 1, limit: 2},
		{name: "at limit", priorRequests: 1, limit: 1, wantReached: true},
		{name: "zero limit", limit: 0, wantReached: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newStoreFixture(t)
			for index := 0; index < test.priorRequests; index++ {
				created := testDayStart.Add(time.Duration(index+1) * time.Minute)
				if _, err := f.store.ReserveUsage(context.Background(), f.reserveInput(f.userID, created, 10)); err != nil {
					t.Fatalf("seed reservation: %v", err)
				}
			}

			createdAt := testDayStart.Add(2 * time.Hour)
			usage, err := f.store.ReserveUsage(context.Background(), f.reserveInput(f.userID, createdAt, test.limit))
			var limitErr *DailyLimitReachedError
			if test.wantReached {
				if !errors.As(err, &limitErr) {
					t.Fatalf("ReserveUsage() error = %v, want *DailyLimitReachedError", err)
				}
				if limitErr.Limit != test.limit {
					t.Errorf("limit error limit = %d, want %d", limitErr.Limit, test.limit)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReserveUsage() error = %v", err)
			}
			if usage.ID <= 0 || usage.UserID != f.userID || usage.Cached || usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
				t.Errorf("reservation = %+v, expected an empty uncached reservation", usage)
			}
			if !usage.CreatedAt.Equal(createdAt) {
				t.Errorf("CreatedAt = %s, want %s", usage.CreatedAt, createdAt)
			}
		})
	}
}

func TestReserveUsageIsAtomicAndUserScoped(t *testing.T) {
	f := newStoreFixture(t)
	const limit = 5
	const requests = 24
	var wait sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	limitErrors := 0
	otherErrors := make([]error, 0)
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			createdAt := testDayStart.Add(time.Duration(index+1) * time.Minute)
			_, err := f.store.ReserveUsage(context.Background(), f.reserveInput(f.userID, createdAt, limit))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
				return
			}
			var dailyLimitErr *DailyLimitReachedError
			if errors.As(err, &dailyLimitErr) {
				limitErrors++
				return
			}
			otherErrors = append(otherErrors, err)
		}(index)
	}
	wait.Wait()
	if len(otherErrors) != 0 {
		t.Fatalf("unexpected reservation errors: %v", otherErrors)
	}
	if successes != limit || limitErrors != requests-limit {
		t.Fatalf("successes = %d and limit errors = %d, want %d and %d", successes, limitErrors, limit, requests-limit)
	}

	otherInput := f.reserveInput(f.otherID, testDayStart.Add(3*time.Hour), 1)
	if _, err := f.store.ReserveUsage(context.Background(), otherInput); err != nil {
		t.Fatalf("other user reservation: %v", err)
	}
	ownerRows, err := f.store.ListUsage(context.Background(), f.userID, testDayStart, testDayStart.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("list owner usage: %v", err)
	}
	if len(ownerRows) != limit {
		t.Errorf("owner usage rows = %d, want %d", len(ownerRows), limit)
	}
}

func TestUsageCacheHitsBoundsAndTokenAccounting(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	cacheHit, err := f.store.RecordCachedUsage(ctx, f.userID, "autofill", "test-model", testDayStart.Add(time.Minute))
	if err != nil {
		t.Fatalf("RecordCachedUsage() error = %v", err)
	}
	if !cacheHit.Cached {
		t.Fatal("cache-hit usage is not marked cached")
	}

	// A cached response is accounted for but doesn't consume the provider quota.
	reservation, err := f.store.ReserveUsage(ctx, f.reserveInput(f.userID, testDayStart.Add(2*time.Minute), 1))
	if err != nil {
		t.Fatalf("ReserveUsage() after cache hit: %v", err)
	}
	if err := f.store.CompleteUsage(ctx, f.userID, reservation.ID, 17, 9); err != nil {
		t.Fatalf("CompleteUsage() error = %v", err)
	}
	if err := f.store.CompleteUsage(ctx, f.otherID, reservation.ID, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign CompleteUsage() error = %v, want ErrNotFound", err)
	}

	// Reservations just outside the supplied half-open range are not returned.
	priorStart := testDayStart.Add(-24 * time.Hour)
	if _, err := f.store.ReserveUsage(ctx, ReserveUsageInput{
		UserID: f.userID, Kind: "ask", Model: "test-model", DailyLimit: 1,
		DayStart: priorStart, DayEnd: testDayStart, CreatedAt: testDayStart.Add(-time.Second),
	}); err != nil {
		t.Fatalf("prior-day reservation: %v", err)
	}
	rows, err := f.store.ListUsage(ctx, f.userID, testDayStart, testDayStart.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ListUsage() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListUsage() returned %d rows, want cache hit and provider reservation only", len(rows))
	}
	var foundCompleted bool
	for _, row := range rows {
		if row.ID == reservation.ID {
			foundCompleted = row.PromptTokens == 17 && row.CompletionTokens == 9 && !row.Cached
		}
	}
	if !foundCompleted {
		t.Errorf("completed usage not returned with token counts: %+v", rows)
	}
	otherRows, err := f.store.ListUsage(ctx, f.otherID, testDayStart, testDayStart.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ListUsage() for other user: %v", err)
	}
	if len(otherRows) != 0 {
		t.Errorf("other user's usage leaked: %+v", otherRows)
	}
}

func TestAppSettingsAndCachePersistence(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	updatedAt := testDayStart.Add(3 * time.Hour)
	setting, err := f.store.SetAppSetting(ctx, AppSetting{
		Key: "llm_model", Value: json.RawMessage(`"model-v1"`), UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("SetAppSetting() error = %v", err)
	}
	if setting.Key != "llm_model" || string(setting.Value) != `"model-v1"` || !setting.UpdatedAt.Equal(updatedAt) {
		t.Errorf("stored app setting = %+v", setting)
	}
	setting, err = f.store.SetAppSetting(ctx, AppSetting{
		Key: "llm_model", Value: json.RawMessage(`"model-v2"`), UpdatedAt: updatedAt.Add(time.Minute),
	})
	if err != nil || string(setting.Value) != `"model-v2"` {
		t.Fatalf("app setting upsert = %+v, error %v", setting, err)
	}
	setting, err = f.store.GetAppSetting(ctx, "llm_model")
	if err != nil || string(setting.Value) != `"model-v2"` {
		t.Fatalf("GetAppSetting() = %+v, error %v", setting, err)
	}

	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	entry := CacheEntry{
		Key: key, Model: "test-model", PromptVersion: 3,
		Response: json.RawMessage(`{"translation":["word"]}`), CreatedAt: updatedAt,
	}
	if err := f.store.SetCache(ctx, entry); err != nil {
		t.Fatalf("SetCache() error = %v", err)
	}
	loaded, found, err := f.store.GetCache(ctx, key)
	if err != nil || !found {
		t.Fatalf("GetCache() found = %v, error = %v", found, err)
	}
	if string(loaded.Key) != string(key) || loaded.Model != entry.Model || loaded.PromptVersion != entry.PromptVersion ||
		string(loaded.Response) != string(entry.Response) || !loaded.CreatedAt.Equal(entry.CreatedAt) {
		t.Errorf("loaded cache entry = %+v, want %+v", loaded, entry)
	}
	missingKey := make([]byte, 32)
	missingKey[0] = 255
	if _, found, err := f.store.GetCache(ctx, missingKey); err != nil || found {
		t.Errorf("GetCache() for missing key: found = %v, error = %v", found, err)
	}
	if _, found, err := f.store.GetCache(ctx, []byte("missing")); !errors.Is(err, ErrInvalidInput) || found {
		t.Errorf("GetCache() with invalid key: found = %v, error = %v", found, err)
	}
}

func TestJobsAreOwnerScopedAndPersistent(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	createdAt := testDayStart.Add(time.Hour)
	job := Job{
		ID: "job-1", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{"deck_id":7}`),
		Status: "queued", Total: 2, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	created, err := f.store.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if created.ID != job.ID || created.UserID != f.userID || string(created.Payload) != string(job.Payload) {
		t.Errorf("created job = %+v", created)
	}

	claimedAt := createdAt.Add(time.Second)
	claimed, err := f.store.ClaimJob(ctx, f.userID, job.ID, claimedAt)
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	if claimed.Status != "running" || !claimed.UpdatedAt.Equal(claimedAt) {
		t.Fatalf("claimed job = %+v", claimed)
	}
	if _, err := f.store.ClaimJob(ctx, f.userID, job.ID, claimedAt.Add(time.Second)); !errors.Is(err, ErrInvalidJobTransition) {
		t.Errorf("duplicate ClaimJob() error = %v, want ErrInvalidJobTransition", err)
	}
	if _, err := f.store.GetJob(ctx, f.otherID, job.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetJob() for another owner error = %v, want ErrNotFound", err)
	}
	if _, err := f.store.ClaimJob(ctx, f.otherID, job.ID, claimedAt); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClaimJob() for another owner error = %v, want ErrNotFound", err)
	}
	if _, err := f.store.UpdateJob(ctx, JobUpdate{
		ID: job.ID, UserID: f.otherID, Attempt: claimed.Attempt, Status: "running", Progress: 1, Total: 2,
		UpdatedAt: claimedAt.Add(time.Minute),
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateJob() for another owner error = %v, want ErrNotFound", err)
	}

	updatedAt := claimedAt.Add(time.Minute)
	updated, err := f.store.UpdateJob(ctx, JobUpdate{
		ID: job.ID, UserID: f.userID, Attempt: claimed.Attempt, Status: "running", Progress: 1, Total: 2, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("UpdateJob() error = %v", err)
	}
	loaded, err := f.store.GetJob(ctx, f.userID, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if updated.Status != "running" || loaded.Progress != 1 || !loaded.UpdatedAt.Equal(updatedAt) {
		t.Errorf("updated/loaded jobs = %+v / %+v", updated, loaded)
	}
}

func TestCreateJobEnforcesAtomicQueueCapsAndDeckDeduplication(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	at := testDayStart.Add(20 * time.Hour)
	create := func(id string, userID int64, kind string, payload string) error {
		_, err := f.store.CreateJob(ctx, Job{
			ID: id, UserID: userID, Kind: kind, Payload: json.RawMessage(payload),
			Status: "queued", Total: 1, CreatedAt: at, UpdatedAt: at,
		})
		return err
	}
	if err := create("deck-a", f.userID, "deck_enrich", `{"deck_id":41}`); err != nil {
		t.Fatalf("create first deck job: %v", err)
	}
	if err := create("deck-a-duplicate", f.userID, "deck_enrich", `{"deck_id":41,"note_ids":[1,2]}`); !errors.Is(err, ErrJobCapacityReached) {
		t.Fatalf("duplicate active deck job error = %v, want capacity sentinel", err)
	}
	if err := create("deck-b", f.userID, "deck_enrich", `{"deck_id":42}`); err != nil {
		t.Fatalf("create different deck job: %v", err)
	}
	if err := create("other-kind", f.userID, "maintenance", `{}`); err != nil {
		t.Fatalf("create third active user job: %v", err)
	}
	if err := create("user-cap", f.userID, "maintenance", `{}`); !errors.Is(err, ErrJobCapacityReached) {
		t.Fatalf("fourth active user job error = %v, want capacity sentinel", err)
	}
	if err := create("other-user-job", f.otherID, "deck_enrich", `{"deck_id":41}`); err != nil {
		t.Fatalf("same deck for another user should be allowed: %v", err)
	}
}

func TestCreateJobEnforcesGlobalActiveQueueCap(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	userIDs := []int64{f.userID, f.otherID}
	for index := 2; index < 34; index++ {
		user, err := f.database.Queries().CreateUser(ctx, gen.CreateUserParams{
			Email: fmt.Sprintf("queue-user-%02d@example.test", index), Name: "Queue User",
			PasswordHash: "hash", CreatedAt: testDayStart,
		})
		if err != nil {
			t.Fatalf("create queue user %d: %v", index, err)
		}
		userIDs = append(userIDs, user.ID)
	}
	at := testDayStart.Add(21 * time.Hour)
	created := 0
	for userIndex, userID := range userIDs {
		limit := 3
		if userIndex == len(userIDs)-1 {
			limit = 1 // 33 users × 3 + this job = 100 active jobs.
		}
		for jobIndex := 0; jobIndex < limit; jobIndex++ {
			_, err := f.store.CreateJob(ctx, Job{
				ID: fmt.Sprintf("global-%02d-%d", userIndex, jobIndex), UserID: userID,
				Kind: "maintenance", Payload: json.RawMessage(`{}`), Status: "queued", Total: 1,
				CreatedAt: at, UpdatedAt: at,
			})
			if err != nil {
				t.Fatalf("create job %d for user %d after %d active jobs: %v", jobIndex, userID, created, err)
			}
			created++
		}
	}
	if created != 100 {
		t.Fatalf("created %d active jobs, want 100", created)
	}
	if _, err := f.store.CreateJob(ctx, Job{
		ID: "global-over-cap", UserID: f.userID, Kind: "maintenance", Payload: json.RawMessage(`{}`),
		Status: "queued", Total: 1, CreatedAt: at, UpdatedAt: at,
	}); !errors.Is(err, ErrJobCapacityReached) {
		t.Fatalf("101st active job error = %v, want capacity sentinel", err)
	}
}

func TestConcurrentCreateJobCallsCannotExceedPerUserCap(t *testing.T) {
	f := newStoreFixture(t)
	at := testDayStart.Add(22 * time.Hour)
	start := make(chan struct{})
	results := make(chan error, 12)
	var wait sync.WaitGroup
	for index := range 12 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := f.store.CreateJob(context.Background(), Job{
				ID: fmt.Sprintf("concurrent-%02d", index), UserID: f.userID, Kind: "maintenance",
				Payload: json.RawMessage(`{}`), Status: "queued", Total: 1,
				CreatedAt: at, UpdatedAt: at,
			})
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrJobCapacityReached):
			rejected++
		default:
			t.Fatalf("concurrent CreateJob() error = %v, want success or capacity rejection", err)
		}
	}
	if succeeded != 3 || rejected != 9 {
		t.Fatalf("concurrent CreateJob results = %d succeeded/%d rejected, want 3/9", succeeded, rejected)
	}
}

func TestJobResultIsValidAndOnlyStoredOnDone(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	at := testDayStart.Add(2 * time.Hour)
	create := func(id string) Job {
		t.Helper()
		job, err := f.store.CreateJob(ctx, Job{
			ID: id, UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
			Status: "queued", Total: 1, CreatedAt: at, UpdatedAt: at,
		})
		if err != nil {
			t.Fatalf("CreateJob(%q) error = %v", id, err)
		}
		return job
	}
	update := func(job Job, status string, result json.RawMessage) (Job, error) {
		t.Helper()
		return f.store.UpdateJob(ctx, JobUpdate{
			ID: job.ID, UserID: f.userID, Attempt: job.Attempt, Status: status, Progress: 1, Total: 1,
			Result: result, UpdatedAt: at.Add(time.Minute),
		})
	}

	failedJob := create("failed-result")
	if len(failedJob.Result) != 0 {
		t.Fatalf("queued result = %s, want null", failedJob.Result)
	}
	if _, err := update(failedJob, "running", json.RawMessage(`{"not":"terminal"}`)); !errors.Is(err, ErrInvalidJobTransition) {
		t.Fatalf("running update with result error = %v, want ErrInvalidJobTransition", err)
	}
	claimed, err := f.store.ClaimJob(ctx, f.userID, failedJob.ID, at.Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	if len(claimed.Result) != 0 {
		t.Errorf("running result = %s, want null", claimed.Result)
	}
	if _, err := update(claimed, "done", json.RawMessage(`{"broken":`)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("done update with invalid JSON error = %v, want ErrInvalidInput", err)
	}
	if _, err := update(claimed, "failed", json.RawMessage(`{"must":"not persist"}`)); !errors.Is(err, ErrInvalidJobTransition) {
		t.Fatalf("failed update with result error = %v, want ErrInvalidJobTransition", err)
	}
	failed, err := update(claimed, "failed", nil)
	if err != nil {
		t.Fatalf("failed UpdateJob() error = %v", err)
	}
	if len(failed.Result) != 0 {
		t.Errorf("failed result = %s, want null", failed.Result)
	}

	doneJob := create("done-result")
	doneClaim, err := f.store.ClaimJob(ctx, f.userID, doneJob.ID, at.Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimJob(done) error = %v", err)
	}
	wantResult := json.RawMessage(`{"suggestions":[{"term":"Haus"}]}`)
	done, err := update(doneClaim, "done", wantResult)
	if err != nil {
		t.Fatalf("done UpdateJob() error = %v", err)
	}
	loaded, err := f.store.GetJob(ctx, f.userID, doneJob.ID)
	if err != nil {
		t.Fatalf("GetJob(done) error = %v", err)
	}
	if string(done.Result) != string(wantResult) || string(loaded.Result) != string(wantResult) {
		t.Errorf("done results = %s / %s, want %s", done.Result, loaded.Result, wantResult)
	}
}

func TestJobTransitionsRejectStaleAndInvalidUpdates(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*testing.T, *storeFixture, Job, time.Time)
		apply        func(context.Context, *Store, int64, string, int64, time.Time)
		wantStatus   string
		wantProgress int64
	}{
		{
			name:       "queued cannot be claimed through update",
			wantStatus: "queued",
			apply: func(ctx context.Context, store *Store, userID int64, id string, attempt int64, at time.Time) {
				_, err := store.UpdateJob(ctx, JobUpdate{
					ID: id, UserID: userID, Attempt: attempt, Status: "running", Progress: 0, Total: 3, UpdatedAt: at,
				})
				if !errors.Is(err, ErrInvalidJobTransition) {
					t.Errorf("UpdateJob() error = %v, want ErrInvalidJobTransition", err)
				}
			},
		},
		{
			name:       "queued cannot skip to done",
			wantStatus: "queued",
			apply: func(ctx context.Context, store *Store, userID int64, id string, attempt int64, at time.Time) {
				_, err := store.UpdateJob(ctx, JobUpdate{
					ID: id, UserID: userID, Attempt: attempt, Status: "done", Progress: 0, Total: 3, UpdatedAt: at,
				})
				if !errors.Is(err, ErrInvalidJobTransition) {
					t.Errorf("UpdateJob() error = %v, want ErrInvalidJobTransition", err)
				}
			},
		},
		{
			name: "running progress cannot decrease", wantStatus: "running", wantProgress: 2,
			setup: func(t *testing.T, f *storeFixture, job Job, at time.Time) {
				t.Helper()
				claimed, err := f.store.ClaimJob(context.Background(), f.userID, job.ID, at)
				if err != nil {
					t.Fatalf("claim job: %v", err)
				}
				if _, err := f.store.UpdateJob(context.Background(), JobUpdate{
					ID: job.ID, UserID: f.userID, Attempt: claimed.Attempt, Status: "running", Progress: 2, Total: 3, UpdatedAt: at.Add(time.Second),
				}); err != nil {
					t.Fatalf("advance job progress: %v", err)
				}
			},
			apply: func(ctx context.Context, store *Store, userID int64, id string, attempt int64, at time.Time) {
				_, err := store.UpdateJob(ctx, JobUpdate{
					ID: id, UserID: userID, Attempt: attempt, Status: "running", Progress: 1, Total: 3, UpdatedAt: at.Add(2 * time.Second),
				})
				if !errors.Is(err, ErrInvalidJobTransition) {
					t.Errorf("decreasing UpdateJob() error = %v, want ErrInvalidJobTransition", err)
				}
			},
		},
		{
			name: "done cannot return to running", wantStatus: "done", wantProgress: 3,
			setup: func(t *testing.T, f *storeFixture, job Job, at time.Time) {
				t.Helper()
				claimed, err := f.store.ClaimJob(context.Background(), f.userID, job.ID, at)
				if err != nil {
					t.Fatalf("claim job: %v", err)
				}
				if _, err := f.store.UpdateJob(context.Background(), JobUpdate{
					ID: job.ID, UserID: f.userID, Attempt: claimed.Attempt, Status: "done", Progress: 3, Total: 3, UpdatedAt: at.Add(time.Second),
				}); err != nil {
					t.Fatalf("complete job: %v", err)
				}
			},
			apply: func(ctx context.Context, store *Store, userID int64, id string, attempt int64, at time.Time) {
				_, err := store.UpdateJob(ctx, JobUpdate{
					ID: id, UserID: userID, Attempt: attempt, Status: "running", Progress: 2, Total: 3, UpdatedAt: at.Add(2 * time.Second),
				})
				if !errors.Is(err, ErrInvalidJobTransition) {
					t.Errorf("stale UpdateJob() error = %v, want ErrInvalidJobTransition", err)
				}
			},
		},
		{
			name: "failed cannot return to running", wantStatus: "failed", wantProgress: 1,
			setup: func(t *testing.T, f *storeFixture, job Job, at time.Time) {
				t.Helper()
				claimed, err := f.store.ClaimJob(context.Background(), f.userID, job.ID, at)
				if err != nil {
					t.Fatalf("claim job: %v", err)
				}
				if _, err := f.store.UpdateJob(context.Background(), JobUpdate{
					ID: job.ID, UserID: f.userID, Attempt: claimed.Attempt, Status: "failed", Progress: 1, Total: 3, UpdatedAt: at.Add(time.Second),
				}); err != nil {
					t.Fatalf("fail job: %v", err)
				}
			},
			apply: func(ctx context.Context, store *Store, userID int64, id string, attempt int64, at time.Time) {
				_, err := store.UpdateJob(ctx, JobUpdate{
					ID: id, UserID: userID, Attempt: attempt, Status: "running", Progress: 1, Total: 3, UpdatedAt: at.Add(2 * time.Second),
				})
				if !errors.Is(err, ErrInvalidJobTransition) {
					t.Errorf("stale UpdateJob() error = %v, want ErrInvalidJobTransition", err)
				}
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newStoreFixture(t)
			at := testDayStart.Add(time.Duration(index) * time.Hour)
			job := Job{
				ID: "transition-job", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
				Status: "queued", Total: 3, CreatedAt: at, UpdatedAt: at,
			}
			if _, err := f.store.CreateJob(context.Background(), job); err != nil {
				t.Fatalf("create job: %v", err)
			}
			if test.setup != nil {
				test.setup(t, f, job, at)
			}
			current, err := f.store.GetJob(context.Background(), f.userID, job.ID)
			if err != nil {
				t.Fatalf("get job before rejected transition: %v", err)
			}
			test.apply(context.Background(), f.store, f.userID, job.ID, current.Attempt, at)
			stored, err := f.store.GetJob(context.Background(), f.userID, job.ID)
			if err != nil {
				t.Fatalf("get job after rejected transition: %v", err)
			}
			if stored.Status != test.wantStatus || stored.Progress != test.wantProgress {
				t.Errorf("job after rejected transition = status %q progress %d, want %q %d",
					stored.Status, stored.Progress, test.wantStatus, test.wantProgress)
			}
		})
	}
}

func TestJobAttemptFencesStaleUpdatesAndRequeues(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	at := testDayStart.Add(11 * time.Hour)
	created, err := f.store.CreateJob(ctx, Job{
		ID: "attempt-fence", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
		Status: "queued", Total: 1, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	first, err := f.store.ClaimJob(ctx, f.userID, created.ID, at.Add(time.Second))
	if err != nil || first.Attempt != 1 {
		t.Fatalf("first ClaimJob() = %+v, %v; want attempt 1", first, err)
	}
	if _, err := f.store.RequeueRunningJobs(ctx, at.Add(2*time.Second)); err != nil {
		t.Fatalf("RequeueRunningJobs() error = %v", err)
	}
	second, err := f.store.ClaimNextJob(ctx, at.Add(3*time.Second))
	if err != nil || second.Attempt != first.Attempt+1 {
		t.Fatalf("second ClaimNextJob() = %+v, %v; want attempt %d", second, err, first.Attempt+1)
	}

	if _, err := f.store.UpdateJob(ctx, JobUpdate{
		ID: created.ID, UserID: f.userID, Attempt: first.Attempt, Status: "done", Progress: 1, Total: 1,
		Result: json.RawMessage(`{"stale":true}`), UpdatedAt: at.Add(4 * time.Second),
	}); !errors.Is(err, ErrInvalidJobTransition) {
		t.Errorf("stale UpdateJob() error = %v, want ErrInvalidJobTransition", err)
	}
	if err := f.store.RequeueJob(ctx, created.ID, first.Attempt, at.Add(4*time.Second)); !errors.Is(err, ErrInvalidJobTransition) {
		t.Errorf("stale RequeueJob() error = %v, want ErrInvalidJobTransition", err)
	}
	current, err := f.store.GetJob(ctx, f.userID, created.ID)
	if err != nil {
		t.Fatalf("GetJob() after stale operations: %v", err)
	}
	if current.Status != "running" || current.Attempt != second.Attempt || current.Progress != 0 || len(current.Result) != 0 {
		t.Fatalf("current job after stale operations = %+v, want current running attempt unchanged", current)
	}
	if _, err := f.store.UpdateJob(ctx, JobUpdate{
		ID: created.ID, UserID: f.userID, Attempt: second.Attempt, Status: "done", Progress: 1, Total: 1,
		UpdatedAt: at.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("current attempt UpdateJob() error = %v", err)
	}
}

func TestUpdateJobResultSizeLimit(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	at := testDayStart.Add(12 * time.Hour)
	makeClaimed := func(id string) Job {
		t.Helper()
		created, err := f.store.CreateJob(ctx, Job{
			ID: id, UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
			Status: "queued", Total: 1, CreatedAt: at, UpdatedAt: at,
		})
		if err != nil {
			t.Fatalf("CreateJob() error = %v", err)
		}
		claimed, err := f.store.ClaimJob(ctx, f.userID, created.ID, at.Add(time.Second))
		if err != nil {
			t.Fatalf("ClaimJob() error = %v", err)
		}
		return claimed
	}
	boundaryResult := json.RawMessage(`"` + strings.Repeat("x", MaxJobResultBytes-2) + `"`)
	if len(boundaryResult) != MaxJobResultBytes || !json.Valid(boundaryResult) {
		t.Fatalf("boundary result length/validity = %d/%v", len(boundaryResult), json.Valid(boundaryResult))
	}
	boundaryJob := makeClaimed("result-at-limit")
	if _, err := f.store.UpdateJob(ctx, JobUpdate{
		ID: boundaryJob.ID, UserID: f.userID, Attempt: boundaryJob.Attempt, Status: "done", Progress: 1, Total: 1,
		Result: boundaryResult, UpdatedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("UpdateJob() at 1 MiB error = %v", err)
	}

	oversizeJob := makeClaimed("result-over-limit")
	oversizeResult := json.RawMessage(`"` + strings.Repeat("x", MaxJobResultBytes-1) + `"`)
	if len(oversizeResult) != MaxJobResultBytes+1 {
		t.Fatalf("oversize result length = %d, want %d", len(oversizeResult), MaxJobResultBytes+1)
	}
	if _, err := f.store.UpdateJob(ctx, JobUpdate{
		ID: oversizeJob.ID, UserID: f.userID, Attempt: oversizeJob.Attempt, Status: "done", Progress: 1, Total: 1,
		Result: oversizeResult, UpdatedAt: at.Add(2 * time.Second),
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("UpdateJob() over 1 MiB error = %v, want ErrInvalidInput", err)
	}
}

func TestConcurrentJobClaimAllowsExactlyOneWorker(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	createdAt := testDayStart.Add(8 * time.Hour)
	job := Job{
		ID: "claim-once", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
		Status: "queued", Total: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if _, err := f.store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	const workers = 16
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			ready <- struct{}{}
			<-start
			_, err := f.store.ClaimJob(ctx, f.userID, job.ID, createdAt.Add(time.Duration(index+1)*time.Second))
			results <- err
		}(index)
	}
	for range workers {
		<-ready
	}
	close(start)
	wait.Wait()
	close(results)

	successes, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInvalidJobTransition):
			rejected++
		default:
			t.Errorf("ClaimJob() error = %v, want success or ErrInvalidJobTransition", err)
		}
	}
	if successes != 1 || rejected != workers-1 {
		t.Fatalf("claim results: %d successes, %d rejected; want 1 and %d", successes, rejected, workers-1)
	}
	stored, err := f.store.GetJob(ctx, f.userID, job.ID)
	if err != nil {
		t.Fatalf("get claimed job: %v", err)
	}
	if stored.Status != "running" {
		t.Errorf("claimed job status = %q, want running", stored.Status)
	}
}

func TestSystemJobClaimAndRecoveryMethods(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	createdAt := testDayStart.Add(9 * time.Hour)
	first := Job{
		ID: "system-first", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
		Status: "queued", Total: 2, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	second := Job{
		ID: "system-second", UserID: f.otherID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
		Status: "queued", Total: 1, CreatedAt: createdAt.Add(time.Second), UpdatedAt: createdAt.Add(time.Second),
	}
	for _, job := range []Job{first, second} {
		if _, err := f.store.CreateJob(ctx, job); err != nil {
			t.Fatalf("CreateJob(%q) error = %v", job.ID, err)
		}
	}

	claimedAt := createdAt.Add(time.Minute)
	claimed, err := f.store.ClaimNextJob(ctx, claimedAt)
	if err != nil {
		t.Fatalf("ClaimNextJob() error = %v", err)
	}
	if claimed.ID != first.ID || claimed.UserID != f.userID || claimed.Status != "running" || !claimed.UpdatedAt.Equal(claimedAt) {
		t.Errorf("first system claim = %+v", claimed)
	}
	if claimed.Attempt != 1 {
		t.Errorf("first system claim attempt = %d, want 1", claimed.Attempt)
	}

	recoveryAt := claimedAt.Add(time.Minute)
	count, err := f.store.RequeueRunningJobs(ctx, recoveryAt)
	if err != nil || count != 1 {
		t.Fatalf("RequeueRunningJobs() = (%d, %v), want (1, nil)", count, err)
	}
	recovered, err := f.store.GetJob(ctx, f.userID, first.ID)
	if err != nil {
		t.Fatalf("GetJob() recovered row error = %v", err)
	}
	if recovered.Status != "queued" || !recovered.UpdatedAt.Equal(recoveryAt) {
		t.Errorf("recovered row = %+v, want queued at injected time", recovered)
	}
	if recovered.Attempt != claimed.Attempt {
		t.Errorf("recovered attempt = %d, want unchanged %d", recovered.Attempt, claimed.Attempt)
	}

	claimed, err = f.store.ClaimNextJob(ctx, recoveryAt.Add(time.Second))
	if err != nil || claimed.ID != first.ID {
		t.Fatalf("ClaimNextJob() after recovery = %+v, %v; want oldest %q", claimed, err, first.ID)
	}
	if claimed.Attempt != recovered.Attempt+1 {
		t.Fatalf("claim after recovery attempt = %d, want %d", claimed.Attempt, recovered.Attempt+1)
	}
	if err := f.store.RequeueJob(ctx, claimed.ID, claimed.Attempt, recoveryAt.Add(2*time.Second)); err != nil {
		t.Fatalf("RequeueJob() error = %v", err)
	}
	queued, err := f.store.GetJob(ctx, f.userID, first.ID)
	if err != nil || queued.Status != "queued" {
		t.Errorf("explicitly requeued job = %+v, error = %v", queued, err)
	}
	if err := f.store.RequeueJob(ctx, "missing", 0, recoveryAt); !errors.Is(err, ErrInvalidJobTransition) {
		t.Errorf("RequeueJob() for missing id error = %v, want ErrInvalidJobTransition", err)
	}
}

func TestClaimNextJobConcurrentClaimsAreUnique(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	createdAt := testDayStart.Add(10 * time.Hour)
	job := Job{
		ID: "claim-next-once", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
		Status: "queued", Total: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if _, err := f.store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := f.store.ClaimNextJob(ctx, createdAt.Add(time.Duration(index+1)*time.Second))
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	successes, empty := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrNotFound):
			empty++
		default:
			t.Errorf("ClaimNextJob() error = %v, want success or ErrNotFound", err)
		}
	}
	if successes != 1 || empty != workers-1 {
		t.Fatalf("claim-next results: %d successes, %d empty; want 1 and %d", successes, empty, workers-1)
	}
}

func TestStoreRejectsInvalidInputs(t *testing.T) {
	f := newStoreFixture(t)
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "empty app setting JSON", run: func() error {
			_, err := f.store.SetAppSetting(context.Background(), AppSetting{Key: "bad", Value: json.RawMessage("{"), UpdatedAt: testDayStart})
			return err
		}},
		{name: "usage timestamp outside window", run: func() error {
			input := f.reserveInput(f.userID, testDayStart.Add(24*time.Hour), 1)
			_, err := f.store.ReserveUsage(context.Background(), input)
			return err
		}},
		{name: "negative token count", run: func() error {
			return f.store.CompleteUsage(context.Background(), f.userID, 1, -1, 0)
		}},
		{name: "invalid cache JSON", run: func() error {
			return f.store.SetCache(context.Background(), CacheEntry{
				Key: make([]byte, 32), Model: "model", PromptVersion: 1,
				Response: json.RawMessage("not json"), CreatedAt: testDayStart,
			})
		}},
		{name: "invalid job status", run: func() error {
			_, err := f.store.CreateJob(context.Background(), Job{
				ID: "bad", UserID: f.userID, Kind: "deck_enrich", Payload: json.RawMessage(`{}`),
				Status: "unknown", CreatedAt: testDayStart, UpdatedAt: testDayStart,
			})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
