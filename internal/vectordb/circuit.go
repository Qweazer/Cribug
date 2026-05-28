package vectordb

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type circuitState int

const (
	circuitClosed   circuitState = iota
	circuitOpen     circuitState = iota
	circuitHalfOpen circuitState = iota
)

type circuitBreaker struct {
	mu          sync.Mutex
	state       circuitState
	failures    int
	maxFailures int
	openTimeout time.Duration
	openedAt    time.Time
}

func newCircuitBreaker(maxFailures int, openTimeout time.Duration) *circuitBreaker {
	if maxFailures <= 0 {
		maxFailures = 3
	}
	if openTimeout <= 0 {
		openTimeout = 30 * time.Second
	}
	return &circuitBreaker{
		state:       circuitClosed,
		maxFailures: maxFailures,
		openTimeout: openTimeout,
	}
}

func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		return true
	case circuitHalfOpen:
		return true
	case circuitOpen:
		if time.Since(cb.openedAt) >= cb.openTimeout {
			cb.state = circuitHalfOpen
			return true
		}
		return false
	default:
		return false
	}
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = circuitClosed
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	if cb.failures >= cb.maxFailures {
		cb.state = circuitOpen
		cb.openedAt = time.Now()
	}
}

func (cb *circuitBreaker) do(ctx context.Context, fn func() error) error {
	if !cb.allow() {
		return fmt.Errorf("circuit breaker is open")
	}

	err := fn()
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}
