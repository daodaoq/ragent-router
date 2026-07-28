package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// 指数退避测试
// =============================================================================

func TestExponentialBackoff_FullJitter(t *testing.T) {
	cfg := Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxAttempts: 3,
		Jitter:      FullJitter,
	}
	b := NewExponentialBackoff(cfg)

	// FullJitter: delay ∈ [0, cap]
	// attempt 0: cap = 100ms → delay ∈ [0, 100ms]
	for i := 0; i < 100; i++ {
		delay := b.Next(0)
		if delay < 0 || delay > 100*time.Millisecond {
			t.Errorf("FullJitter attempt 0: delay=%v 不在 [0, 100ms] 范围", delay)
		}
	}

	// attempt 1: cap = 200ms → delay ∈ [0, 200ms]
	for i := 0; i < 100; i++ {
		delay := b.Next(1)
		if delay < 0 || delay > 200*time.Millisecond {
			t.Errorf("FullJitter attempt 1: delay=%v 不在 [0, 200ms] 范围", delay)
		}
	}
}

func TestExponentialBackoff_EqualJitter(t *testing.T) {
	cfg := Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxAttempts: 3,
		Jitter:      EqualJitter,
	}
	b := NewExponentialBackoff(cfg)

	// EqualJitter: delay ∈ [cap/2, cap]
	// attempt 0: cap = 100ms → delay ∈ [50ms, 100ms]
	for i := 0; i < 100; i++ {
		delay := b.Next(0)
		if delay < 50*time.Millisecond || delay > 100*time.Millisecond {
			t.Errorf("EqualJitter attempt 0: delay=%v 不在 [50ms, 100ms] 范围", delay)
		}
	}
}

func TestExponentialBackoff_DecorrelatedJitter(t *testing.T) {
	cfg := Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxAttempts: 5,
		Jitter:      DecorrelatedJitter,
	}
	b := NewExponentialBackoff(cfg)

	// DecorrelatedJitter: delay ≤ cap
	// 每次尝试的 cap = min(maxDelay, baseDelay * 2^attempt)
	for attempt := 0; attempt < 5; attempt++ {
		capMs := 100.0 * float64(int(1)<<uint(attempt))
		if capMs > 5000 {
			capMs = 5000
		}
		for i := 0; i < 100; i++ {
			delay := b.Next(attempt)
			if delay < 0 || delay > time.Duration(capMs)*time.Millisecond {
				t.Errorf("Decorrelated attempt %d: delay=%v 超过 cap=%vms", attempt, delay, capMs)
			}
		}
	}
}

func TestExponentialBackoff_MaxDelay(t *testing.T) {
	cfg := Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    500 * time.Millisecond,
		MaxAttempts: 10,
		Jitter:      FullJitter,
	}
	b := NewExponentialBackoff(cfg)

	// attempt 10: cap = 100ms * 2^10 = 102.4s，但被限制为 500ms
	for i := 0; i < 100; i++ {
		delay := b.Next(10)
		if delay > 500*time.Millisecond {
			t.Errorf("超过 MaxDelay: delay=%v, max=500ms", delay)
		}
	}
}

func TestExponentialBackoff_NegativeAttempt(t *testing.T) {
	b := NewExponentialBackoff(DefaultConfig())
	// 负数 attempt 应被修正为 0
	delay := b.Next(-5)
	if delay < 0 {
		t.Errorf("负数 attempt 返回负延迟: %v", delay)
	}
}

func TestExponentialBackoff_MaxAttempts(t *testing.T) {
	cfg := Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		MaxAttempts: 5,
		Jitter:      FullJitter,
	}
	b := NewExponentialBackoff(cfg)
	if b.MaxAttempts() != 5 {
		t.Errorf("MaxAttempts: want=5, got=%d", b.MaxAttempts())
	}
}

func TestExponentialBackoff_DefensiveValidation(t *testing.T) {
	// 非法配置应被修正为默认值
	b := NewExponentialBackoff(Config{
		BaseDelay:   0,
		MaxDelay:    0,
		MaxAttempts: 0,
	})
	if b.MaxAttempts() != 3 {
		t.Errorf("MaxAttempts=0 应被修正为 3, got=%d", b.MaxAttempts())
	}
}

// =============================================================================
// Do 执行器测试
// =============================================================================

func TestDo_Success(t *testing.T) {
	cfg := DefaultConfig()
	b := NewExponentialBackoff(cfg)

	var attempts atomic.Int32
	err := Do(context.Background(), b, 3, func() error {
		attempts.Add(1)
		if attempts.Load() < 3 {
			return errors.New("not yet")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("应成功, got=%v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("尝试次数: want=3, got=%d", attempts.Load())
	}
}

func TestDo_AllFail(t *testing.T) {
	cfg := DefaultConfig()
	b := NewExponentialBackoff(cfg)

	var attempts atomic.Int32
	err := Do(context.Background(), b, 2, func() error {
		attempts.Add(1)
		return errors.New("always fail")
	})

	if err == nil {
		t.Fatal("所有尝试失败应返回错误")
	}
	// attempt 0 + 2 次重试 = 3 次
	if attempts.Load() != 3 {
		t.Errorf("尝试次数: want=3, got=%d", attempts.Load())
	}
}

func TestDo_ZeroAttempts(t *testing.T) {
	cfg := DefaultConfig()
	b := NewExponentialBackoff(cfg)

	var attempts atomic.Int32
	err := Do(context.Background(), b, 0, func() error {
		attempts.Add(1)
		return errors.New("fail")
	})

	if err == nil {
		t.Fatal("0 次重试 + 失败应返回错误")
	}
	// maxAttempts=0 → 只执行 1 次
	if attempts.Load() != 1 {
		t.Errorf("尝试次数: want=1, got=%d", attempts.Load())
	}
}

func TestDo_ContextCancel(t *testing.T) {
	cfg := Config{
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		MaxAttempts: 10,
		Jitter:      FullJitter,
	}
	b := NewExponentialBackoff(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Do(ctx, b, 10, func() error {
		return errors.New("fail")
	})

	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("context 取消应返回错误")
	}
	// 应在约 100ms 内返回，不会等待 10 次重试
	if elapsed > 500*time.Millisecond {
		t.Errorf("context 取消后耗时过长: %v", elapsed)
	}
}

func TestDo_ContextPreCancelled(t *testing.T) {
	cfg := DefaultConfig()
	b := NewExponentialBackoff(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := Do(ctx, b, 3, func() error {
		return errors.New("should not run")
	})

	if err == nil {
		t.Fatal("已取消的 context 应返回错误")
	}
}

// =============================================================================
// DefaultConfig 测试
// =============================================================================

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BaseDelay != 100*time.Millisecond {
		t.Errorf("BaseDelay: want=100ms, got=%s", cfg.BaseDelay)
	}
	if cfg.MaxDelay != 30*time.Second {
		t.Errorf("MaxDelay: want=30s, got=%s", cfg.MaxDelay)
	}
	if cfg.MaxAttempts != 3 {
		t.Errorf("MaxAttempts: want=3, got=%d", cfg.MaxAttempts)
	}
	if cfg.Jitter != FullJitter {
		t.Errorf("Jitter: want=FullJitter, got=%d", cfg.Jitter)
	}
}
