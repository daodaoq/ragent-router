// Package bulkhead provides concurrency limiting via a semaphore pattern,
// preventing a single slow or failing dependency from consuming all
// available goroutines (the "bulkhead" resilience pattern).
package bulkhead

import (
	"context"
	"errors"
)

// ErrBulkheadFull is returned when the bulkhead has reached its
// concurrency limit and cannot accept new work.
var ErrBulkheadFull = errors.New("bulkhead: concurrency limit reached")

// Bulkhead limits the number of concurrently executing operations
// using a buffered channel as a counting semaphore.
//
// Unlike a simple mutex, a bulkhead allows a configurable level of
// parallelism—when the limit is reached, additional callers are
// rejected immediately rather than queuing (fail-fast semantics).
type Bulkhead struct {
	sem chan struct{}
}

// New creates a Bulkhead that allows at most maxConcurrent
// simultaneous operations.
func New(maxConcurrent int) *Bulkhead {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Bulkhead{
		sem: make(chan struct{}, maxConcurrent),
	}
}

// Execute runs fn if the bulkhead has capacity. If the limit is reached,
// it returns ErrBulkheadFull immediately without blocking.
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error {
	select {
	case b.sem <- struct{}{}:
		defer func() { <-b.sem }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrBulkheadFull
	}
}

// Available returns the number of free slots in the bulkhead.
func (b *Bulkhead) Available() int {
	return cap(b.sem) - len(b.sem)
}

// InUse returns the number of currently occupied slots.
func (b *Bulkhead) InUse() int {
	return len(b.sem)
}

// Capacity returns the maximum concurrent operations.
func (b *Bulkhead) Capacity() int {
	return cap(b.sem)
}
