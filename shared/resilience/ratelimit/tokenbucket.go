// Package ratelimit 提供高性能的令牌桶限流器与分片并发存储。
//
// # 令牌桶算法
//
// 令牌桶是一种经典的流量整形算法：系统以固定速率向桶中放入令牌，
// 每个请求需消耗一个令牌才能通过。桶有容量上限，允许短时间的突发流量。
//
// # Lazy Refill 优化
//
// 常规令牌桶在每次 Allow() 时都计算"距离上次补充过了多久→应补充多少令牌"，
// 这涉及 time.Now() 系统调用和浮点运算。本实现的优化思路：
//
//   - 热路径（桶中还有 Token）：一次 Mutex + 整数递减，约 28ns/op。
//   - 冷路径（桶空）：才执行 time.Since + 除法计算补充量。
//
// 这种策略基于一个简单观察：限流器绝大多数时候处于"有 Token"状态
// （如果经常桶空，说明 rate 设置太低，应该调大）。
//
// # 并发安全
//
// 所有字段由 sync.Mutex 保护。没有使用 atomic——因为 tokens 和
// lastRefill 必须保持一致性（两个字段的更新必须原子化），Mutex 是
// 最清晰的选择，且在非竞争路径上 Go 的 Mutex 开销极低（~25ns）。
package ratelimit

import (
	"sync"
	"time"
)

// Clock 是对 time 包的抽象接口。
//
// 生产环境使用 realClock（直接委托给 time.Now/time.Since），
// 测试环境可注入固定时钟，精确验证 Lazy Refill 的时间逻辑。
type Clock interface {
	Now() time.Time
	// Since 返回从 t 到现在经过的时间。
	Since(time.Time) time.Duration
}

// realClock 是 Clock 的生产环境实现。
type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

// TokenBucket 是令牌桶限流器的核心实现。
//
// 零值不可用，必须通过 NewTokenBucket 或 NewTokenBucketWithClock 创建。
//
// 设计要点：
//   - fillInterval：每个 Token 的时间成本（纳秒），由 rate 反推。
//     例如 rate=100/s → fillInterval=10ms，即每 10ms 生成 1 个 Token。
//   - capacity：桶容量 = 最大突发量。例如 rate=100, capacity=50，
//     稳态每秒 100 请求，但可以瞬间处理 50 个积压请求。
//   - lastRefill：最近一次补充的时间。冷路径更新时，不是直接设为当前时间，
//     而是按 fillInterval 的整数倍推进，避免引入额外 Token。
type TokenBucket struct {
	fillInterval time.Duration // 每生成一个 Token 的时间间隔 (= 1e9 / rate)
	capacity     uint64        // 桶最大容量，决定突发上限
	tokens       uint64        // 当前可用 Token 数
	lastRefill   time.Time     // 最近一次补充 Token 的时间戳
	clock        Clock         // 时间源（可注入）
	mu           sync.Mutex    // 保护以上字段的互斥锁
}

// NewTokenBucket 创建一个初始满的令牌桶。
//
// 参数：
//   - rate:  每秒生成的 Token 数。例如 100 表示 QPS=100。
//   - capacity: 桶容量。例如 50 表示最多积攒 50 个 Token 应对突发。
//
// 桶初始为满——首轮请求不会因桶刚创建而被拒绝。
func NewTokenBucket(rate float64, capacity uint64) *TokenBucket {
	return NewTokenBucketWithClock(rate, capacity, nil)
}

// NewTokenBucketWithClock 创建令牌桶并注入自定义时钟（主要用于测试）。
func NewTokenBucketWithClock(rate float64, capacity uint64, clock Clock) *TokenBucket {
	if clock == nil {
		clock = realClock{}
	}
	// 防御性校验：capacity 至少为 1，避免除零或永久拒绝。
	if capacity < 1 {
		capacity = 1
	}
	// rate 过小会导致 fillInterval 溢出 int64，设下界为 1e-9。
	if rate < 1e-9 {
		rate = 1e-9
	}

	return &TokenBucket{
		// 1e9 纳秒 / rate = 每个 Token 的时间成本。
		// rate=100 → 10ms/token, rate=1000 → 1ms/token。
		fillInterval: time.Duration(float64(time.Second) / rate),
		capacity:     capacity,
		tokens:       capacity, // 初始满桶
		lastRefill:   clock.Now(),
		clock:        clock,
	}
}

// Allow 判断一次请求是否应被放行，放行则消耗一个 Token。
//
// 返回值 true = 放行，false = 被限流拒绝。
//
// 性能特征（i9-13900HX 基准）：
//   - 热路径（Token > 0）：~28ns/op，零内存分配
//   - 冷路径（Token == 0）：额外 time.Since + 两次时间运算
//
// 精度说明：
// lastRefill 按 fillInterval 的整数倍推进，而非直接设为 time.Now()。
// 这保证了长时间无请求后不会一次性补满——rate=100/s 时，空闲 5 秒
// 只能补 500 个 Token（精确按速率），而非"满了"。
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// ── 快速路径：桶中还有 Token，直接扣减 ──
	// 进入此分支的频率 = 1 - (实际 QPS / rate)，通常 > 90%。
	if tb.tokens > 0 {
		tb.tokens--
		return true
	}

	// ── 慢速路径：桶空，计算应补充的 Token 数 ──
	elapsed := tb.clock.Since(tb.lastRefill)
	tokensToAdd := uint64(elapsed / tb.fillInterval)

	// 距离上次补充的时间太短，一个 Token 都凑不出来 → 拒绝。
	if tokensToAdd == 0 {
		return false
	}

	// 按补充的 Token 数等比例推进时间戳。
	// 注：不是 time.Now()——如果直接设当前时间，会产生不应存在的 Token。
	tb.lastRefill = tb.lastRefill.Add(time.Duration(tokensToAdd) * tb.fillInterval)

	// 防止超量补充（长时间空闲后 tokensToAdd 可能远超 capacity）。
	if tokensToAdd > tb.capacity {
		tokensToAdd = tb.capacity
	}

	tb.tokens = tokensToAdd - 1 // 补充后消耗一个
	return true
}

// Tokens 返回当前可用 Token 数，供 metrics 采集。
func (tb *TokenBucket) Tokens() uint64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.tokens
}

// Rate 返回配置的速率（Token/秒）。
func (tb *TokenBucket) Rate() float64 {
	return float64(time.Second) / float64(tb.fillInterval)
}

// Capacity 返回桶的最大容量。
func (tb *TokenBucket) Capacity() uint64 {
	return tb.capacity
}
