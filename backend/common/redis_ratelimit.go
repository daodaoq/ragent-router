package common

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

// ────────────────────────────────────────────────────────────
// Redis Lua 原子限流器
//
// 面试考点：
//  1. 为什么用 Lua 脚本？（原子性，多条 Redis 命令在服务端一次执行，避免竞态）
//  2. 令牌桶 vs 漏桶 vs 滑动窗口？（令牌桶允许突发，漏桶平滑输出，滑动窗口精确）
//  3. 如何处理 Redis 不可用？（降级到本地限流，宁可放行不可拒绝）
//  4. 集群限流怎么做？（Redis 集群 + Lua，或中心化限流服务）
// ────────────────────────────────────────────────────────────

// RedisRateLimiter 基于 Redis 的滑动窗口限流器。
//
// 实现原理（滑动窗口 + Lua 原子操作）：
//   - 使用 Redis Sorted Set，score = 请求时间戳
//   - 每次请求：删除过期成员 → 统计窗口内请求数 → 判断是否超限 → 添加新成员
//   - 全部在一个 Lua 脚本中执行，保证原子性
//
// 对比其他限流算法：
//   - 固定窗口：有边界突发问题（窗口交界处可能 2x 流量）
//   - 滑动窗口：精确，但内存开销较大（每请求一条记录）
//   - 令牌桶：允许突发，但实现复杂
//   - 本实现：滑动窗口，适合 API 网关场景
type RedisRateLimiter struct {
	client  *redis.Client
	key     string
	window  time.Duration // 时间窗口
	maxRate int           // 窗口内最大请求数
}

// NewRedisRateLimiter 创建 Redis 限流器。
//
// 参数：
//   - client: Redis 客户端
//   - key: 限流 key（建议按维度命名，如 "ratelimit:user:123" 或 "ratelimit:ip:1.2.3.4"）
//   - window: 时间窗口（如 1s、1min）
//   - maxRate: 窗口内最大请求数
func NewRedisRateLimiter(client *redis.Client, key string, window time.Duration, maxRate int) *RedisRateLimiter {
	return &RedisRateLimiter{
		client:  client,
		key:     "ratelimit:" + key,
		window:  window,
		maxRate: maxRate,
	}
}

// slidingWindowScript 滑动窗口限流 Lua 脚本。
//
// KEYS[1] = 限流 key（Sorted Set）
// ARGV[1] = 当前时间戳（微秒）
// ARGV[2] = 窗口大小（微秒）
// ARGV[3] = 最大请求数
//
// 返回值：[allowed(1/0), current_count]
//
// 执行流程（原子）：
//  1. ZREMRANGEBYSCORE: 删除窗口外的过期成员
//  2. ZCARD: 统计窗口内当前请求数
//  3. 判断是否超限
//  4. 未超限则 ZADD 添加当前请求
//  5. EXPIRE 设置 key 过期时间（自动清理）
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local max_rate = tonumber(ARGV[3])

-- 删除窗口外的过期成员
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- 统计窗口内请求数
local current = redis.call('ZCARD', key)

if current < max_rate then
    -- 未超限，添加当前请求
    redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
    redis.call('EXPIRE', key, math.ceil(window / 1000000))
    return {1, current + 1}
else
    -- 已超限
    redis.call('EXPIRE', key, math.ceil(window / 1000000))
    return {0, current}
end
`)

// Allow 检查请求是否允许通过。
//
// 返回值：
//   - allowed: 是否允许
//   - current: 窗口内当前请求数
//   - err: Redis 错误（Redis 不可用时默认放行）
func (l *RedisRateLimiter) Allow(ctx context.Context) (allowed bool, current int, err error) {
	if RedisClient == nil {
		// Redis 不可用，降级放行（宁可放行不可拒绝，保证可用性）
		return true, 0, nil
	}

	now := time.Now().UnixMicro()
	window := l.window.Microseconds()

	result, err := slidingWindowScript.Run(ctx, l.client, []string{l.key},
		now, window, l.maxRate).Int64Slice()
	if err != nil {
		// Redis 错误，降级放行
		return true, 0, nil
	}

	allowed = result[0] == 1
	current = int(result[1])
	return allowed, current, nil
}

// ────────────────────────────────────────────────────────────
// 令牌桶限流（Redis 版本，展示多种限流算法）
// ────────────────────────────────────────────────────────────

// tokenBucketScript 令牌桶 Lua 脚本。
//
// KEYS[1] = 令牌桶 key（Hash）
// ARGV[1] = 桶容量
// ARGV[2] = 每秒补充速率
// ARGV[3] = 当前时间戳（秒）
//
// 返回值：[allowed(1/0), remaining_tokens]
//
// 实现原理：
//   - 使用 Redis Hash 存储 {tokens: 剩余令牌数, last_refill: 上次补充时间}
//   - 每次请求：计算应补充的令牌数 → 判断是否有令牌 → 消耗令牌
//   - 全部原子执行
var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- 获取当前状态
local tokens = tonumber(redis.call('HGET', key, 'tokens') or capacity)
local last_refill = tonumber(redis.call('HGET', key, 'last_refill') or now)

-- 计算应补充的令牌数
local elapsed = now - last_refill
local refill = elapsed * rate
tokens = math.min(capacity, tokens + refill)

-- 判断是否有令牌
if tokens >= 1 then
    -- 消耗一个令牌
    tokens = tokens - 1
    redis.call('HSET', key, 'tokens', tokens, 'last_refill', now)
    redis.call('EXPIRE', key, math.ceil(capacity / rate) * 2)
    return {1, tokens}
else
    -- 无令牌
    redis.call('HSET', key, 'tokens', tokens, 'last_refill', now)
    redis.call('EXPIRE', key, math.ceil(capacity / rate) * 2)
    return {0, tokens}
end
`)

// RedisTokenBucket 基于 Redis 的令牌桶限流器。
type RedisTokenBucket struct {
	client   *redis.Client
	key      string
	capacity int     // 桶容量
	rate     float64 // 每秒补充速率
}

// NewRedisTokenBucket 创建 Redis 令牌桶。
func NewRedisTokenBucket(client *redis.Client, key string, capacity int, rate float64) *RedisTokenBucket {
	return &RedisTokenBucket{
		client:   client,
		key:      "bucket:" + key,
		capacity: capacity,
		rate:     rate,
	}
}

// Allow 检查请求是否允许通过。
func (b *RedisTokenBucket) Allow(ctx context.Context) (bool, int, error) {
	if RedisClient == nil {
		return true, 0, nil
	}

	now := time.Now().Unix()
	result, err := tokenBucketScript.Run(ctx, b.client, []string{b.key},
		b.capacity, b.rate, now).Int64Slice()
	if err != nil {
		return true, 0, nil
	}

	return result[0] == 1, int(result[1]), nil
}

// ────────────────────────────────────────────────────────────
// 多维度限流管理器
// ────────────────────────────────────────────────────────────

// MultiDimensionRateLimiter 多维度限流管理器。
//
// 支持按不同维度限流：
//   - 全局：所有请求共享
//   - 用户：每个用户独立限流
//   - IP：每个 IP 独立限流
//   - 模型：每个模型独立限流
type MultiDimensionRateLimiter struct {
	client       *redis.Client
	globalLimit  *RedisRateLimiter
	userWindow   time.Duration
	userMaxRate  int
	ipWindow     time.Duration
	ipMaxRate    int
	modelWindow  time.Duration
	modelMaxRate int
}

// NewMultiDimensionRateLimiter 创建多维度限流器。
func NewMultiDimensionRateLimiter(client *redis.Client) *MultiDimensionRateLimiter {
	return &MultiDimensionRateLimiter{
		client:       client,
		globalLimit:  NewRedisRateLimiter(client, "global", time.Second, 100),
		userWindow:   time.Minute,
		userMaxRate:  60,
		ipWindow:     time.Minute,
		ipMaxRate:    120,
		modelWindow:  time.Minute,
		modelMaxRate: 30,
	}
}

// AllowGlobal 全局限流。
func (m *MultiDimensionRateLimiter) AllowGlobal(ctx context.Context) (bool, int, error) {
	return m.globalLimit.Allow(ctx)
}

// AllowUser 用户级限流。
func (m *MultiDimensionRateLimiter) AllowUser(ctx context.Context, userId int) (bool, int, error) {
	limiter := NewRedisRateLimiter(m.client, "user:"+itoa(userId), m.userWindow, m.userMaxRate)
	return limiter.Allow(ctx)
}

// AllowIP IP 级限流。
func (m *MultiDimensionRateLimiter) AllowIP(ctx context.Context, ip string) (bool, int, error) {
	limiter := NewRedisRateLimiter(m.client, "ip:"+ip, m.ipWindow, m.ipMaxRate)
	return limiter.Allow(ctx)
}

// AllowModel 模型级限流。
func (m *MultiDimensionRateLimiter) AllowModel(ctx context.Context, model string) (bool, int, error) {
	limiter := NewRedisRateLimiter(m.client, "model:"+model, m.modelWindow, m.modelMaxRate)
	return limiter.Allow(ctx)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
