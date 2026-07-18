// Package circuitbreaker 实现三态熔断器模式（Closed → Open → HalfOpen），
// 使用滑动时间窗口统计失败率，通过半开探测验证服务恢复。
//
// # 为什么需要熔断器
//
// 在分布式系统中，当下游服务故障时，如果不加控制地持续发送请求：
//  1. 请求线程/goroutine 被阻塞，耗尽上游资源（线程池/连接池）
//  2. 故障恢复后，积压的请求形成流量尖峰，可能再次击垮服务
//  3. 级联故障：一个服务挂了 → 调用方也挂了 → 调用方的调用方也挂了
//
// 熔断器通过快速失败（fail-fast）切断这个链条：检测到下游故障 →
// 直接拒绝请求（不等待超时）→ 定时探测恢复 → 恢复后重新放行。
//
// # 状态转换
//
//	Closed  ──(失败率超过阈值)──→ Open
//	Open    ──(冷却时间到期)───→ HalfOpen
//	HalfOpen ──(探测请求成功)───→ Closed
//	HalfOpen ──(探测请求失败)───→ Open
//
// # 滑动窗口 vs 固定窗口
//
// 固定窗口（如"过去 10 秒"）有边界效应：窗口切换瞬间错误率会突变。
// 滑动窗口将时间等分为多个桶（ring buffer），最旧的桶过期时平滑移出，
// 错误率变化连续、无跳变。
//
// # 并发安全
//
// 所有状态读写受 sync.Mutex 保护。Call 方法是线程安全的——
// 多个 goroutine 可以同时调用，状态转换正确序列化。
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen 是熔断器打开时返回的错误。
// 调用方可以特殊处理此错误（如触发告警、记录 metrics），
// 区别于普通的上游业务错误。
var ErrCircuitOpen = errors.New("circuit breaker is open")

// ────────────────────────────────────────────────────────────
// 状态定义
// ────────────────────────────────────────────────────────────

// State 表示熔断器的当前状态。
type State int

const (
	// StateClosed 是正常状态。请求正常通过，失败数被记录到滑动窗口。
	StateClosed State = iota

	// StateOpen 是熔断状态。所有请求立即被拒绝（返回 ErrCircuitOpen），
	// 不尝试调用下游。这是快速失败策略的核心。
	StateOpen

	// StateHalfOpen 是探测状态。允许有限数量的请求通过——
	// 如果成功 → 恢复到 Closed；如果失败 → 重新进入 Open。
	// 这种"有限试探"避免了全量流量冲击刚恢复的服务。
	StateHalfOpen
)

// String 返回状态的可读名称，用于日志和 metrics。
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

// ────────────────────────────────────────────────────────────
// 滑动窗口
// ────────────────────────────────────────────────────────────

// bucket 是滑动窗口中的一个时间桶，统计该时间片内的成功/失败数。
type bucket struct {
	failures  int64
	successes int64
}

// ────────────────────────────────────────────────────────────
// 熔断器
// ────────────────────────────────────────────────────────────

// CircuitBreaker 保护一个下游服务免受级联故障。
//
// 核心机制：
//   - 在 Closed 状态持续统计失败率
//   - 失败率超过阈值 → 立即打开，之后的所有请求直接拒绝
//   - 冷却时间到期 → 进入半开状态，发送有限探测请求
//   - 探测成功 → 关闭（恢复正常）；探测失败 → 重新打开（继续熔断）
//
// 零值不可用，必须通过 New 函数创建。
type CircuitBreaker struct {
	mu sync.Mutex

	state State // 当前状态

	// ── 配置参数 ──
	failureThreshold float64       // 失败率阈值（0.0-1.0）。0.5 = 50% 的请求失败就熔断
	windowDuration   time.Duration // 滑动窗口总长度
	bucketDuration   time.Duration // 每个桶的时间粒度 (= windowDuration / bucketCount)
	openTimeout      time.Duration // Open 状态的最长持续时间
	halfOpenMaxReqs  int           // HalfOpen 状态下最多允许几个探测请求

	// ── 滑动窗口（环形缓冲区）──
	buckets     []bucket  // 时间桶数组
	bucketCount int       // 桶数量
	windowStart time.Time // 窗口起始时间

	// ── 运行状态 ──
	lastFailureTime time.Time // 最近一次失败的时间（用于计算 Open 持续时长）
	halfOpenReqs    int       // 当前半开状态已发送的探测请求数
	totalFailures   int64     // 累计失败次数（自创建以来，不清零）
	totalSuccesses  int64     // 累计成功次数（自创建以来，不清零）
}

// ────────────────────────────────────────────────────────────
// 配置
// ────────────────────────────────────────────────────────────

// Config 是创建熔断器的参数集合，零值字段使用 DefaultConfig 的默认值。
type Config struct {
	// FailureThreshold 是触发熔断的失败率阈值。
	// 0.5 = 窗口内 50% 的请求失败时熔断。默认 0.5。
	FailureThreshold float64

	// WindowDuration 是失败率的观察窗口长度。默认 10 秒。
	WindowDuration time.Duration

	// BucketCount 是滑动窗口中的时间桶数量。
	// 越大 → 失败率变化越平滑，但内存占用略高。默认 10。
	BucketCount int

	// OpenTimeout 是熔断器保持 Open 状态的最长时间。
	// 超时后自动进入 HalfOpen。默认 30 秒。
	OpenTimeout time.Duration

	// HalfOpenMaxReqs 是半开状态最多允许的探测请求数。
	// 默认 1（仅一个请求用于探测）。
	HalfOpenMaxReqs int
}

// DefaultConfig 返回生产环境可用的默认配置。
//
//	FailureThreshold: 50%
//	WindowDuration:   10 秒
//	BucketCount:      10 个（每桶 1 秒）
//	OpenTimeout:      30 秒
//	HalfOpenMaxReqs:  1 个
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 0.5,
		WindowDuration:   10 * time.Second,
		BucketCount:      10,
		OpenTimeout:      30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
}

// New 根据配置创建熔断器，对非法值进行防御性校验并设为默认值。
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

// ────────────────────────────────────────────────────────────
// 公共方法
// ────────────────────────────────────────────────────────────

// Call 在熔断器的保护下执行 fn。
//
// 返回值：
//   - 熔断器打开时：返回 ErrCircuitOpen（fn 不会被调用）
//   - 熔断器关闭/半开时：返回 fn 的结果（nil 或 error）
//
// fn 返回 error 被视为"失败"并计入滑动窗口；
// fn 返回 nil 被视为"成功"并计入滑动窗口。
//
// Call 是线程安全的——多个 goroutine 可并发调用。
func (cb *CircuitBreaker) Call(fn func() error) error {
	if err := cb.allowRequest(); err != nil {
		return err
	}

	err := fn()
	cb.recordResult(err)
	return err
}

// State 返回当前的熔断器状态（线程安全）。
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Stats 返回当前的统计快照（线程安全），供监控/metics 使用。
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	var failures, successes int64
	for _, b := range cb.buckets {
		failures += b.failures
		successes += b.successes
	}

	return Stats{
		State:           cb.state,
		TotalFailures:   cb.totalFailures,
		TotalSuccesses:  cb.totalSuccesses,
		WindowFailures:  failures,
		WindowSuccesses: successes,
	}
}

// Stats 是熔断器的统计快照。
type Stats struct {
	State           State // 当前状态
	TotalFailures   int64 // 累计失败次数（创建以来）
	TotalSuccesses  int64 // 累计成功次数（创建以来）
	WindowFailures  int64 // 当前窗口内的失败次数
	WindowSuccesses int64 // 当前窗口内的成功次数
}

// ────────────────────────────────────────────────────────────
// 内部方法（调用时必须持有 mu）
// ────────────────────────────────────────────────────────────

// allowRequest 根据当前状态判断是否允许本次请求通过。
// 同时处理状态转换（Closed→Open, Open→HalfOpen）。
func (cb *CircuitBreaker) allowRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// 推进滑动窗口，清除过期桶。
	cb.slideWindow()

	switch cb.state {
	case StateClosed:
		// Closed 状态下需要检查是否应该熔断。
		// 注意：shouldTrip 在本次请求执行之前判断——
		// 这意味着触发熔断的那次请求本身也会被拒绝。
		if cb.shouldTrip() {
			cb.transitionTo(StateOpen)
			return ErrCircuitOpen
		}
		return nil

	case StateOpen:
		// Open 状态下检查冷却时间是否到期。
		// 到期 → 进入 HalfOpen，允许本次探测请求通过。
		if time.Since(cb.lastFailureTime) >= cb.openTimeout {
			cb.transitionTo(StateHalfOpen)
			cb.halfOpenReqs++
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// HalfOpen 状态限制探测请求数。
		// 超过限制的请求直接拒绝，不做额外排队。
		if cb.halfOpenReqs >= cb.halfOpenMaxReqs {
			return ErrCircuitOpen
		}
		cb.halfOpenReqs++
		return nil

	default:
		return ErrCircuitOpen
	}
}

// recordResult 记录请求结果，处理半开状态下的状态转换。
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.recordFailure()
	} else {
		cb.recordSuccess()
	}

	// HalfOpen 状态下的转换逻辑：
	//   - 探测失败 → 立即重新打开（说明服务还未恢复）
	//   - 全部探测成功且达到 halfOpenMaxReqs → 关闭（说明服务已恢复）
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

// shouldTrip 判断当前窗口内的失败率是否超过阈值。
// 调用前必须持有 mu。
func (cb *CircuitBreaker) shouldTrip() bool {
	var totalFailures, totalSuccesses int64
	for _, b := range cb.buckets {
		totalFailures += b.failures
		totalSuccesses += b.successes
	}

	total := totalFailures + totalSuccesses
	if total == 0 {
		return false // 没有数据，不触发熔断
	}

	return float64(totalFailures)/float64(total) >= cb.failureThreshold
}

// currentBucket 返回当前时间对应的桶索引。
func (cb *CircuitBreaker) currentBucket() int {
	elapsed := time.Since(cb.windowStart)
	bucketIdx := int(elapsed / cb.bucketDuration)
	if bucketIdx >= cb.bucketCount {
		bucketIdx = cb.bucketCount - 1 // 边界保护
	}
	return bucketIdx
}

// slideWindow 推进滑动窗口，清除过期桶的计数。
// 调用前必须持有 mu。
//
// 例如：windowDuration=10s, bucketCount=10, bucketDuration=1s。
// 如果距离上次推进过了 2.3 秒 → bucketsToSlide=2 → 清除 2 个最旧的桶。
func (cb *CircuitBreaker) slideWindow() {
	elapsed := time.Since(cb.windowStart)
	bucketsToSlide := int(elapsed / cb.bucketDuration)

	if bucketsToSlide <= 0 {
		return
	}

	// 清除过期桶（逐个清零，避免一次性清空所有）。
	for i := 0; i < bucketsToSlide && i < cb.bucketCount; i++ {
		idx := (cb.currentBucket() + 1 + i) % cb.bucketCount
		cb.buckets[idx] = bucket{}
	}

	cb.windowStart = cb.windowStart.Add(time.Duration(bucketsToSlide) * cb.bucketDuration)
}

// transitionTo 执行状态转换，重置相关计数器。
// 调用前必须持有 mu。
func (cb *CircuitBreaker) transitionTo(newState State) {
	cb.state = newState
	cb.halfOpenReqs = 0

	if newState == StateOpen {
		cb.lastFailureTime = time.Now()
	}
}
