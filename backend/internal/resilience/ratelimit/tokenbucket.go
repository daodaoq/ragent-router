// Package ratelimit provides a high-performance token bucket rate limiter
// with lazy refill strategy and sharded concurrent storage.
package ratelimit

import (
	"sync"
	"time"
)

// Clock abstracts time operations for testability.
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

// TokenBucket implements the token bucket rate-limiting algorithm with
// lazy refill: time-based token replenishment is deferred until the bucket
// is empty, avoiding syscall overhead on the hot path.
//
// Each Allow() call on the fast path (tokens > 0) costs exactly one mutex
// lock + one integer decrement.
type TokenBucket struct {
	fillInterval time.Duration // time to generate one token (1e9 / rate)
	capacity     uint64        // max tokens the bucket can hold
	tokens       uint64        // current available tokens
	lastRefill   time.Time     // timestamp of the most recent refill
	clock        Clock
	mu           sync.Mutex
}

// NewTokenBucket creates a TokenBucket with the given rate (tokens/second)
// and capacity (max burst size). The bucket starts full.
func NewTokenBucket(rate float64, capacity uint64) *TokenBucket {
	return NewTokenBucketWithClock(rate, capacity, nil)
}

// NewTokenBucketWithClock creates a TokenBucket with an injectable clock.
func NewTokenBucketWithClock(rate float64, capacity uint64, clock Clock) *TokenBucket {
	if clock == nil {
		clock = realClock{}
	}
	if capacity < 1 {
		capacity = 1
	}
	if rate < 1e-9 {
		rate = 1e-9
	}

	return &TokenBucket{
		fillInterval: time.Duration(float64(time.Second) / rate),
		capacity:     capacity,
		tokens:       capacity,
		lastRefill:   clock.Now(),
		clock:        clock,
	}
}

// Allow checks whether a request should be permitted. Returns true if
// the request is within the rate limit and consumes one token.
//
// Hot path: when tokens > 0, only decrements the counter without
// computing time deltas.
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Fast path: bucket still has tokens, consume immediately.
	if tb.tokens > 0 {
		tb.tokens--
		return true
	}

	// Slow path: bucket is empty, compute refill.
	elapsed := tb.clock.Since(tb.lastRefill)
	tokensToAdd := uint64(elapsed / tb.fillInterval)

	if tokensToAdd == 0 {
		return false
	}

	// Advance the refill timestamp proportionally to tokens added.
	tb.lastRefill = tb.lastRefill.Add(time.Duration(tokensToAdd) * tb.fillInterval)

	// Cap at capacity.
	if tokensToAdd > tb.capacity {
		tokensToAdd = tb.capacity
	}

	tb.tokens = tokensToAdd - 1 // consume one
	return true
}

// Tokens returns the current number of available tokens (for metrics).
func (tb *TokenBucket) Tokens() uint64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.tokens
}

// Rate returns the configured rate in tokens/second.
func (tb *TokenBucket) Rate() float64 {
	return float64(time.Second) / float64(tb.fillInterval)
}

// Capacity returns the maximum tokens the bucket can hold.
func (tb *TokenBucket) Capacity() uint64 {
	return tb.capacity
}
