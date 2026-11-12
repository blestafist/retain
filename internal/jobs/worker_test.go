package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
	"github.com/pestit/retain/internal/llmstore"
)

var workerTestTime = time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type workerFixture struct {
	store   *llmstore.Store
	userID  int64
	otherID int64
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "jobs.db"))
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
		Email: "owner@example.test", Name: "Owner", PasswordHash: "hash", CreatedAt: workerTestTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := queries.CreateUser(context.Background(), gen.CreateUserParams{
		Email: "other@example.test", Name: "Other", PasswordHash: "hash", CreatedAt: workerTestTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &workerFixture{store: llmstore.New(queries), userID: owner.ID, otherID: other.ID}
}

func (f *workerFixture) createJob(t *testing.T, id, kind string, userID int64, total int64) llmstore.Job {
	t.Helper()
	job, err := f.store.CreateJob(context.Background(), llmstore.Job{
		ID: id, UserID: userID, Kind: kind, Payload: json.RawMessage(`{"value":1}`),
		Status: "queued", Total: total, CreatedAt: workerTestTime, UpdatedAt: workerTestTime,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func newTestWorker(t *testing.T, f *workerFixture, handlers map[string]Handler, count int) *Worker {
	t.Helper()
	worker, err := New(f.store, fixedClock{now: workerTestTime.Add(time.Minute)}, handlers, Config{
		Workers: count, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return worker
}

func TestRunOneCompletesOrFailsJobsSafely(t *testing.T) {
	tests := []struct {
		name          string
		kind          string
		handlerResult json.RawMessage
		handlerErr    error
		handlers      bool
		wantStatus    string
		wantError     string
		wantProg      int64
		wantResult    string
	}{
		{name: "success persists result", kind: "ok", handlerResult: json.RawMessage(`{"suggestions":[{"term":"Haus"}]}`), handlers: true, wantStatus: "done", wantProg: 3, wantResult: `{"suggestions":[{"term":"Haus"}]}`},
		{name: "handler error is sanitized and drops result", kind: "broken", handlerResult: json.RawMessage(`{"private":"must not leak"}`), handlers: true, handlerErr: errors.New("provider body contains api_secret and prompt"), wantStatus: "failed", wantError: jobHandlerFailedMessage},
		{name: "unknown kind is safe", kind: "unknown", wantStatus: "failed", wantError: unknownJobKindMessage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkerFixture(t)
			f.createJob(t, "job", test.kind, f.userID, 3)
			handlers := map[string]Handler{}
			if test.handlers {
				handlers[test.kind] = func(context.Context, llmstore.Job) (json.RawMessage, error) {
					return test.handlerResult, test.handlerErr
				}
			}
			worker := newTestWorker(t, f, handlers, 1)

			claimed, err := worker.RunOne(context.Background())
			if err != nil || !claimed {
				t.Fatalf("RunOne() = (%v, %v), want a claimed job", claimed, err)
			}
			stored, err := f.store.GetJob(context.Background(), f.userID, "job")
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			if stored.Status != test.wantStatus || stored.Progress != test.wantProg {
				t.Errorf("job status/progress = %q/%d, want %q/%d", stored.Status, stored.Progress, test.wantStatus, test.wantProg)
			}
			if test.wantError == "" {
				if stored.Error != nil {
					t.Errorf("job error = %q, want nil", *stored.Error)
				}
			} else if stored.Error == nil || *stored.Error != test.wantError || strings.Contains(*stored.Error, "secret") || strings.Contains(*stored.Error, "prompt") {
				t.Errorf("stored error = %v, want safe %q", stored.Error, test.wantError)
			}
			if string(stored.Result) != test.wantResult {
				t.Errorf("stored result = %s, want %s", stored.Result, test.wantResult)
			}
			if !stored.UpdatedAt.Equal(workerTestTime.Add(time.Minute)) {
				t.Errorf("UpdatedAt = %s, want injected clock timestamp", stored.UpdatedAt)
			}
		})
	}
}

func TestRunRecoversRunningJobsAtStartup(t *testing.T) {
	f := newWorkerFixture(t)
	job := f.createJob(t, "interrupted", "recover", f.otherID, 2)
	if _, err := f.store.ClaimJob(context.Background(), f.otherID, job.ID, workerTestTime.Add(time.Second)); err != nil {
		t.Fatalf("simulate interrupted claim: %v", err)
	}

	var calls atomic.Int64
	worker := newTestWorker(t, f, map[string]Handler{
		"recover": func(context.Context, llmstore.Job) (json.RawMessage, error) {
			calls.Add(1)
			return nil, nil
		},
	}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		stored, err := f.store.GetJob(context.Background(), f.otherID, job.ID)
		if err == nil && stored.Status == "done" {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("recovered job did not finish (handler calls: %d)", calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	stored, err := f.store.GetJob(context.Background(), f.otherID, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if stored.Status != "done" || calls.Load() != 1 {
		t.Errorf("recovered job status/calls = %q/%d, want done/1", stored.Status, calls.Load())
	}
}

func TestConcurrentRunOneClaimsAJobOnce(t *testing.T) {
	f := newWorkerFixture(t)
	f.createJob(t, "single", "count", f.userID, 1)
	var calls atomic.Int64
	worker := newTestWorker(t, f, map[string]Handler{
		"count": func(context.Context, llmstore.Job) (json.RawMessage, error) { calls.Add(1); return nil, nil },
	}, 8)

	const contenders = 24
	start := make(chan struct{})
	var wait sync.WaitGroup
	var mu sync.Mutex
	claimed := 0
	errs := make([]error, 0)
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			got, err := worker.RunOne(context.Background())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if got {
				claimed++
			}
		}()
	}
	close(start)
	wait.Wait()
	if len(errs) != 0 {
		t.Fatalf("RunOne() errors = %v", errs)
	}
	if claimed != 1 || calls.Load() != 1 {
		t.Fatalf("claims/handler calls = %d/%d, want 1/1", claimed, calls.Load())
	}
}

func TestRunOneCancellationRequeuesClaim(t *testing.T) {
	f := newWorkerFixture(t)
	job := f.createJob(t, "cancel", "wait", f.userID, 1)
	started := make(chan struct{})
	worker := newTestWorker(t, f, map[string]Handler{
		"wait": func(ctx context.Context, _ llmstore.Job) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return json.RawMessage(`{"private":true}`), ctx.Err()
		},
	}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		claimed, err := worker.RunOne(ctx)
		if !claimed && err == nil {
			done <- errors.New("RunOne did not claim the job")
			return
		}
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOne() error = %v, want context.Canceled", err)
	}
	stored, err := f.store.GetJob(context.Background(), f.userID, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if stored.Status != "queued" {
		t.Errorf("canceled job status = %q, want queued", stored.Status)
	}
	if len(stored.Result) != 0 {
		t.Errorf("canceled job result = %s, want null", stored.Result)
	}
}

func TestRunTreatsStaleCancellationRequeueAsBenign(t *testing.T) {
	f := newWorkerFixture(t)
	job := f.createJob(t, "stale-cancel", "wait", f.userID, 1)
	started := make(chan struct{})
	worker := newTestWorker(t, f, map[string]Handler{
		"wait": func(ctx context.Context, _ llmstore.Job) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("worker did not start handler")
	}
	if _, err := f.store.RequeueRunningJobs(context.Background(), workerTestTime.Add(2*time.Minute)); err != nil {
		cancel()
		t.Fatalf("simulate recovery: %v", err)
	}
	newClaim, err := f.store.ClaimJob(context.Background(), f.userID, job.ID, workerTestTime.Add(3*time.Minute))
	if err != nil {
		cancel()
		t.Fatalf("reclaim job: %v", err)
	}
	if newClaim.Attempt != 2 {
		cancel()
		t.Fatalf("reclaimed attempt = %d, want 2", newClaim.Attempt)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run() after stale cancellation requeue = %v, want graceful nil", err)
	}
	current, err := f.store.GetJob(context.Background(), f.userID, job.ID)
	if err != nil {
		t.Fatalf("read re-claimed job: %v", err)
	}
	if current.Status != "running" || current.Attempt != newClaim.Attempt {
		t.Errorf("job after old worker cancellation = status %q attempt %d, want running attempt %d",
			current.Status, current.Attempt, newClaim.Attempt)
	}
}

func TestRunOneResultSizeLimitAndMonotonicFailureProgress(t *testing.T) {
	tests := []struct {
		name         string
		resultSize   int
		wantStatus   string
		wantError    bool
		wantProgress int64
	}{
		{name: "exact limit is accepted", resultSize: llmstore.MaxJobResultBytes, wantStatus: "done", wantProgress: 2},
		{name: "over limit fails safely", resultSize: llmstore.MaxJobResultBytes + 1, wantStatus: "failed", wantError: true, wantProgress: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkerFixture(t)
			f.createJob(t, "size-limit", "size", f.userID, 2)
			result := json.RawMessage(`"` + strings.Repeat("x", test.resultSize-2) + `"`)
			if len(result) != test.resultSize || !json.Valid(result) {
				t.Fatalf("handler result length/validity = %d/%v", len(result), json.Valid(result))
			}
			worker := newTestWorker(t, f, map[string]Handler{
				"size": func(ctx context.Context, job llmstore.Job) (json.RawMessage, error) {
					_, err := f.store.UpdateJob(ctx, llmstore.JobUpdate{
						ID: job.ID, UserID: job.UserID, Attempt: job.Attempt,
						Status: "running", Progress: 1, Total: job.Total,
						UpdatedAt: workerTestTime.Add(30 * time.Second),
					})
					if err != nil {
						return nil, err
					}
					return result, nil
				},
			}, 1)
			claimed, err := worker.RunOne(context.Background())
			if err != nil || !claimed {
				t.Fatalf("RunOne() = (%v, %v), want claimed job", claimed, err)
			}
			stored, err := f.store.GetJob(context.Background(), f.userID, "size-limit")
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			if stored.Status != test.wantStatus {
				t.Fatalf("job status = %q, want %q", stored.Status, test.wantStatus)
			}
			if stored.Progress != test.wantProgress {
				t.Errorf("job progress = %d, want monotonic progress %d", stored.Progress, test.wantProgress)
			}
			if test.wantError {
				if stored.Error == nil || *stored.Error != jobHandlerFailedMessage {
					t.Errorf("oversize result error = %v, want generic handler failure", stored.Error)
				}
				if len(stored.Result) != 0 {
					t.Errorf("oversize result persisted: %d bytes", len(stored.Result))
				}
			} else if len(stored.Result) != llmstore.MaxJobResultBytes {
				t.Errorf("boundary result size = %d, want %d", len(stored.Result), llmstore.MaxJobResultBytes)
			}
		})
	}
}

func TestRunPreservesJobOwnershipOnTerminalUpdate(t *testing.T) {
	f := newWorkerFixture(t)
	job := f.createJob(t, "owned", "ownership", f.otherID, 2)
	var seenUserID atomic.Int64
	worker := newTestWorker(t, f, map[string]Handler{
		"ownership": func(_ context.Context, got llmstore.Job) (json.RawMessage, error) {
			seenUserID.Store(got.UserID)
			return json.RawMessage(`{"ok":true}`), nil
		},
	}, 1)
	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("RunOne() = (%v, %v), want a claim", claimed, err)
	}
	if seenUserID.Load() != f.otherID {
		t.Errorf("handler owner = %d, want %d", seenUserID.Load(), f.otherID)
	}
	if _, err := f.store.GetJob(context.Background(), f.userID, job.ID); !errors.Is(err, llmstore.ErrNotFound) {
		t.Errorf("GetJob() for foreign owner error = %v, want ErrNotFound", err)
	}
	stored, err := f.store.GetJob(context.Background(), f.otherID, job.ID)
	if err != nil || stored.Status != "done" {
		t.Errorf("owner job = %+v, error = %v, want done", stored, err)
	}
	if string(stored.Result) != `{"ok":true}` {
		t.Errorf("owner job result = %s, want handler result", stored.Result)
	}
}

func TestNewValidatesConfiguration(t *testing.T) {
	f := newWorkerFixture(t)
	tests := []struct {
		name   string
		config Config
	}{
		{name: "zero workers", config: Config{Workers: 0}},
		{name: "negative polling interval", config: Config{Workers: 1, PollInterval: -time.Second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(f.store, fixedClock{now: workerTestTime}, nil, test.config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
	if _, err := New(f.store, fixedClock{now: workerTestTime}, nil, Config{Workers: 1}); err != nil {
		t.Errorf("New() with default polling interval error = %v", err)
	}
}

func TestRunRejectsConcurrentInvocation(t *testing.T) {
	f := newWorkerFixture(t)
	started := make(chan struct{})
	worker := newTestWorker(t, f, map[string]Handler{
		"block": func(ctx context.Context, _ llmstore.Job) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, 1)
	f.createJob(t, "blocked", "block", f.userID, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { firstDone <- worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start handler")
	}
	if err := worker.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("concurrent Run() error = %v, want ErrAlreadyRunning", err)
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Errorf("first Run() error = %v", err)
	}
}
