package ratelimit

import (
	"context"
	"sync"
	"time"
)

// ────────────────────────────────────────────────────────────
// FNV-64a 哈希函数
// ────────────────────────────────────────────────────────────

// FNV-64a 常量（Fowler–Noll–Vo 哈希，64 位变体）。
// 选择理由：非加密哈希中速度最快的一档，分布均匀性优秀，
// 适合分片存储的 key 路由场景。
const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

// HashFNV64a 计算字符串的 FNV-64a 哈希值。
// 非加密哈希——选它是为了速度和分布均匀性，而非碰撞抵抗。
// 在分片存储中，碰撞只会导致两个不同 key 路由到同一分片，
// 不产生正确性问题，仅略微增加该分片的负载。
func HashFNV64a(s string) uint64 {
	h := fnvOffset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// ────────────────────────────────────────────────────────────
// Store 接口
// ────────────────────────────────────────────────────────────

// Store 是限流器状态的持久化抽象。
// 当前有两种实现：
//   - ShardedStore：内存存储，分片锁，适合单实例部署
//   - RedisStore：Redis 存储，分布式一致性，适合多实例部署（待实现）
//
// 接口方法的语义：
//   - Load：如果 key 存在则返回已有值；否则调用 builder 创建新值、
//     存储并返回。必须并发安全且保证 builder 在并发 Load 同一 key 时
//     尽量少被调用（理想情况仅调用 1 次）。
//   - Store：直接覆盖 key 的值，用于外部更新（如修改限流配额）。
type Store interface {
	Load(key string, builder func() interface{}) interface{}
	Store(key string, value interface{}) error
}

// ────────────────────────────────────────────────────────────
// 分片存储
// ────────────────────────────────────────────────────────────

// ShardCount 是分片数。
//
// 为什么是 2048？
//   - 足够大：在 32 核高并发下，期望冲突数 = goroutines / shards ≈ 0.015/shard，
//     锁竞争基本消除。
//   - 不太大：每个 Shard 包含 2 个 map + 1 个 RWMutex ≈ 100 bytes，
//     2048 个 ≈ 200KB，内存开销可忽略。
//   - 二次幂：对取模运算友好（编译器可能优化为位运算）。
const ShardCount = 2048

// Shard 是分片存储的一个分区。每个分片持有独立的 sync.RWMutex，
// 因此不同分片的操作可以完全并行。
type Shard struct {
	mu         sync.RWMutex
	data       map[string]interface{} // key → 限流器实例
	lastAccess map[string]time.Time   // key → 最后访问时间（用于 TTL 驱逐）
}

// newShard 创建空分片。
func newShard() *Shard {
	return &Shard{
		data:       make(map[string]interface{}),
		lastAccess: make(map[string]time.Time),
	}
}

// ShardedStore 是高并发 KV 存储的核心实现。
//
// # 设计
//
// 数据通过 FNV-64a 哈希分散到 ShardCount 个分片。
// 每个分片有独立的 sync.RWMutex——不同分片的操作无锁竞争。
//
// # Double-Checked Locking
//
// Load 方法使用双重检查锁定模式：
//
//	1. RLock  → 检查 key 是否存在 → RUnlock   （乐观路径，无竞争）
//	2. Lock   → 二次检查          → 创建/存储 → Unlock（悲观路径，仅缓存未命中时）
//
// 第一次检查（读锁）是乐观的：大多数 Load 调用 key 已存在，
// 只需读锁即可返回。第二次检查（写锁内）是必要的：
// 在我们释放读锁到获取写锁之间，另一个 goroutine 可能已经创建了该 key。
// 没有二次检查会导致两个 goroutine 各创建一个值，先创建的那个会泄漏。
//
// # TTL 驱逐
//
// 后台 goroutine 周期性扫描所有分片，删除超过 TTL 未访问的条目。
// 清理时持写锁，但仅在收集过期 key 并删除期间持有——sleep 期间释放锁。
type ShardedStore struct {
	shards []*Shard
	hasher func(string) uint64
	cancel context.CancelFunc
}

// NewShardedStore 创建分片存储并启动后台 TTL 驱逐。
//
// 参数：
//   - ctx：用于控制后台驱逐 goroutine 的生命周期
//   - ttl：条目无访问后的存活时间
func NewShardedStore(ctx context.Context, ttl time.Duration) *ShardedStore {
	ctx, cancel := context.WithCancel(ctx)
	shards := make([]*Shard, ShardCount)
	for i := range shards {
		shards[i] = newShard()
	}

	s := &ShardedStore{
		shards: shards,
		hasher: HashFNV64a,
		cancel: cancel,
	}

	go s.evictLoop(ctx, ttl)
	return s
}

// Load 获取 key 对应的值，若不存在则通过 builder 创建。
//
// 并发安全：多个 goroutine 同时 Load 同一 key，builder 可能被调用
// 1 次或极少数次（取决于竞态窗口），但绝不会超过并发 goroutine 数。
// 返回值唯一——所有调用者拿到同一个对象。
func (s *ShardedStore) Load(key string, builder func() interface{}) interface{} {
	sh := s.shards[s.shard(key)]

	// ── 阶段 1：乐观读 ──
	// 大多数请求在这里返回，成本仅是一次 RLock + 一次 map 查询。
	sh.mu.RLock()
	v, ok := sh.data[key]
	sh.mu.RUnlock()
	if ok {
		return v
	}

	// ── 阶段 2：悲观写 ──
	// 在获取写锁之前先调用 builder。builder 可能很重（涉及内存分配），
	// 放在锁外执行可以减少临界区的长度。如果另一个 goroutine 抢先创建了
	// key，这个 newVal 会被丢弃——代价是一次多余的内存分配，但换来了
	// 更短的锁持有时间。
	newVal := builder()
	sh.mu.Lock()
	defer sh.mu.Unlock()

	// ── 阶段 3：二次检查 ──
	// 防止 TOCTOU 竞争：RUnlock 到 Lock 之间，另一个 goroutine 可能
	// 已经创建了 key。不加这行 → 两个值同时存在 → 内存泄漏 + 数据不一致。
	v, ok = sh.data[key]
	if ok {
		return v // 另一个 goroutine 抢先了，返回它的值
	}

	sh.data[key] = newVal
	sh.lastAccess[key] = time.Now()
	return newVal
}

// Store 保存一个值到指定 key（覆盖已有值）。
func (s *ShardedStore) Store(key string, value interface{}) error {
	sh := s.shards[s.shard(key)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.data[key] = value
	sh.lastAccess[key] = time.Now()
	return nil
}

// shard 计算 key 所属的分片索引。
func (s *ShardedStore) shard(key string) uint64 {
	return s.hasher(key) % ShardCount
}

// Close 停止后台 TTL 驱逐 goroutine。
func (s *ShardedStore) Close() error {
	s.cancel()
	return nil
}

// ────────────────────────────────────────────────────────────
// TTL 驱逐
// ────────────────────────────────────────────────────────────

// evictLoop 周期性扫描所有分片，删除超过 TTL 未访问的条目。
//
// 驱逐频率 = TTL / 2。用一半 TTL 作为间隔是为了避免：
//   - TTL 到期后条目仍残留在内存中太久（驱逐间隔太大）
//   - 过于频繁的扫描浪费 CPU（驱逐间隔太小）
//
// 锁策略：每个分片独立处理——持锁 → 收集过期 key → 删除 → 释放。
// 不会在持锁状态下 sleep，因此不会阻塞读写请求超过 O(过期条目数)。
func (s *ShardedStore) evictLoop(ctx context.Context, ttl time.Duration) {
	ticker := time.NewTicker(ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, sh := range s.shards {
				sh.mu.Lock()
				for k, lastAccess := range sh.lastAccess {
					if now.Sub(lastAccess) > ttl {
						delete(sh.data, k)
						delete(sh.lastAccess, k)
					}
				}
				sh.mu.Unlock()
			}
		}
	}
}

// ────────────────────────────────────────────────────────────
// 统计
// ────────────────────────────────────────────────────────────

// Stats 返回所有分片的聚合统计（只读快照）。
func (s *ShardedStore) Stats() StoreStats {
	var total int
	for _, sh := range s.shards {
		sh.mu.RLock()
		total += len(sh.data)
		sh.mu.RUnlock()
	}
	return StoreStats{Entries: total, Shards: ShardCount}
}

// StoreStats 是分片存储的聚合指标。
type StoreStats struct {
	Entries int // 当前存储的条目总数
	Shards  int // 分片数量
}
