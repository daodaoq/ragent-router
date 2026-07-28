package circuitbreaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// 熔断器基本功能测试
// =============================================================================

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := New(DefaultConfig())
	if cb.State() != StateClosed {
		t.Fatalf("初始状态: want=closed, got=%s", cb.State())
	}
}

func TestCircuitBreaker_BasicFlow(t *testing.T) {
	// 验证 Closed → Open → HalfOpen → Closed 完整状态转换
	cfg := Config{
		FailureThreshold: 0.5,
		WindowDuration:   2 * time.Second,
		BucketCount:      4,
		OpenTimeout:      500 * time.Millisecond,
		HalfOpenMaxReqs:  1,
	}
	cb := New(cfg)

	// ── Closed 状态：成功请求不触发熔断 ──
	for i := 0; i < 5; i++ {
		if err := cb.Call(func() error { return nil }); err != nil {
			t.Fatalf("Closed 状态成功请求不应报错: %v", err)
		}
	}
	if cb.State() != StateClosed {
		t.Fatalf("成功请求后应保持 Closed, got=%s", cb.State())
	}

	// ── 连续失败触发熔断 → Open ──
	for i := 0; i < 10; i++ {
		cb.Call(func() error { return errors.New("failure") })
	}
	if cb.State() != StateOpen {
		t.Fatalf("高失败率后应变为 Open, got=%s", cb.State())
	}

	// ── Open 状态下请求被拒绝 ──
	err := cb.Call(func() error { return nil })
	if err != ErrCircuitOpen {
		t.Fatalf("Open 状态应返回 ErrCircuitOpen, got=%v", err)
	}

	// ── 等待冷却后进入 HalfOpen ──
	time.Sleep(600 * time.Millisecond)
	err = cb.Call(func() error { return nil }) // 探测请求
	if err != nil {
		t.Fatalf("HalfOpen 探测请求不应报错: %v", err)
	}
	if cb.State() != StateClosed {
		t.Fatalf("探测成功后应恢复 Closed, got=%s", cb.State())
	}
}

func TestCircuitBreaker_FailureThreshold(t *testing.T) {
	cfg := Config{
		FailureThreshold: 0.5,
		WindowDuration:   10 * time.Second,
		BucketCount:      10,
		OpenTimeout:      30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
	cb := New(cfg)

	// 4 成功 + 3 失败 = 43% 失败率，不触发（< 50%）
	for i := 0; i < 4; i++ {
		cb.Call(func() error { return nil })
	}
	for i := 0; i < 3; i++ {
		cb.Call(func() error { return errors.New("fail") })
	}
	if cb.State() != StateClosed {
		t.Fatalf("43%% 失败率不应触发熔断, got=%s", cb.State())
	}

	// 再加 2 个失败 → 5/9 = 55%（>= 50% 阈值），触发熔断
	// 注意：shouldTrip 在 Call 之前检查（不含当前请求），
	// 所以需要第 10 个请求（5/9=55%）才会触发。
	cb.Call(func() error { return errors.New("fail") }) // 8th: 4/7 checked, still < 50%
	cb.Call(func() error { return errors.New("fail") }) // 9th: 5/8 checked = 62% ≥ 50% → trigger!
	if cb.State() != StateOpen {
		t.Fatalf("超过 50%% 失败率应触发熔断, got=%s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cfg := Config{
		FailureThreshold: 0.5,
		WindowDuration:   1 * time.Second,
		BucketCount:      2,
		OpenTimeout:      200 * time.Millisecond,
		HalfOpenMaxReqs:  1,
	}
	cb := New(cfg)

	// 触发熔断
	for i := 0; i < 5; i++ {
		cb.Call(func() error { return errors.New("fail") })
	}
	if cb.State() != StateOpen {
		t.Fatalf("应为 Open, got=%s", cb.State())
	}

	// 等待冷却 → HalfOpen
	time.Sleep(250 * time.Millisecond)

	// 探测失败 → 重新打开
	err := cb.Call(func() error { return errors.New("probe fail") })
	if err == nil {
		t.Fatal("HalfOpen 探测失败应返回错误")
	}
	if cb.State() != StateOpen {
		t.Fatalf("探测失败后应重新打开, got=%s", cb.State())
	}
}

// =============================================================================
// 并发安全测试
// =============================================================================

func TestCircuitBreaker_Concurrent(t *testing.T) {
	cfg := Config{
		FailureThreshold: 0.5,
		WindowDuration:   10 * time.Second,
		BucketCount:      10,
		OpenTimeout:      1 * time.Second,
		HalfOpenMaxReqs:  1,
	}
	cb := New(cfg)

	const goroutines = 100
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	var successCount, rejectCount atomic.Int64

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := cb.Call(func() error {
					// 模拟 50% 成功率
					if j%2 == 0 {
						return nil
					}
					return errors.New("fail")
				})
				if err == ErrCircuitOpen {
					rejectCount.Add(1)
				} else if err == nil {
					successCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// 验证没有 panic 或死锁，且有合理的成功/拒绝计数
	total := successCount.Load() + rejectCount.Load()
	t.Logf("并发测试: success=%d, rejected=%d, total=%d", successCount.Load(), rejectCount.Load(), total)
	if total == 0 {
		t.Fatal("没有请求被处理")
	}
}

func TestCircuitBreaker_ConcurrentStateTransition(t *testing.T) {
	// 验证并发状态转换不会导致数据竞争
	cfg := Config{
		FailureThreshold: 0.3,
		WindowDuration:   100 * time.Millisecond,
		BucketCount:      5,
		OpenTimeout:      50 * time.Millisecond,
		HalfOpenMaxReqs:  2,
	}
	cb := New(cfg)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				cb.Call(func() error {
					if (id+j)%3 == 0 {
						return errors.New("fail")
					}
					return nil
				})
				// 同时读取状态
				_ = cb.State()
				_ = cb.Stats()
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// 统计快照测试
// =============================================================================

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := New(DefaultConfig())

	// 5 次成功
	for i := 0; i < 5; i++ {
		cb.Call(func() error { return nil })
	}
	// 3 次失败
	for i := 0; i < 3; i++ {
		cb.Call(func() error { return errors.New("fail") })
	}

	stats := cb.Stats()
	if stats.TotalSuccesses != 5 {
		t.Errorf("TotalSuccesses: want=5, got=%d", stats.TotalSuccesses)
	}
	if stats.TotalFailures != 3 {
		t.Errorf("TotalFailures: want=3, got=%d", stats.TotalFailures)
	}
	if stats.State != StateClosed {
		t.Errorf("State: want=closed, got=%s", stats.State)
	}
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.FailureThreshold != 0.5 {
		t.Errorf("FailureThreshold: want=0.5, got=%f", cfg.FailureThreshold)
	}
	if cfg.WindowDuration != 10*time.Second {
		t.Errorf("WindowDuration: want=10s, got=%s", cfg.WindowDuration)
	}
	if cfg.BucketCount != 10 {
		t.Errorf("BucketCount: want=10, got=%d", cfg.BucketCount)
	}
	if cfg.OpenTimeout != 30*time.Second {
		t.Errorf("OpenTimeout: want=30s, got=%s", cfg.OpenTimeout)
	}
	if cfg.HalfOpenMaxReqs != 1 {
		t.Errorf("HalfOpenMaxReqs: want=1, got=%d", cfg.HalfOpenMaxReqs)
	}
}

func TestCircuitBreaker_DefensiveValidation(t *testing.T) {
	// 非法配置应被修正为默认值
	cb := New(Config{
		FailureThreshold: -1,
		WindowDuration:   0,
		BucketCount:      0,
		OpenTimeout:      0,
		HalfOpenMaxReqs:  0,
	})
	if cb.State() != StateClosed {
		t.Fatalf("非法配置创建的熔断器应为 Closed, got=%s", cb.State())
	}
}
