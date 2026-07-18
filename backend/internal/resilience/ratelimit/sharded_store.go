package ratelimit

import (
	"context"
	"sync"
	"time"
)

// FNV-64a constants (Fowler–Noll–Vo hash, 64-bit variant).
const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

// HashFNV64a computes a FNV-64a hash of a string. This hash function
// is non-cryptographic—chosen for speed and uniform distribution
// rather than collision resistance.
func HashFNV64a(s string) uint64 {
	h := fnvOffset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// Store is the persistence abstraction for rate limiter state.
// Implementations include in-memory (ShardedStore) and Redis (RedisStore).
type Store interface {
	// Load returns the value for key if present, otherwise calls builder
	// to create a new value, stores it, and returns it.
	Load(key string, builder func() interface{}) interface{}
	// Store saves a value for the given key.
	Store(key string, value interface{}) error
}

// ShardCount is the number of shards in a ShardedStore. 2048 is chosen
// empirically: enough to virtually eliminate lock contention at typical
// concurrency levels, without excessive memory overhead (~200 KB total).
const ShardCount = 2048

// Shard is a single partition of the sharded store. Each shard has its
// own mutex, so contention is reduced to ≈ 1/ShardCount.
type Shard struct {
	mu         sync.RWMutex
	data       map[string]interface{}
	lastAccess map[string]time.Time
}

// newShard creates an empty shard.
func newShard() *Shard {
	return &Shard{
		data:       make(map[string]interface{}),
		lastAccess: make(map[string]time.Time),
	}
}

// ShardedStore is a high-concurrency key-value store that partitions data
// across ShardCount shards using FNV-64a hashing. Each shard is protected
// by its own sync.RWMutex, dramatically reducing lock contention compared
// to a single global mutex.
//
// Load uses double-checked locking:
//  1. RLock → check → RUnlock (optimistic, no contention)
//  2. Lock → double-check → create → Unlock (pessimistic, only on miss)
//
// This pattern ensures that the expensive write-lock path is only taken
// when the key genuinely does not exist, while the common case (key
// exists) only pays for a read lock.
type ShardedStore struct {
	shards  []*Shard
	hasher  func(string) uint64
	cancel  context.CancelFunc
}

// NewShardedStore creates a ShardedStore with background TTL eviction.
// ttl specifies how long entries live without access.
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

// Load retrieves a value by key, creating it via builder on cache miss.
func (s *ShardedStore) Load(key string, builder func() interface{}) interface{} {
	sh := s.shards[s.shard(key)]

	// Optimistic read — most calls return here.
	sh.mu.RLock()
	v, ok := sh.data[key]
	sh.mu.RUnlock()
	if ok {
		return v
	}

	// Key missing — take write lock and double-check.
	newVal := builder()
	sh.mu.Lock()
	defer sh.mu.Unlock()

	// Double-check: another goroutine may have created the key between
	// our RUnlock and Lock. Without this check, we'd create a duplicate
	// and the original holder would leak.
	v, ok = sh.data[key]
	if ok {
		return v
	}

	sh.data[key] = newVal
	sh.lastAccess[key] = time.Now()
	return newVal
}

// Store saves a value for the given key.
func (s *ShardedStore) Store(key string, value interface{}) error {
	sh := s.shards[s.shard(key)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.data[key] = value
	sh.lastAccess[key] = time.Now()
	return nil
}

// shard computes which shard a key maps to.
func (s *ShardedStore) shard(key string) uint64 {
	return s.hasher(key) % ShardCount
}

// Close stops the background eviction goroutine.
func (s *ShardedStore) Close() error {
	s.cancel()
	return nil
}

// evictLoop periodically removes expired entries. The lock is held only
// while collecting keys and deleting—not during the sleep interval.
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

// Stats returns aggregate statistics across all shards.
func (s *ShardedStore) Stats() StoreStats {
	var total int
	for _, sh := range s.shards {
		sh.mu.RLock()
		total += len(sh.data)
		sh.mu.RUnlock()
	}
	return StoreStats{Entries: total, Shards: ShardCount}
}

// StoreStats holds aggregate metrics for a ShardedStore.
type StoreStats struct {
	Entries int
	Shards  int
}
