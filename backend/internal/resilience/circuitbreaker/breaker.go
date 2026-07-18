// Package circuitbreaker implements a three-state circuit breaker pattern
// with sliding-window failure tracking and half-open probing.
//
// States:
//
//	Closed  → normal operation, requests pass through
//	Open    → requests are rejected immediately (fast-fail)
//	HalfOpen → limited probe requests are allowed to test recovery
//
// Transitions:
//
//	Closed → Open:     failure rate exceeds threshold within the window
//	Open   → HalfOpen: timeout expires
//	HalfOpen → Closed: probe request(s) succeed
//	HalfOpen → Open:   probe request(s) fail
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker rejects a request.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// State represents the current state of the circuit breaker.
type State int

const (
	StateClosed   State = iota // normal operation
	StateOpen                  // rejecting all requests
	StateHalfOpen              // allowing limited probes
)

// String returns a human-readable state name.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Bucket represents a time bucket in the sliding window.
type bucket struct {
	failures  int64
	successes int64
}

// CircuitBreaker protects a backend service from cascading failures by
// tripping to an open state when the error rate exceeds a threshold.
//
// The failure rate is computed over a sliding window divided into fixed-size
// buckets. Each bucket aggregates successes and failures for its time slice.
// When the window slides past a bucket, its counts are discarded.
//
// In the half-open state, only a limited number of probe requests are
// allowed. If they succeed, the breaker closes; if any fails, it re-opens.
type CircuitBreaker struct {
	mu sync.Mutex

	state State

	// Configuration
	failureThreshold float64       // fraction of failures that trips the breaker (0.0–1.0)
	windowDuration   time.Duration // total observation window
	bucketDuration   time.Duration // granularity of each bucket
	openTimeout      time.Duration // how long to stay open before half-open
	halfOpenMaxReqs  int           // max requests allowed in half-open state

	// Sliding window buckets (ring buffer)
	buckets      []bucket
	bucketCount  int
	windowStart  time.Time

	// State tracking
	lastFailureTime time.Time
	halfOpenReqs    int
	totalFailures   int64
	totalSuccesses  int64
}

// Config holds the parameters for creating a CircuitBreaker.
type Config struct {
	// FailureThreshold is the error rate (0.0–1.0) that opens the breaker.
	// Default: 0.5 (50%)
	FailureThreshold float64

	// WindowDuration is the observation period for error rate calculation.
	// Default: 10 seconds
	WindowDuration time.Duration

	// BucketCount is the number of time buckets in the sliding window.
	// Higher = smoother rate calculation, more memory. Default: 10.
	BucketCount int

	// OpenTimeout is how long the breaker stays open before transitioning
	// to half-open. Default: 30 seconds.
	OpenTimeout time.Duration

	// HalfOpenMaxReqs is the maximum number of probe requests allowed
	// in half-open state. Default: 1.
	HalfOpenMaxReqs int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 0.5,
		WindowDuration:   10 * time.Second,
		BucketCount:      10,
		OpenTimeout:      30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
}

// New creates a CircuitBreaker with the given configuration.
func New(cfg Config) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 || cfg.FailureThreshold > 1 {
		cfg.FailureThreshold = 0.5
	}
	if cfg.WindowDuration <= 0 {
		cfg.WindowDuration = 10 * time.Second
	}
	if cfg.BucketCount <= 0 {
		cfg.BucketCount = 10
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxReqs <= 0 {
		cfg.HalfOpenMaxReqs = 1
	}

	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: cfg.FailureThreshold,
		windowDuration:   cfg.WindowDuration,
		bucketDuration:   cfg.WindowDuration / time.Duration(cfg.BucketCount),
		openTimeout:      cfg.OpenTimeout,
		halfOpenMaxReqs:  cfg.HalfOpenMaxReqs,
		buckets:          make([]bucket, cfg.BucketCount),
		bucketCount:      cfg.BucketCount,
		windowStart:      time.Now(),
	}
}

// Call executes fn within the circuit breaker's protection.
// Returns ErrCircuitOpen if the breaker is open. Otherwise, fn's error
// is used to update the failure/success statistics.
func (cb *CircuitBreaker) Call(fn func() error) error {
	if err := cb.allowRequest(); err != nil {
		return err
	}

	err := fn()
	cb.recordResult(err)
	return err
}

// allowRequest checks whether a request should be permitted based on
// the current state. It handles state transitions.
func (cb *CircuitBreaker) allowRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.slideWindow()

	switch cb.state {
	case StateClosed:
		// Check if we should trip.
		if cb.shouldTrip() {
			cb.transitionTo(StateOpen)
			return ErrCircuitOpen
		}
		return nil

	case StateOpen:
		// Check if the timeout has expired.
		if time.Since(cb.lastFailureTime) >= cb.openTimeout {
			cb.transitionTo(StateHalfOpen)
			cb.halfOpenReqs++
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		if cb.halfOpenReqs >= cb.halfOpenMaxReqs {
			return ErrCircuitOpen
		}
		cb.halfOpenReqs++
		return nil

	default:
		return ErrCircuitOpen
	}
}

// recordResult updates failure/success statistics and handles state
// transitions based on the result of a request.
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.recordFailure()
	} else {
		cb.recordSuccess()
	}

	// Handle half-open transitions.
	if cb.state == StateHalfOpen {
		if err != nil {
			cb.transitionTo(StateOpen)
		} else if cb.halfOpenReqs >= cb.halfOpenMaxReqs {
			cb.transitionTo(StateClosed)
		}
	}
}

func (cb *CircuitBreaker) recordFailure() {
	idx := cb.currentBucket()
	cb.buckets[idx].failures++
	cb.totalFailures++
	cb.lastFailureTime = time.Now()
}

func (cb *CircuitBreaker) recordSuccess() {
	idx := cb.currentBucket()
	cb.buckets[idx].successes++
	cb.totalSuccesses++
}

// shouldTrip checks whether the failure rate exceeds the threshold.
// Must be called with mu held.
func (cb *CircuitBreaker) shouldTrip() bool {
	var totalFailures, totalSuccesses int64
	for _, b := range cb.buckets {
		totalFailures += b.failures
		totalSuccesses += b.successes
	}

	total := totalFailures + totalSuccesses
	if total == 0 {
		return false
	}

	return float64(totalFailures)/float64(total) >= cb.failureThreshold
}

// currentBucket returns the index of the bucket for the current time.
func (cb *CircuitBreaker) currentBucket() int {
	elapsed := time.Since(cb.windowStart)
	bucketIdx := int(elapsed / cb.bucketDuration)
	if bucketIdx >= cb.bucketCount {
		bucketIdx = cb.bucketCount - 1
	}
	return bucketIdx
}

// slideWindow advances the window and clears expired buckets.
// Must be called with mu held.
func (cb *CircuitBreaker) slideWindow() {
	elapsed := time.Since(cb.windowStart)
	bucketsToSlide := int(elapsed / cb.bucketDuration)

	if bucketsToSlide <= 0 {
		return
	}

	// Slide the window forward.
	for i := 0; i < bucketsToSlide && i < cb.bucketCount; i++ {
		idx := (cb.currentBucket() + 1 + i) % cb.bucketCount
		cb.buckets[idx] = bucket{}
	}

	cb.windowStart = cb.windowStart.Add(time.Duration(bucketsToSlide) * cb.bucketDuration)
}

// transitionTo changes the breaker's state and resets relevant counters.
// Must be called with mu held.
func (cb *CircuitBreaker) transitionTo(newState State) {
	cb.state = newState
	cb.halfOpenReqs = 0

	if newState == StateOpen {
		cb.lastFailureTime = time.Now()
	}
}

// State returns the current state (thread-safe).
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Stats returns aggregate statistics for monitoring.
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	var failures, successes int64
	for _, b := range cb.buckets {
		failures += b.failures
		successes += b.successes
	}

	return Stats{
		State:         cb.state,
		TotalFailures: cb.totalFailures,
		TotalSuccesses: cb.totalSuccesses,
		WindowFailures: failures,
		WindowSuccesses: successes,
	}
}

// Stats holds the circuit breaker's current statistics.
type Stats struct {
	State           State
	TotalFailures   int64
	TotalSuccesses  int64
	WindowFailures  int64
	WindowSuccesses int64
}
