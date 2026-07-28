// Package redis 提供 Redis 基础设施和分布式限流。
//
// 高并发方案 1：基于 Redis + Lua 脚本的分布式令牌桶限流。
// 面试考点：Lua 脚本原子性、分布式限流 vs 本地限流、令牌桶 vs 漏桶。
package redis

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// Client 全局 Redis 客户端。
	Client *redis.Client
)

// Init 初始化 Redis 连接。
func Init() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	password := os.Getenv("REDIS_PASSWORD")

	Client = redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           0,
		PoolSize:     100,              // 连接池大小
		MinIdleConns: 10,               // 最小空闲连接
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := Client.Ping(ctx).Err(); err != nil {
		log.Printf("[Redis] 连接失败: %v (降级为本地模式)", err)
		Client = nil
		return
	}
	log.Println("[Redis] 连接成功")
}

// ────────────────────────────────────────────────────────────
// 分布式令牌桶限流（Lua 脚本原子操作）
// ────────────────────────────────────────────────────────────

// rateLimitLua 是分布式令牌桶的 Lua 脚本。
//
// KEYS[1] = 限流键
// ARGV[1] = 桶容量
// ARGV[2] = 每秒补充速率
// ARGV[3] = 当前时间戳（秒，浮点数）
// ARGV[4] = 请求的令牌数（通常为 1）
//
// 返回值：1 = 允许，0 = 拒绝
//
// 原子性保证：Redis 单线程执行 Lua 脚本，不会出现竞态条件。
// 这是分布式限流的核心——多个 API 实例共享同一个 Redis 键，
// 通过 Lua 脚本保证"检查余量 → 扣减令牌"的原子性。
const rateLimitLua = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

-- 获取当前令牌桶状态
local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(bucket[1]) or capacity
local last_refill = tonumber(bucket[2]) or now

-- 计算应补充的令牌数
local elapsed = math.max(0, now - last_refill)
local refill = elapsed * rate
tokens = math.min(capacity, tokens + refill)

-- 尝试扣减
local allowed = 0
if tokens >= requested then
    tokens = tokens - requested
    allowed = 1
end

-- 更新状态
redis.call('HMSET', key, 'tokens', tokens, 'last_refill', now)
redis.call('EXPIRE', key, math.ceil(capacity / rate) + 1)

return allowed
`

// Allow 分布式限流判断。
//
// key: 限流键（如 "ratelimit:global", "ratelimit:provider:DeepSeek"）
// capacity: 桶容量（允许的突发请求数）
// rate: 每秒补充速率
//
// 返回 true 表示允许，false 表示被限流。
func Allow(ctx context.Context, key string, capacity int, rate float64) bool {
	if Client == nil {
		return true // Redis 不可用时降级为不限流
	}

	result, err := Client.Eval(ctx, rateLimitLua, []string{key},
		capacity, rate, float64(time.Now().UnixMilli())/1000.0, 1,
	).Int()

	if err != nil {
		log.Printf("[限流] Redis Eval 错误: %v (降级放行)", err)
		return true
	}
	return result == 1
}

// AllowN 分布式限流判断（消耗 N 个令牌）。
func AllowN(ctx context.Context, key string, capacity int, rate float64, n int) bool {
	if Client == nil {
		return true
	}

	result, err := Client.Eval(ctx, rateLimitLua, []string{key},
		capacity, rate, float64(time.Now().UnixMilli())/1000.0, n,
	).Int()

	if err != nil {
		return true
	}
	return result == 1
}
