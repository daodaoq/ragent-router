package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// 固定时钟（用于精确控制时间）
// =============================================================================

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time                  { return c.now }
func (c *fixedClock) Since(t time.Time) time.Duration { return c.now.Sub(t) }
func (c *fixedClock) Advance(d time.Duration)         { c.now = c.now.Add(d) }

// =============================================================================
// 基本功能测试
// =============================================================================

func TestTokenBucket_BasicAllow(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(10, 5, clock) // rate=10/s, capacity=5

	// 初始满桶，应能消耗 5 个
	for i := 0; i < 5; i++ {
		if !tb.Allow() {
			t.Fatalf("第 %d 次请求应被放行", i+1)
		}
	}

	// 第 6 个应被拒绝（桶空，且时间未推进）
	if tb.Allow() {
		t.Fatal("桶空后应被限流")
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(10, 5, clock) // rate=10/s → 1 token/100ms

	// 耗尽所有 Token
	for i := 0; i < 5; i++ {
		tb.Allow()
	}

	// 推进 100ms → 补充 1 个 Token
	clock.Advance(100 * time.Millisecond)
	if !tb.Allow() {
		t.Fatal("推进 100ms 后应补充 1 个 Token")
	}

	// 再次耗尽
	if tb.Allow() {
		t.Fatal("补充的 Token 被消耗后应再次限流")
	}

	// 推进 500ms → 补充 5 个（但不超过 capacity）
	clock.Advance(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if !tb.Allow() {
			t.Fatalf("推进 500ms 后第 %d 次请求应被放行", i+1)
		}
	}
}

func TestTokenBucket_Capacity(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(100, 3, clock) // capacity=3

	// 耗尽初始 Token
	for i := 0; i < 3; i++ {
		tb.Allow()
	}
	if tb.Allow() {
		t.Fatal("桶空后应被限流")
	}

	// 空闲很久后，补充的 Token 不应超过 capacity
	clock.Advance(10 * time.Second) // 理论上可补充 1000 个，但 cap 为 3
	if tb.Tokens() > 3 {
		t.Errorf("Tokens 不应超过 capacity: got=%d", tb.Tokens())
	}

	// 应能消耗 capacity 个
	count := 0
	for tb.Allow() {
		count++
	}
	if count != 3 {
		t.Errorf("应最多放行 capacity=3 个, got=%d", count)
	}
}

func TestTokenBucket_PrecisionRefill(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(10, 100, clock) // rate=10/s, capacity=100

	// 耗尽初始 Token
	for i := 0; i < 100; i++ {
		tb.Allow()
	}
	if tb.Allow() {
		t.Fatal("桶空后应被限流")
	}

	// 推进 5 秒 → 触发一次 Allow 来补充（Tokens() 不会触发 refill）
	clock.Advance(5 * time.Second)
	if !tb.Allow() {
		t.Fatal("推进 5s 后应能 Allow")
	}

	// Allow 内部补充了 50 个（rate=10/s × 5s），消耗 1 个后剩余 49
	if tb.Tokens() != 49 {
		t.Errorf("精确补充后 Tokens: want=49, got=%d", tb.Tokens())
	}
}

func TestTokenBucket_TokensAndRate(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(100, 50, clock)

	if tb.Rate() != 100 {
		t.Errorf("Rate: want=100, got=%f", tb.Rate())
	}
	if tb.Capacity() != 50 {
		t.Errorf("Capacity: want=50, got=%d", tb.Capacity())
	}
	if tb.Tokens() != 50 {
		t.Errorf("初始 Tokens: want=50, got=%d", tb.Tokens())
	}
}

func TestTokenBucket_DefensiveValidation(t *testing.T) {
	clock := &fixedClock{now: time.Now()}

	// capacity=0 应被修正为 1
	tb := NewTokenBucketWithClock(10, 0, clock)
	if tb.Capacity() != 1 {
		t.Errorf("capacity=0 应被修正为 1, got=%d", tb.Capacity())
	}

	// rate=0 应被修正为最小值
	tb2 := NewTokenBucketWithClock(0, 5, clock)
	if tb2.Rate() <= 0 {
		t.Errorf("rate=0 应被修正为正值, got=%f", tb2.Rate())
	}
}

// =============================================================================
// 并发安全测试
// =============================================================================

func TestTokenBucket_Concurrent(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(1000, 100, clock)

	const goroutines = 100
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	var allowed atomic.Int64

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if tb.Allow() {
					allowed.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// 并发消耗不应超过 capacity + (并发期间补充的 token)
	// 由于使用固定时钟且不推进时间，不应有补充
	allowedCount := allowed.Load()
	if allowedCount > 100 {
		t.Errorf("并发消耗超过 capacity: allowed=%d, capacity=100", allowedCount)
	}
	t.Logf("并发测试: goroutines=%d, iterations=%d, allowed=%d", goroutines, iterations, allowedCount)
}

func TestTokenBucket_ConcurrentAllowAndRefill(t *testing.T) {
	// 验证并发 Allow + 时间推进不会导致数据竞争
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(100, 50, clock)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// 一半 goroutine 消耗 Token
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tb.Allow()
			}
		}()
	}

	// 一半 goroutine 推进时间
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				clock.Advance(time.Millisecond)
				_ = tb.Tokens()
			}
		}()
	}

	wg.Wait()
}

func TestTokenBucket_NilClock(t *testing.T) {
	// nil clock 应使用 realClock
	tb := NewTokenBucket(100, 50)
	if !tb.Allow() {
		t.Fatal("nil clock 应使用 realClock，初始应允许请求")
	}
}

// =============================================================================
// Benchmark
// =============================================================================

func BenchmarkTokenBucket_Allow_HotPath(b *testing.B) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(1000000, uint64(b.N), clock) // 保证不会桶空

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow()
	}
}

func BenchmarkTokenBucket_Allow_ColdPath(b *testing.B) {
	clock := &fixedClock{now: time.Now()}
	tb := NewTokenBucketWithClock(1000000, 1, clock)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clock.Advance(time.Microsecond)
		tb.Allow()
	}
}
