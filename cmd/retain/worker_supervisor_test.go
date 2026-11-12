package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSuperviseWorkerRestartsAfterRunFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	restarted := make(chan struct{})
	delays := make(chan time.Duration, 2)
	available := make(chan bool, 4)
	result := make(chan error, 1)
	go func() {
		result <- superviseWorker(ctx, func(ctx context.Context) error {
			if calls.Add(1) == 1 {
				return errors.New("transient repository error")
			}
			close(restarted)
			<-ctx.Done()
			return ctx.Err()
		}, func(_ context.Context, delay time.Duration) error {
			delays <- delay
			return nil
		}, func(value bool) { available <- value }, nil, func() time.Time { return now })
	}()

	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("worker was not restarted after Run failed")
	}
	if got := <-delays; got != workerRestartBase {
		t.Fatalf("first restart delay = %s, want %s", got, workerRestartBase)
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("superviseWorker() after cancellation = %v, want nil", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("Run calls = %d, want 2", got)
	}
}

func TestSuperviseWorkerCancellationIsGraceful(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- superviseWorker(ctx, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}, sleepWorkerBackoff, nil, nil, func() time.Time { return now })
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("superviseWorker() = %v, want nil on cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop after cancellation")
	}
}

func TestSuperviseWorkerStopsAfterBoundedAttempts(t *testing.T) {
	var calls atomic.Int32
	var delays []time.Duration
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	err := superviseWorker(context.Background(), func(context.Context) error {
		calls.Add(1)
		return errors.New("private repository error")
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}, nil, nil, func() time.Time { return now })
	if !errors.Is(err, ErrWorkerRestartLimit) {
		t.Fatalf("superviseWorker() error = %v, want restart limit", err)
	}
	if calls.Load() != workerRestartAttempts {
		t.Errorf("Run calls = %d, want %d", calls.Load(), workerRestartAttempts)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	if len(delays) != len(want) {
		t.Fatalf("backoff delays = %v, want %v", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Errorf("backoff delay %d = %s, want %s", i, delays[i], want[i])
		}
	}
}

func TestSuperviseWorkerResetsRestartBudgetAfterStableRun(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	var calls int
	var delays []time.Duration
	err := superviseWorker(context.Background(), func(context.Context) error {
		calls++
		if calls == workerRestartAttempts {
			now = now.Add(workerStableRunTime)
		}
		return errors.New("transient repository error")
	}, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}, nil, nil, func() time.Time { return now })

	if !errors.Is(err, ErrWorkerRestartLimit) {
		t.Fatalf("superviseWorker() error = %v, want restart limit", err)
	}
	if calls != 2*workerRestartAttempts-1 {
		t.Fatalf("Run calls = %d, want %d after stable run resets the budget", calls, 2*workerRestartAttempts-1)
	}
	if len(delays) != calls-1 {
		t.Fatalf("backoff delays = %d, want %d", len(delays), calls-1)
	}
	for index, delay := range delays {
		wantAttempt := index%(workerRestartAttempts-1) + 1
		want := workerRestartBase << (wantAttempt - 1)
		if delay != want {
			t.Errorf("backoff delay %d = %s, want %s", index, delay, want)
		}
	}
}

func TestWaitForWorkerResultHonorsShutdownDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForWorkerResult(ctx, make(chan error)); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForWorkerResult() error = %v, want canceled context", err)
	}
}
