package common

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
)

// ────────────────────────────────────────────────────────────
// Redis 分布式锁（基于 SET NX EX + Lua 原子释放）
//
// 面试考点：
//  1. 为什么用 SET NX EX 而不是 SETNX + EXPIRE？（原子性，避免死锁）
//  2. 为什么释放锁要用 Lua 脚本？（原子性，防止误删别人的锁）
//  3. 什么是 Redlock？（多节点 Redis 集群的分布式锁算法）
//  4. 锁的续约机制？（Watchdog，防止业务未完成锁已过期）
// ────────────────────────────────────────────────────────────

var (
	ErrLockNotAcquired = errors.New("分布式锁未获取")
	ErrLockReleased    = errors.New("分布式锁已释放")
)

// RedisLock 基于 Redis 的分布式锁。
//
// 实现原理：
//   - 加锁：SET key value NX EX（不存在时设置 + 过期时间，原子操作）
//   - 释放：Lua 脚本（先比较 value，再 DEL，保证原子性）
//   - 续约：后台 goroutine 定期续期（Watchdog 机制）
type RedisLock struct {
	client   *redis.Client
	key      string
	value    string // 唯一标识，防止误删
	ttl      time.Duration
	cancel   context.CancelFunc // 用于停止续约 goroutine
	acquired bool
}

// NewRedisLock 创建分布式锁。
//
// 参数：
//   - client: Redis 客户端
//   - key: 锁的 key（建议按业务命名，如 "lock:channel:update:123"）
//   - ttl: 锁的过期时间（防止死锁，建议 10-30s）
func NewRedisLock(client *redis.Client, key string, ttl time.Duration) *RedisLock {
	return &RedisLock{
		client: client,
		key:    "lock:" + key,
		value:  GenerateRandomKey(16), // 每个锁实例唯一
		ttl:    ttl,
	}
}

// acquireScript Lua 脚本：原子加锁（其实 SET NX EX 已经够了，这里展示 Lua 能力）
//
// KEYS[1] = 锁 key
// ARGV[1] = 锁 value（唯一标识）
// ARGV[2] = 过期时间（秒）
var acquireScript = redis.NewScript(`
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'EX', ARGV[2]) then
    return 1
end
return 0
`)

// releaseScript Lua 脚本：原子释放锁（先比较 value 再删除，防止误删）
//
// KEYS[1] = 锁 key
// ARGV[1] = 锁 value
//
// 为什么不能用 DEL key？
// 因为 DEL 不检查 value，可能删掉别人的锁。
// 例如：线程 A 获取锁 → 业务超时 → 锁自动过期 → 线程 B 获取锁 → 线程 A 执行 DEL → 删掉了 B 的锁！
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)

// extendScript Lua 脚本：原子续约（延长过期时间，仅当锁仍属于自己时）
//
// KEYS[1] = 锁 key
// ARGV[1] = 锁 value
// ARGV[2] = 新的过期时间（秒）
var extendScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

// Acquire 获取锁（阻塞重试）。
//
// 参数：
//   - ctx: 上下文（用于超时控制）
//   - retryInterval: 重试间隔
//   - maxRetries: 最大重试次数（0 = 无限重试）
func (l *RedisLock) Acquire(ctx context.Context, retryInterval time.Duration, maxRetries int) error {
	if RedisClient == nil {
		return errors.New("Redis 未启用")
	}

	for i := 0; maxRetries == 0 || i < maxRetries; i++ {
		result, err := acquireScript.Run(ctx, l.client, []string{l.key}, l.value, int(l.ttl.Seconds())).Int()
		if err != nil {
			return err
		}
		if result == 1 {
			l.acquired = true
			// 启动 Watchdog 自动续约
			l.startWatchdog(ctx)
			return nil
		}

		// 未获取到锁，等待重试
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
			// 继续重试
		}
	}

	return ErrLockNotAcquired
}

// TryAcquire 尝试获取锁（非阻塞，立即返回）。
func (l *RedisLock) TryAcquire(ctx context.Context) (bool, error) {
	if RedisClient == nil {
		return false, errors.New("Redis 未启用")
	}

	result, err := acquireScript.Run(ctx, l.client, []string{l.key}, l.value, int(l.ttl.Seconds())).Int()
	if err != nil {
		return false, err
	}
	if result == 1 {
		l.acquired = true
		l.startWatchdog(ctx)
		return true, nil
	}
	return false, nil
}

// Release 释放锁。
func (l *RedisLock) Release(ctx context.Context) error {
	if !l.acquired {
		return ErrLockReleased
	}

	// 停止 Watchdog
	if l.cancel != nil {
		l.cancel()
	}

	result, err := releaseScript.Run(ctx, l.client, []string{l.key}, l.value).Int()
	if err != nil {
		return err
	}

	l.acquired = false
	if result == 0 {
		// 锁已过期或不属于自己
		return nil
	}
	return nil
}

// startWatchdog 启动后台续约 goroutine。
//
// Watchdog 机制：
//   - 每隔 ttl/3 秒续约一次
//   - 如果锁已过期或被其他实例获取，自动停止
//   - 防止业务执行时间超过锁的 TTL 导致锁提前释放
func (l *RedisLock) startWatchdog(ctx context.Context) {
	watchCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel

	go func() {
		interval := l.ttl / 3
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				result, err := extendScript.Run(watchCtx, l.client, []string{l.key}, l.value, int(l.ttl.Seconds())).Int()
				if err != nil || result == 0 {
					// 锁已不属于自己，停止续约
					return
				}
			}
		}
	}()
}

// IsAcquired 检查锁是否已获取。
func (l *RedisLock) IsAcquired() bool {
	return l.acquired
}

// ────────────────────────────────────────────────────────────
// 便捷函数
// ────────────────────────────────────────────────────────────

// WithLock 分布式锁的便捷用法，自动加锁和释放。
//
// 用法：
//
//	err := common.WithLock(ctx, redisClient, "my-resource", 10*time.Second, func(ctx context.Context) error {
//	    // 在锁保护下执行业务逻辑
//	    return nil
//	})
func WithLock(ctx context.Context, client *redis.Client, key string, ttl time.Duration, fn func(ctx context.Context) error) error {
	lock := NewRedisLock(client, key, ttl)
	if err := lock.Acquire(ctx, 100*time.Millisecond, 30); err != nil {
		return err
	}
	defer lock.Release(ctx)
	return fn(ctx)
}
