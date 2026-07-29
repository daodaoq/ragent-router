// RedisStore 是基于 Redis 的分布式限流存储。
//
// 与 ShardedStore（单机内存）不同，RedisStore 实现跨实例的限流状态共享：
//   - 多个服务实例共享同一份限流状态
//   - 使用 Redis Lua 脚本保证原子性
//   - 支持滑动窗口限流（精确）和令牌桶限流（突发友好）
//
// 面试考点：
//   - 为什么用 Lua 脚本？Redis 单线程 + Lua 原子执行 = 无竞态条件
//   - 滑动窗口 vs 令牌桶：滑动窗口更精确，令牌桶允许短时突发
//   - 为什么不用 Redis INCR？INCR 是固定窗口，窗口边界有 2x 突发问题
package ratelimit

import (
	"context"
	"fmt"
	"time"

	goRedis "github.com/redis/go-redis/v9"
)

// RedisStore 是 Store 接口的 Redis 实现。
//
// 注意：RedisStore 不存储 TokenBucket 对象（那需要序列化整个状态），
// 而是直接在 Redis 中用 Lua 脚本实现分布式令牌桶。
// Load/Store 方法用于管理限流配置（rate/capacity），而非运行时状态。
type RedisStore struct {
	client goRedis.UniversalClient
	prefix string // key 前缀，避免业务冲突
}

// NewRedisStore 创建 Redis 限流存储。
//
// 参数：
//   - client: Redis 客户端（支持单机/哨兵/集群）
//   - prefix: key 前缀（如 "ratelimit:"）
func NewRedisStore(client goRedis.UniversalClient, prefix string) *RedisStore {
	if prefix == "" {
		prefix = "ratelimit:"
	}
	return &RedisStore{
		client: client,
		prefix: prefix,
	}
}

// ────────────────────────────────────────────────────────────
// Store 接口实现（配置存储）
// ────────────────────────────────────────────────────────────

// Load 从 Redis 加载限流配置，不存在则通过 builder 创建并存储。
//
// 存储格式：JSON 序列化的 RateLimitConfig。
// TTL：24 小时（避免配置残留）。
func (s *RedisStore) Load(key string, builder func() interface{}) interface{} {
	ctx := context.Background()
	rdsKey := s.prefix + "config:" + key

	// 尝试从 Redis 获取
	val, err := s.client.Get(ctx, rdsKey).Bytes()
	if err == nil && len(val) > 0 {
		return string(val) // 返回序列化的配置
	}

	// 不存在，调用 builder 创建
	newVal := builder()
	if newVal == nil {
		return nil
	}

	// 存储到 Redis
	if data, ok := newVal.(string); ok {
		s.client.Set(ctx, rdsKey, data, 24*time.Hour)
	}
	return newVal
}

// Store 保存限流配置到 Redis。
func (s *RedisStore) Store(key string, value interface{}) error {
	ctx := context.Background()
	rdsKey := s.prefix + "config:" + key

	data := fmt.Sprintf("%v", value)
	return s.client.Set(ctx, rdsKey, data, 24*time.Hour).Err()
}

// ────────────────────────────────────────────────────────────
// 分布式令牌桶（Lua 脚本，原子操作）
// ────────────────────────────────────────────────────────────

// redisTokenBucketScript Redis Lua 脚本实现分布式令牌桶。
//
// KEYS[1] = 令牌桶 key
// ARGV[1] = rate（每秒生成 Token 数）
// ARGV[2] = capacity（桶容量）
// ARGV[3] = now（当前时间戳，微秒）
// ARGV[4] = requested（请求的 Token 数，默认 1）
//
// 返回值：{allowed(0/1), remaining_tokens, wait_time_us}
//
// 数据结构（Hash）：
//   - tokens: 当前可用 Token 数
//   - last_refill: 上次补充时间（微秒）
//
// Lua 脚本的优势：
//   - Redis 单线程保证原子性，无需分布式锁
//   - 一次网络往返完成判断 + 扣减
//   - 避免 check-then-act 竞态条件
var redisTokenBucketScript = goRedis.NewScript(`
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

-- 获取当前状态
local tokens = tonumber(redis.call('HGET', key, 'tokens') or capacity)
local last_refill = tonumber(redis.call('HGET', key, 'last_refill') or now)

-- 计算应补充的 Token 数
local elapsed = now - last_refill
local fill_interval = 1000000 / rate  -- 微秒/Token
local tokens_to_add = math.floor(elapsed / fill_interval)

if tokens_to_add > 0 then
    tokens = math.min(tokens + tokens_to_add, capacity)
    last_refill = last_refill + tokens_to_add * fill_interval
end

-- 判断是否放行
local allowed = 0
local wait_time = 0

if tokens >= requested then
    tokens = tokens - requested
    allowed = 1
else
    -- 计算需要等待的时间（微秒）
    wait_time = math.ceil((requested - tokens) * fill_interval)
end

-- 更新状态
redis.call('HSET', key, 'tokens', tokens, 'last_refill', last_refill)
redis.call('EXPIRE', key, math.ceil(capacity / rate) + 10) -- TTL = 桶排空时间 + 缓冲

return {allowed, tokens, wait_time}
`)

// AllowDistributed 分布式令牌桶限流判断。
//
// 与 ShardedStore 不同，此方法的限流状态存储在 Redis 中，
// 所有服务实例共享同一份限流状态。
//
// 参数：
//   - key: 限流维度（如 "provider:openai"）
//   - rate: 每秒允许的请求数
//   - capacity: 突发容量
//
// 返回值：
//   - allowed: 是否放行
//   - remaining: 剩余 Token 数
//   - retryAfter: 建议重试等待时间（被拒绝时 > 0）
func (s *RedisStore) AllowDistributed(ctx context.Context, key string, rate float64, capacity uint64) (allowed bool, remaining uint64, retryAfter time.Duration) {
	rdsKey := s.prefix + "bucket:" + key
	now := time.Now().UnixMicro()

	result, err := redisTokenBucketScript.Run(ctx, s.client,
		[]string{rdsKey},
		rate, capacity, now, 1,
	).Int64Slice()
	if err != nil {
		// Redis 不可用时降级放行（宁可放过，不可误杀）
		return true, 0, 0
	}

	allowed = result[0] == 1
	remaining = uint64(result[1])
	retryAfter = time.Duration(result[2]) * time.Microsecond

	return
}

// ────────────────────────────────────────────────────────────
// 滑动窗口限流（精确计数）
// ────────────────────────────────────────────────────────────

// slidingWindowScript Redis Lua 脚本实现滑动窗口限流。
//
// 使用 Sorted Set，score = 时间戳，member = 唯一请求 ID。
// 窗口内的 member 数量 = 当前 QPS。
//
// 优势：无固定窗口边界问题（固定窗口在边界处可能有 2x 突发）。
var slidingWindowScript = goRedis.NewScript(`
local key = KEYS[1]
local window = tonumber(ARGV[1])  -- 窗口大小（微秒）
local limit = tonumber(ARGV[2])   -- 窗口内最大请求数
local now = tonumber(ARGV[3])
local member = ARGV[4]

-- 清理过期成员
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)

-- 统计窗口内请求数
local count = redis.call('ZCARD', key)

if count < limit then
    -- 放行：添加当前请求
    redis.call('ZADD', key, now, member)
    redis.call('EXPIRE', key, math.ceil(window / 1000000) + 1)
    return {1, limit - count - 1}
else
    -- 拒绝：获取最早的 member 计算重试时间
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local retry_after = 0
    if #oldest >= 2 then
        retry_after = tonumber(oldest[2]) + window - now
    end
    return {0, 0, retry_after}
end
`)

// AllowSlidingWindow 滑动窗口限流。
//
// 适用场景：需要精确 QPS 控制（API 网关每秒 N 次）。
// 精度：微秒级，无固定窗口边界问题。
func (s *RedisStore) AllowSlidingWindow(ctx context.Context, key string, limit int64, window time.Duration) (allowed bool, remaining int64, retryAfter time.Duration) {
	rdsKey := s.prefix + "window:" + key
	now := time.Now().UnixMicro()
	member := fmt.Sprintf("%d:%d", now, time.Now().UnixNano()) // 唯一标识

	result, err := slidingWindowScript.Run(ctx, s.client,
		[]string{rdsKey},
		window.Microseconds(), limit, now, member,
	).Int64Slice()
	if err != nil {
		// Redis 不可用时降级放行
		return true, 0, 0
	}

	allowed = result[0] == 1
	remaining = result[1]
	if len(result) > 2 && result[2] > 0 {
		retryAfter = time.Duration(result[2]) * time.Microsecond
	}

	return
}

// Close 关闭 Redis 连接。
func (s *RedisStore) Close() error {
	return s.client.Close()
}
