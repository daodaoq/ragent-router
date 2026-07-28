package redis

import (
	"context"
	"log"
	"os"
	"time"

	goRedis "github.com/redis/go-redis/v9"
)

var Client *goRedis.Client

func Init() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	Client = goRedis.NewClient(&goRedis.Options{
		Addr:         addr,
		Password:     os.Getenv("REDIS_PASSWORD"),
		DB:           0,
		PoolSize:     100,
		MinIdleConns: 10,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Client.Ping(ctx).Err(); err != nil {
		log.Printf("[Redis] 连接失败: %v", err)
		Client = nil
	}
}

// ────────────────────────────────────────────────────────────
// 分布式令牌桶限流（Lua 脚本原子操作）
// ────────────────────────────────────────────────────────────

const rateLimitLua = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])
local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(bucket[1]) or capacity
local last_refill = tonumber(bucket[2]) or now
local elapsed = math.max(0, now - last_refill)
local refill = elapsed * rate
tokens = math.min(capacity, tokens + refill)
local allowed = 0
if tokens >= requested then
    tokens = tokens - requested
    allowed = 1
end
redis.call('HMSET', key, 'tokens', tokens, 'last_refill', now)
redis.call('EXPIRE', key, math.ceil(capacity / rate) + 1)
return allowed
`

func Allow(ctx context.Context, key string, capacity int, rate float64) bool {
	if Client == nil {
		return true
	}
	result, err := Client.Eval(ctx, rateLimitLua, []string{key},
		capacity, rate, float64(time.Now().UnixMilli())/1000.0, 1,
	).Int()
	if err != nil {
		return true
	}
	return result == 1
}
