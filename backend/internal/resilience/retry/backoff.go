// Package retry provides exponential backoff strategies with jitter
// for distributed system retry logic.
//
// Jitter is critical in distributed systems: without it, clients that
// hit a rate limit simultaneously will retry simultaneously (the
// "thundering herd" problem). Adding randomness spreads retries across
// the backoff window.
package retry

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// Strategy defines a backoff delay computation.
type Strategy interface {
	// Next returns the duration to wait before the next retry attempt.
	// attempt is 0-indexed (first retry = attempt 0).
	Next(attempt int) time.Duration
}

// Config holds the parameters for exponential backoff.
type Config struct {
	// BaseDelay is the initial delay before the first retry.
	// Default: 100ms
	BaseDelay time.Duration

	// MaxDelay is the upper bound on any single retry delay.
	// Default: 30s
	MaxDelay time.Duration

	// MaxAttempts is the maximum number of retries.
	// Default: 3
	MaxAttempts int

	// Jitter specifies the jitter strategy.
	// Default: FullJitter
	Jitter JitterStrategy
}

// JitterStrategy names the jitter algorithm to use.
type JitterStrategy int

const (
	// FullJitter: delay = random(0, cap). Best for avoiding thundering herd.
	FullJitter JitterStrategy = iota

	// EqualJitter: delay = cap/2 + random(0, cap/2). Preserves more of
	// the original backoff timing while still spreading retries.
	EqualJitter

	// DecorrelatedJitter: delay = min(cap, random(base, cap * 3)).
	// Each retry's delay is independent of the previous, avoiding
	// correlated retry storms across nodes.
	DecorrelatedJitter
)

// DefaultConfig returns sensible defaults for API call retries.
func DefaultConfig() Config {
	return Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		MaxAttempts: 3,
		Jitter:      FullJitter,
	}
}

// ExponentialBackoff computes exponential backoff delays with configurable jitter.
type ExponentialBackoff struct {
	baseDelay   time.Duration
	maxDelay    time.Duration
	maxAttempts int
	jitter      JitterStrategy
}

// NewExponentialBackoff creates a new backoff strategy from config.
func NewExponentialBackoff(cfg Config) *ExponentialBackoff {
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}

	return &ExponentialBackoff{
		baseDelay:   cfg.BaseDelay,
		maxDelay:    cfg.MaxDelay,
		maxAttempts: cfg.MaxAttempts,
		jitter:      cfg.Jitter,
	}
}

// Next computes the delay for the given retry attempt.
func (b *ExponentialBackoff) Next(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	// Exponential growth: baseDelay * 2^attempt
	cap := float64(b.baseDelay) * math.Pow(2, float64(attempt))
	if cap > float64(b.maxDelay) {
		cap = float64(b.maxDelay)
	}

	var delay float64
	switch b.jitter {
	case FullJitter:
		delay = rand.Float64() * cap

	case EqualJitter:
		half := cap / 2
		delay = half + rand.Float64()*half

	case DecorrelatedJitter:
		// Each attempt's delay is independent but bounded.
		prev := float64(b.baseDelay)
		if attempt > 0 {
			prev = float64(b.baseDelay) * math.Pow(2, float64(attempt-1))
		}
		delay = math.Min(cap, prev*3*rand.Float64())

	default:
		delay = rand.Float64() * cap
	}

	return time.Duration(delay)
}

// MaxAttempts returns the configured max retry count.
func (b *ExponentialBackoff) MaxAttempts() int {
	return b.maxAttempts
}

// Do executes fn with retry logic. It returns the first successful
// result, or the last error if all attempts are exhausted.
// The context is checked before each attempt.
func Do(ctx context.Context, strategy Strategy, maxAttempts int, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= maxAttempts; attempt++ {
		// Check context before each attempt.
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		default:
		}

		if attempt > 0 {
			delay := strategy.Next(attempt - 1) // first retry = attempt 1
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr != nil {
					return lastErr
				}
				return ctx.Err()
			case <-timer.C:
			}
		}

		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
	}

	return lastErr
}
