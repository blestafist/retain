package llm

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerHalfOpenProbeAndRecovery(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := circuitBreaker{threshold: 1, cooldown: time.Minute}

	initial, err := breaker.acquire(now)
	if err != nil {
		t.Fatalf("initial allow() error = %v", err)
	}
	breaker.failure(initial, now)
	if _, err := breaker.acquire(now.Add(time.Second)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("allow before cooldown = %v, want ErrCircuitOpen", err)
	}
	probe, err := breaker.acquire(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("half-open probe allow() error = %v", err)
	}
	if _, err := breaker.acquire(now.Add(time.Minute)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("concurrent half-open allow() = %v, want ErrCircuitOpen", err)
	}
	breaker.success(probe)
	if _, err := breaker.acquire(now.Add(2 * time.Minute)); err != nil {
		t.Fatalf("allow after successful probe = %v", err)
	}
}

func TestCircuitBreakerIgnoresStaleParallelSuccess(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := circuitBreaker{threshold: 1, cooldown: time.Minute}
	stale, err := breaker.acquire(now)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := breaker.acquire(now)
	if err != nil {
		t.Fatal(err)
	}

	finishStale := make(chan struct{})
	staleDone := make(chan struct{})
	go func() {
		defer close(staleDone)
		<-finishStale
		breaker.success(stale)
	}()

	breaker.failure(failed, now)
	probe, err := breaker.acquire(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("half-open probe acquire() error = %v", err)
	}
	close(finishStale)
	<-staleDone

	if _, err := breaker.acquire(now.Add(time.Minute)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("acquire after stale success = %v, want ErrCircuitOpen", err)
	}
	breaker.success(probe)
	if _, err := breaker.acquire(now.Add(2 * time.Minute)); err != nil {
		t.Fatalf("acquire after probe success = %v", err)
	}
}

func TestCircuitBreakerReleaseReturnsProbeToOpen(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := circuitBreaker{threshold: 1, cooldown: time.Minute}
	initial, err := breaker.acquire(now)
	if err != nil {
		t.Fatal(err)
	}
	breaker.failure(initial, now)
	probe, err := breaker.acquire(now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	breaker.release(probe, now.Add(time.Minute))
	if _, err := breaker.acquire(now.Add(time.Minute)); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("acquire after released probe = %v, want ErrCircuitOpen", err)
	}
}
