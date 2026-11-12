// Package jobs provides an in-process worker pool for persisted background jobs.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/llmstore"
)

var (
	ErrInvalidConfig  = errors.New("invalid jobs worker configuration")
	ErrAlreadyRunning = errors.New("jobs worker is already running")
)

const (
	jobHandlerFailedMessage = "job handler failed"
	unknownJobKindMessage   = "unsupported job kind"
	defaultPollInterval     = 250 * time.Millisecond
)

// Handler processes one claimed job and returns a JSON result on success. The
// worker stores only a generic failure message; handler errors are deliberately
// not persisted or returned.
type Handler func(context.Context, llmstore.Job) (json.RawMessage, error)

// Repository is the subset of llmstore used by workers.
type Repository interface {
	ClaimNextJob(context.Context, time.Time) (llmstore.Job, error)
	RequeueRunningJobs(context.Context, time.Time) (int64, error)
	RequeueJob(context.Context, string, int64, time.Time) error
	GetJob(context.Context, int64, string) (llmstore.Job, error)
	UpdateJob(context.Context, llmstore.JobUpdate) (llmstore.Job, error)
}

// Config controls the number of concurrent handlers and empty-queue polling.
type Config struct {
	Workers      int
	PollInterval time.Duration
}

// Worker claims and executes persisted jobs using the configured handlers.
type Worker struct {
	repository   Repository
	clock        clock.Clock
	handlers     map[string]Handler
	workers      int
	pollInterval time.Duration

	runMu   sync.Mutex
	running bool
}

// New constructs a worker pool. Workers must be positive. A zero poll interval
// selects a short default suitable for the in-process queue.
func New(repository Repository, clk clock.Clock, handlers map[string]Handler, config Config) (*Worker, error) {
	if repository == nil || clk == nil || config.Workers <= 0 || config.PollInterval < 0 {
		return nil, ErrInvalidConfig
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollInterval
	}
	copyHandlers := make(map[string]Handler, len(handlers))
	for kind, handler := range handlers {
		copyHandlers[kind] = handler
	}
	return &Worker{
		repository:   repository,
		clock:        clk,
		handlers:     copyHandlers,
		workers:      config.Workers,
		pollInterval: config.PollInterval,
	}, nil
}

// RunOne claims and processes at most one job. It is suitable for deterministic
// tests and manual queue draining. A canceled handler is returned to the queue.
// The bool reports whether a job was claimed.
func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	if w == nil || ctx == nil {
		return false, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	job, err := w.repository.ClaimNextJob(ctx, w.clock.Now().UTC())
	if errors.Is(err, llmstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim queued job: %w", err)
	}

	handler := w.handlers[job.Kind]
	if handler == nil {
		return true, w.finish(ctx, job, "failed", job.Progress, safeError(unknownJobKindMessage), nil)
	}

	result, handlerErr := handler(ctx, job)
	if ctx.Err() != nil {
		if err := w.requeueCanceled(ctx, job); err != nil {
			return true, err
		}
		return true, ctx.Err()
	}

	if handlerErr != nil {
		return true, w.finish(ctx, job, "failed", job.Progress, safeError(jobHandlerFailedMessage), nil)
	}
	if len(result) > llmstore.MaxJobResultBytes {
		return true, w.finish(ctx, job, "failed", job.Progress, safeError(jobHandlerFailedMessage), nil)
	}
	if len(result) > 0 && !json.Valid(result) {
		return true, w.finish(ctx, job, "failed", job.Progress, safeError(jobHandlerFailedMessage), nil)
	}
	return true, w.finish(ctx, job, "done", job.Total, nil, result)
}

// Run recovers interrupted jobs once, then starts the configured pool. Parent
// context cancellation is graceful: active claims are requeued and Run returns
// nil after all workers exit.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrInvalidConfig
	}
	w.runMu.Lock()
	if w.running {
		w.runMu.Unlock()
		return ErrAlreadyRunning
	}
	w.running = true
	w.runMu.Unlock()
	defer func() {
		w.runMu.Lock()
		w.running = false
		w.runMu.Unlock()
	}()

	if ctx.Err() != nil {
		return nil
	}
	if _, err := w.repository.RequeueRunningJobs(ctx, w.clock.Now().UTC()); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("recover interrupted jobs: %w", err)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	var wait sync.WaitGroup
	for range w.workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := w.runLoop(workerCtx); err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
			}
		}()
	}
	wait.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (w *Worker) runLoop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		claimed, err := w.RunOne(ctx)
		if err != nil {
			if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				return nil
			}
			return err
		}
		if claimed {
			continue
		}

		timer := time.NewTimer(w.pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) finish(
	ctx context.Context,
	job llmstore.Job,
	status string,
	progress int64,
	message *string,
	result json.RawMessage,
) error {
	if status == "failed" {
		current, err := w.repository.GetJob(ctx, job.UserID, job.ID)
		if err != nil {
			if ctx.Err() != nil {
				if requeueErr := w.requeueCanceled(ctx, job); requeueErr != nil {
					return requeueErr
				}
				return ctx.Err()
			}
			return fmt.Errorf("read job progress before failure: %w", err)
		}
		if current.Attempt != job.Attempt || current.Status != "running" {
			if ctx.Err() != nil {
				if requeueErr := w.requeueCanceled(ctx, job); requeueErr != nil {
					return requeueErr
				}
				return ctx.Err()
			}
			return llmstore.ErrInvalidJobTransition
		}
		progress = current.Progress
		job.Total = current.Total
	}
	_, err := w.repository.UpdateJob(ctx, llmstore.JobUpdate{
		ID:        job.ID,
		UserID:    job.UserID,
		Attempt:   job.Attempt,
		Status:    status,
		Progress:  progress,
		Total:     job.Total,
		Error:     message,
		Result:    result,
		UpdatedAt: w.clock.Now().UTC(),
	})
	if err != nil && ctx.Err() != nil {
		if requeueErr := w.requeueCanceled(ctx, job); requeueErr != nil {
			return requeueErr
		}
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("persist job result: %w", err)
	}
	return nil
}

func (w *Worker) requeueCanceled(ctx context.Context, job llmstore.Job) error {
	// Shutdown must still be able to persist the queue transition after the
	// handler's context has been canceled.
	if err := w.repository.RequeueJob(context.WithoutCancel(ctx), job.ID, job.Attempt, w.clock.Now().UTC()); err != nil {
		if errors.Is(err, llmstore.ErrInvalidJobTransition) {
			return nil
		}
		return fmt.Errorf("requeue canceled job: %w", err)
	}
	return nil
}

func safeError(message string) *string {
	return &message
}
