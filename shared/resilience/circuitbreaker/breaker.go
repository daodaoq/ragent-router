// Package circuitbreaker 实现三态熔断器 + Redis 分布式状态同步。
//
// 高并发方案 2：通过 Redis Pub/Sub 实现跨实例熔断状态同步。
// 面试考点：熔断器三态转换、滑动窗口、分布式状态一致性。
package circuitbreaker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

// State 熔断器状态。
type State int

const (
	StateClosed   State = iota // 正常
	StateOpen                  // 熔断
	StateHalfOpen              // 半开探测
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// Config 熔断器配置。
type Config struct {
	FailureThreshold float64       // 失败率阈值 (0.0-1.0)
	WindowDuration   time.Duration // 滑动窗口时长
	BucketCount      int           // 窗口桶数量
	OpenTimeout      time.Duration // Open 状态持续时间
	HalfOpenMaxReqs  int           // HalfOpen 最大探测数
}

func DefaultConfig() Config {
	return Config{
		FailureThreshold: 0.5,
		WindowDuration:   10 * time.Second,
		BucketCount:      10,
		OpenTimeout:      30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
}

type bucket struct {
	failures  int64
	successes int64
}

// CircuitBreaker 三态熔断器。
type CircuitBreaker struct {
	mu sync.Mutex

	state           State
	failureThreshold float64
	windowDuration   time.Duration
	bucketDuration   time.Duration
	openTimeout      time.Duration
	halfOpenMaxReqs  int

	buckets     []bucket
	bucketCount int
	windowStart time.Time

	lastFailureTime time.Time
	halfOpenReqs    int
	totalFailures   int64
	totalSuccesses  int64

	// 分布式状态同步
	name   string       // 熔断器名称（用于 Redis 键）
	rdb    *redis.Client // Redis 客户端（nil 则不同步）
	pubsub *redis.PubSub
}

// New 创建熔断器。
func New(name string, cfg Config, rdb *redis.Client) *CircuitBreaker {
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

	cb := &CircuitBreaker{
		state:            StateClosed,
		name:             name,
		rdb:              rdb,
		failureThreshold: cfg.FailureThreshold,
		windowDuration:   cfg.WindowDuration,
		bucketDuration:   cfg.WindowDuration / time.Duration(cfg.BucketCount),
		openTimeout:      cfg.OpenTimeout,
		halfOpenMaxReqs:  cfg.HalfOpenMaxReqs,
		buckets:          make([]bucket, cfg.BucketCount),
		bucketCount:      cfg.BucketCount,
		windowStart:      time.Now(),
	}

	// 启动分布式状态同步监听
	if rdb != nil {
		go cb.listenStateSync()
	}

	return cb
}

// Call 在熔断器保护下执行 fn。
func (cb *CircuitBreaker) Call(fn func() error) error {
	if err := cb.allowRequest(); err != nil {
		return err
	}
	err := fn()
	cb.recordResult(err)
	return err
}

// State 返回当前状态。
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Stats 统计快照。
type Stats struct {
	State           State
	TotalFailures   int64
	TotalSuccesses  int64
	WindowFailures  int64
	WindowSuccesses int64
}

func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	var wf, ws int64
	for _, b := range cb.buckets {
		wf += b.failures
		ws += b.successes
	}
	return Stats{
		State:           cb.state,
		TotalFailures:   cb.totalFailures,
		TotalSuccesses:  cb.totalSuccesses,
		WindowFailures:  wf,
		WindowSuccesses: ws,
	}
}

// ────────────────────────────────────────────────────────────
// 内部方法
// ────────────────────────────────────────────────────────────

func (cb *CircuitBreaker) allowRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.slideWindow()

	switch cb.state {
	case StateClosed:
		if cb.shouldTrip() {
			cb.transitionTo(StateOpen)
			return ErrCircuitOpen
		}
		return nil
	case StateOpen:
		if time.Since(cb.lastFailureTime) >= cb.openTimeout {
			cb.transitionTo(StateHalfOpen)
			cb.halfOpenReqs++
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		if cb.halfOpenReqs >= cb.halfOpenMaxReqs {
			return ErrCircuitOpen
		}
		cb.halfOpenReqs++
		return nil
	default:
		return ErrCircuitOpen
	}
}

func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.recordFailure()
	} else {
		cb.recordSuccess()
	}

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

func (cb *CircuitBreaker) shouldTrip() bool {
	var totalFailures, totalSuccesses int64
	for _, b := range cb.buckets {
		totalFailures += b.failures
		totalSuccesses += b.successes
	}
	total := totalFailures + totalSuccesses
	if total == 0 {
		return false
	}
	return float64(totalFailures)/float64(total) >= cb.failureThreshold
}

func (cb *CircuitBreaker) currentBucket() int {
	elapsed := time.Since(cb.windowStart)
	idx := int(elapsed / cb.bucketDuration)
	if idx >= cb.bucketCount {
		idx = cb.bucketCount - 1
	}
	return idx
}

func (cb *CircuitBreaker) slideWindow() {
	elapsed := time.Since(cb.windowStart)
	toSlide := int(elapsed / cb.bucketDuration)
	if toSlide <= 0 {
		return
	}
	for i := 0; i < toSlide && i < cb.bucketCount; i++ {
		idx := (cb.currentBucket() + 1 + i) % cb.bucketCount
		cb.buckets[idx] = bucket{}
	}
	cb.windowStart = cb.windowStart.Add(time.Duration(toSlide) * cb.bucketDuration)
}

func (cb *CircuitBreaker) transitionTo(newState State) {
	oldState := cb.state
	cb.state = newState
	cb.halfOpenReqs = 0
	if newState == StateOpen {
		cb.lastFailureTime = time.Now()
	}

	// 分布式状态同步：广播状态变更
	if cb.rdb != nil && oldState != newState {
		go cb.publishStateChange(newState)
	}
}

// ────────────────────────────────────────────────────────────
// 分布式状态同步（Redis Pub/Sub）
// ────────────────────────────────────────────────────────────

type stateChangeMsg struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Timestamp int64  `json:"ts"`
}

const stateSyncChannel = "ragent:circuitbreaker:sync"

// publishStateChange 发布状态变更到 Redis。
func (cb *CircuitBreaker) publishStateChange(newState State) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg, _ := json.Marshal(stateChangeMsg{
		Name:      cb.name,
		State:     newState.String(),
		Timestamp: time.Now().UnixMilli(),
	})
	if err := cb.rdb.Publish(ctx, stateSyncChannel, msg).Err(); err != nil {
		log.Printf("[熔断器] 状态同步发布失败: %v", err)
	}
}

// listenStateSync 监听其他实例的状态变更。
func (cb *CircuitBreaker) listenStateSync() {
	ctx := context.Background()
	cb.pubsub = cb.rdb.Subscribe(ctx, stateSyncChannel)
	defer cb.pubsub.Close()

	ch := cb.pubsub.Channel()
	for msg := range ch {
		var sc stateChangeMsg
		if err := json.Unmarshal([]byte(msg.Payload), &sc); err != nil {
			continue
		}
		if sc.Name != cb.name {
			continue // 不是自己的状态
		}

		// 应用远程状态变更
		cb.mu.Lock()
		switch sc.State {
		case "open":
			if cb.state != StateOpen {
				cb.state = StateOpen
				cb.lastFailureTime = time.Now()
				cb.halfOpenReqs = 0
			}
		case "half-open":
			if cb.state == StateOpen {
				cb.state = StateHalfOpen
				cb.halfOpenReqs = 0
			}
		case "closed":
			if cb.state != StateClosed {
				cb.state = StateClosed
				cb.halfOpenReqs = 0
			}
		}
		cb.mu.Unlock()
	}
}
