package llm

import (
	"sync"
	"time"
)

type breakerState uint8

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

type circuitBreaker struct {
	mu          sync.Mutex
	state       breakerState
	consecutive int
	openUntil   time.Time
	generation  uint64
	threshold   int
	cooldown    time.Duration
}

// breakerLease identifies the breaker generation in which a call was admitted.
// Outcomes from older generations cannot change the current breaker state.
type breakerLease struct {
	generation uint64
	probe      bool
}

func (b *circuitBreaker) acquire(now time.Time) (breakerLease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case breakerOpen:
		if now.Before(b.openUntil) {
			return breakerLease{}, ErrCircuitOpen
		}
		b.state = breakerHalfOpen
		b.generation++
		return breakerLease{generation: b.generation, probe: true}, nil
	case breakerHalfOpen:
		return breakerLease{}, ErrCircuitOpen
	default:
		return breakerLease{generation: b.generation}, nil
	}
}

func (b *circuitBreaker) success(lease breakerLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lease.generation != b.generation {
		return
	}
	if lease.probe && b.state == breakerHalfOpen {
		b.state = breakerClosed
		b.consecutive = 0
		b.openUntil = time.Time{}
		b.generation++
		return
	}
	if !lease.probe && b.state == breakerClosed {
		b.consecutive = 0
	}
}

func (b *circuitBreaker) failure(lease breakerLease, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lease.generation != b.generation {
		return
	}

	if lease.probe && b.state == breakerHalfOpen {
		b.consecutive++
		b.open(now)
		return
	}
	if lease.probe || b.state != breakerClosed {
		return
	}
	b.consecutive++
	if b.consecutive >= b.threshold {
		b.open(now)
	}
}

// release returns an unfinished half-open probe to open state. This is used
// for cancellation and every other exit that did not report a provider outcome.
func (b *circuitBreaker) release(lease breakerLease, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lease.generation == b.generation && lease.probe && b.state == breakerHalfOpen {
		b.open(now)
	}
}

func (b *circuitBreaker) open(now time.Time) {
	b.state = breakerOpen
	b.openUntil = now.Add(b.cooldown)
	b.generation++
}
