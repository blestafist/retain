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
	workerStableRunTime   = time.Minute
)

type workerRunFunc func(context.Context) error
type workerSleepFunc func(context.Context, time.Duration) error

func superviseWorker(
	ctx context.Context,
	run workerRunFunc,
	sleep workerSleepFunc,
	setAvailable func(bool),
	logger *slog.Logger,
	now func() time.Time,
) (result error) {
	defer func() {
		if recover() != nil {
			result = ErrWorkerSupervisor
			if setAvailable != nil {
				setAvailable(false)
			}
		}
	}()
	if ctx == nil || run == nil || sleep == nil || now == nil {
		return ErrWorkerSupervisor
	}

	restartCount := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		if setAvailable != nil {
			setAvailable(true)
		}
		runStartedAt := now()
		// All unexpected exits are retried identically; keep the underlying error
		// out of logs because repository/provider details may be sensitive.
		_ = run(ctx)
		if now().Sub(runStartedAt) >= workerStableRunTime {
			restartCount = 0
		}
		if setAvailable != nil {
			setAvailable(false)
		}
		if ctx.Err() != nil {
			return nil
		}

		restartCount++
		if restartCount >= workerRestartAttempts {
			if logger != nil {
				logger.Error("background jobs worker restart limit reached", "component", "jobs", "attempts", restartCount)
			}
			return ErrWorkerRestartLimit
		}

		backoff := workerRestartBase << (restartCount - 1)
		if backoff > workerRestartMax {
			backoff = workerRestartMax
		}
		if logger != nil {
			logger.Warn("background jobs worker stopped; restarting", "component", "jobs", "attempt", restartCount, "backoff", backoff)
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
