// Package timeout provides cascading timeout control using Go's context
// package. Each component in a request pipeline can add its own deadline,
// and the most restrictive deadline automatically propagates upstream.
package timeout

import (
	"context"
	"time"
)

// Cascading creates a context tree where child deadlines are constrained
// by their parent. The effective timeout is min(parent_deadline, child_timeout).
//
// This prevents the common bug where a retry loop with a 10s per-attempt
// timeout runs for 50s because the parent has no deadline set.
//
// Usage:
//
//	rootCtx := timeout.Cascading(ctx.Background(), 30*time.Second) // total budget
//	for i := 0; i < 3; i++ {
//	    attemptCtx := timeout.Cascading(rootCtx, 10*time.Second)   // per-attempt limit
//	    err := doRequest(attemptCtx)
//	    // ...
//	}
func Cascading(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	// Check if parent already has a tighter deadline.
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			// Parent deadline is tighter—use it directly.
			return context.WithCancel(parent)
		}
	}
	return context.WithTimeout(parent, timeout)
}

// WithBudget creates a context with a total time budget, suitable for
// multi-stage operations where each stage consumes part of the budget.
func WithBudget(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, budget)
}

// Remaining returns the time left before the context's deadline.
// Returns 0 if the context has no deadline or is already expired.
func Remaining(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}
