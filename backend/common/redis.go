package common

import (
	"context"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
)

// RedisClient 全局 Redis 客户端（可选，未配置时为 nil）。
var RedisClient *redis.Client

// InitRedisClient 初始化 Redis 连接。
// 如果 REDIS_CONN_STRING 为空，Redis 未启用（降级到内存缓存）。
func InitRedisClient() {
	redisURL := GetEnv("REDIS_CONN_STRING", "")
	if redisURL == "" {
		log.Println("[Redis] 未配置 REDIS_CONN_STRING，Redis 未启用（降级到内存缓存）")
		return
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("[Redis] URL 解析失败: %v，Redis 未启用", err)
		return
	}

	RedisClient = redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := RedisClient.Ping(ctx).Err(); err != nil {
		log.Printf("[Redis] 连接失败: %v，Redis 未启用", err)
		RedisClient = nil
		return
	}

	log.Println("[Redis] 连接成功")
}

// RedisSet 设置键值对（带过期时间）。
func RedisSet(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if RedisClient == nil {
		return nil
	}
	return RedisClient.Set(ctx, key, value, expiration).Err()
}

// RedisGet 获取键值。
func RedisGet(ctx context.Context, key string) (string, error) {
	if RedisClient == nil {
		return "", redis.Nil
	}
	return RedisClient.Get(ctx, key).Result()
}

// RedisDel 删除键。
func RedisDel(ctx context.Context, keys ...string) error {
	if RedisClient == nil {
		return nil
	}
	return RedisClient.Del(ctx, keys...).Err()
}

// RedisIncr 原子递增。
func RedisIncr(ctx context.Context, key string) (int64, error) {
	if RedisClient == nil {
		return 0, nil
	}
	return RedisClient.Incr(ctx, key).Result()
}
