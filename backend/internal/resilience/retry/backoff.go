// Package retry 提供指数退避重试策略与 Jitter 抖动算法。
//
// # 为什么需要 Jitter
//
// 在分布式系统中，如果多个客户端同时被限流（如 API 返回 429），
// 它们会在同一时刻发起重试，再次撞上限流——形成"惊群效应"
// （thundering herd）。Jitter 通过给每次重试的等待时间加随机偏移，
// 将集中的重试分散到整个退避窗口内。
//
// 本包提供三种抖动策略，适用不同场景：
//
//   - Full Jitter（推荐）：delay = random(0, cap)
//     最激进的随机化，最适合避免惊群。
//   - Equal Jitter：delay = cap/2 + random(0, cap/2)
//     折中方案，保留一半的确定性时序。
//   - Decorrelated Jitter：delay = min(cap, random(base*3, cap))
//     每次重试的延迟与前一次无关，适合跨节点场景。
//
// # 指数退避公式
//
//	cap = min(maxDelay, baseDelay * 2^attempt)
//	delay = jitter(cap)
package retry

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// Strategy 定义退避延迟的计算接口。
type Strategy interface {
	// Next 返回第 attempt 次重试前应等待的时长。
	// attempt 从 0 开始（0 = 第一次重试）。
	Next(attempt int) time.Duration
}

// ────────────────────────────────────────────────────────────
// 配置
// ────────────────────────────────────────────────────────────

// Config 是指数退避的参数集合。
type Config struct {
	// BaseDelay 是第一次重试前的初始等待时间。默认 100ms。
	BaseDelay time.Duration

	// MaxDelay 是单次重试等待时间的上限。默认 30s。
	MaxDelay time.Duration

	// MaxAttempts 是最大重试次数（不含初始请求）。默认 3。
	MaxAttempts int

	// Jitter 指定抖动策略。默认 FullJitter。
	Jitter JitterStrategy
}

// JitterStrategy 是抖动算法的枚举类型。
type JitterStrategy int

const (
	// FullJitter：在 [0, cap] 区间均匀随机。
	// 最佳分散效果，推荐用于高并发 API 调用场景。
	FullJitter JitterStrategy = iota

	// EqualJitter：在 [cap/2, cap] 区间均匀随机。
	// 保留一半的确定性延迟，适合需要兼顾定时精度的场景。
	EqualJitter

	// DecorrelatedJitter：本次延迟在 [prev, cap×3] 区间随机，但不超过 cap。
	// 延迟与上游和历史无关，适合跨多个独立节点的重试场景。
	DecorrelatedJitter
)

// DefaultConfig 返回 API 调用场景的合理默认值：
//
//	BaseDelay:   100ms
//	MaxDelay:    30s
//	MaxAttempts: 3 次
//	Jitter:      FullJitter
func DefaultConfig() Config {
	return Config{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		MaxAttempts: 3,
		Jitter:      FullJitter,
	}
}

// ────────────────────────────────────────────────────────────
// 指数退避实现
// ────────────────────────────────────────────────────────────

// ExponentialBackoff 根据配置计算每次重试的等待时间。
type ExponentialBackoff struct {
	baseDelay   time.Duration
	maxDelay    time.Duration
	maxAttempts int
	jitter      JitterStrategy
}

// NewExponentialBackoff 根据配置创建退避策略。
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

// Next 计算第 attempt 次重试的等待时间。
//
// 指数增长公式：cap = baseDelay × 2^attempt，上限 maxDelay。
//
// 示例（baseDelay=100ms, maxDelay=30s）：
//
//	attempt 0 → cap=100ms
//	attempt 1 → cap=200ms
//	attempt 2 → cap=400ms
//	attempt 5 → cap=3.2s
//	attempt 9 → cap=30s（触及上限）
func (b *ExponentialBackoff) Next(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	// 指数增长：baseDelay × 2^attempt
	cap := float64(b.baseDelay) * math.Pow(2, float64(attempt))
	if cap > float64(b.maxDelay) {
		cap = float64(b.maxDelay)
	}

	var delay float64
	switch b.jitter {
	case FullJitter:
		// 在 [0, cap] 区间均匀随机。
		// 例如 cap=1s：可能等 0.1s，也可能等 0.9s，平均 0.5s。
		delay = rand.Float64() * cap

	case EqualJitter:
		// 在 [cap/2, cap] 区间均匀随机。
		// 例如 cap=1s：等待 0.5s-1s，平均 0.75s。
		half := cap / 2
		delay = half + rand.Float64()*half

	case DecorrelatedJitter:
		// 延迟与上次无关：从 [prev, cap×3] 取随机值，但不超过 cap。
		// 这保证了每次重试的时机相互独立，适合多节点场景。
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

// MaxAttempts 返回最大重试次数。
func (b *ExponentialBackoff) MaxAttempts() int {
	return b.maxAttempts
}

// ────────────────────────────────────────────────────────────
// 执行器
// ────────────────────────────────────────────────────────────

// Do 用重试逻辑执行 fn。成功则立即返回 nil，
// 失败则等待退避延迟后重试，直到超过最大尝试次数。
//
// 参数：
//   - ctx：每次重试前检查 ctx 是否已取消，取消则立即返回
//   - strategy：退避延迟计算策略
//   - maxAttempts：最大重试次数（0 = 不重试，仅执行一次）
//   - fn：要执行的函数，返回 nil 视为成功
//
// 返回值：
//   - fn 首次成功 → nil
//   - fn 始终失败 → 最后一次的错误
//   - ctx 被取消 → ctx.Err() 或最后一次 fn 的错误
func Do(ctx context.Context, strategy Strategy, maxAttempts int, fn func() error) error {
	var lastErr error

	// attempt 从 0 到 maxAttempts，共 maxAttempts+1 次尝试。
	// attempt=0 是初始请求（不等待），attempt>0 是重试（等待后退避延迟）。
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		// 每次尝试前检查 context 是否已取消。
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		default:
		}

		// 重试前等待退避延迟（初始请求不等待）。
		if attempt > 0 {
			delay := strategy.Next(attempt - 1)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr != nil {
					return lastErr
				}
				return ctx.Err()
			case <-timer.C:
				// 延迟结束，执行重试。
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
