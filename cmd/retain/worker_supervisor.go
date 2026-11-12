package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var (
	ErrWorkerRestartLimit = errors.New("background jobs worker restart limit reached")
	ErrWorkerSupervisor   = errors.New("background jobs supervisor stopped unexpectedly")
)

const (
	workerRestartAttempts = 5
	workerRestartBase     = 100 * time.Millisecond
	workerRestartMax      = 5 * time.Second
)

type workerRunFunc func(context.Context) error
type workerSleepFunc func(context.Context, time.Duration) error

func superviseWorker(
	ctx context.Context,
	run workerRunFunc,
	sleep workerSleepFunc,
	setAvailable func(bool),
	logger *slog.Logger,
) (result error) {
	defer func() {
		if recover() != nil {
			result = ErrWorkerSupervisor
			if setAvailable != nil {
				setAvailable(false)
			}
		}
	}()
	if ctx == nil || run == nil || sleep == nil {
		return ErrWorkerSupervisor
	}

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return nil
		}
		if setAvailable != nil {
			setAvailable(true)
		}
		// All unexpected exits are retried identically; keep the underlying error
		// out of logs because repository/provider details may be sensitive.
		_ = run(ctx)
		if setAvailable != nil {
			setAvailable(false)
		}
		if ctx.Err() != nil {
			return nil
		}

		if attempt >= workerRestartAttempts {
			if logger != nil {
				logger.Error("background jobs worker restart limit reached", "component", "jobs", "attempts", attempt)
			}
			return ErrWorkerRestartLimit
		}

		backoff := workerRestartBase << (attempt - 1)
		if backoff > workerRestartMax {
			backoff = workerRestartMax
		}
		if logger != nil {
			logger.Warn("background jobs worker stopped; restarting", "component", "jobs", "attempt", attempt, "backoff", backoff)
		}
		if err := sleep(ctx, backoff); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return ErrWorkerSupervisor
		}
	}
}

func sleepWorkerBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitForWorkerResult(ctx context.Context, results <-chan error) error {
	select {
	case err := <-results:
		return err
	default:
	}
	select {
	case err := <-results:
		return err
	case <-ctx.Done():
		select {
		case err := <-results:
			return err
		default:
		}
		return fmt.Errorf("wait for background jobs supervisor: %w", ctx.Err())
	}
}
