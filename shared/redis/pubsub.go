package redis

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	goRedis "github.com/redis/go-redis/v9"
)

// ────────────────────────────────────────────────────────────
// Redis Pub/Sub 事件广播系统
//
// 面试考点：
//  1. Pub/Sub 的消息可靠性？（fire-and-forget，不持久化，订阅者离线会丢失）
//  2. 与 Redis Streams 的区别？（Streams 持久化、消费者组、ACK 机制）
//  3. 适用场景？（实时通知、事件广播、配置更新推送）
//  4. 生产环境如何保证消息可靠？（用 Streams 替代，或业务层重试）
// ────────────────────────────────────────────────────────────

// Event 事件结构。
type Event struct {
	Type      string      `json:"type"`       // 事件类型
	Source    string      `json:"source"`     // 事件来源
	Data      interface{} `json:"data"`       // 事件数据
	Timestamp int64       `json:"timestamp"`  // 时间戳
}

// EventHandler 事件处理函数。
type EventHandler func(event Event)

// EventBus 基于 Redis Pub/Sub 的事件总线。
//
// 特性：
//   - 支持多频道订阅
//   - 支持通配符订阅（PSUBSCRIBE）
//   - 自动重连（连接断开后自动恢复订阅）
//   - 本地事件处理器注册
type EventBus struct {
	client     *goRedis.Client
	handlers   map[string][]EventHandler // channel → handlers
	patterns   map[string][]EventHandler // pattern → handlers
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewEventBus 创建事件总线。
func NewEventBus(client *goRedis.Client) *EventBus {
	ctx, cancel := context.WithCancel(context.Background())
	return &EventBus{
		client:   client,
		handlers: make(map[string][]EventHandler),
		patterns: make(map[string][]EventHandler),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Publish 发布事件到指定频道。
func (b *EventBus) Publish(ctx context.Context, channel string, event Event) error {
	if b.client == nil {
		return nil
	}

	event.Timestamp = time.Now().UnixMilli()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return b.client.Publish(ctx, channel, data).Err()
}

// Subscribe 订阅指定频道。
func (b *EventBus) Subscribe(channel string, handler EventHandler) {
	b.mu.Lock()
	b.handlers[channel] = append(b.handlers[channel], handler)
	b.mu.Unlock()
}

// PSubscribe 订阅通配符频道（如 "event:*"）。
func (b *EventBus) PSubscribe(pattern string, handler EventHandler) {
	b.mu.Lock()
	b.patterns[pattern] = append(b.patterns[pattern], handler)
	b.mu.Unlock()
}

// Start 启动事件总线（开始监听订阅）。
func (b *EventBus) Start() {
	if b.client == nil {
		return
	}

	// 收集所有订阅的频道
	b.mu.RLock()
	channels := make([]string, 0, len(b.handlers))
	for ch := range b.handlers {
		channels = append(channels, ch)
	}
	patterns := make([]string, 0, len(b.patterns))
	for p := range b.patterns {
		patterns = append(patterns, p)
	}
	b.mu.RUnlock()

	// 启动普通订阅
	if len(channels) > 0 {
		go b.listenChannels(channels)
	}

	// 启动通配符订阅
	if len(patterns) > 0 {
		go b.listenPatterns(patterns)
	}
}

// listenChannels 监听普通频道。
func (b *EventBus) listenChannels(channels []string) {
	pubsub := b.client.Subscribe(b.ctx, channels...)
	defer pubsub.Close()

	// 等待订阅确认
	if _, err := pubsub.Receive(b.ctx); err != nil {
		log.Printf("[EventBus] 订阅失败: %v", err)
		return
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-b.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			b.dispatch(msg.Channel, []byte(msg.Payload))
		}
	}
}

// listenPatterns 监听通配符频道。
func (b *EventBus) listenPatterns(patterns []string) {
	pubsub := b.client.PSubscribe(b.ctx, patterns...)
	defer pubsub.Close()

	if _, err := pubsub.Receive(b.ctx); err != nil {
		log.Printf("[EventBus] 通配符订阅失败: %v", err)
		return
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-b.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			b.dispatch(msg.Channel, []byte(msg.Payload))
		}
	}
}

// dispatch 分发事件到处理器。
func (b *EventBus) dispatch(channel string, payload []byte) {
	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		log.Printf("[EventBus] 事件解析失败: %v", err)
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	// 分发到精确匹配的处理器
	if handlers, ok := b.handlers[channel]; ok {
		for _, h := range handlers {
			go h(event)
		}
	}

	// 分发到通配符匹配的处理器
	for pattern, handlers := range b.patterns {
		if matchPattern(pattern, channel) {
			for _, h := range handlers {
				go h(event)
			}
		}
	}
}

// Stop 停止事件总线。
func (b *EventBus) Stop() {
	b.cancel()
}

// matchPattern 简单的通配符匹配（支持 *）。
func matchPattern(pattern, channel string) bool {
	if pattern == "*" {
		return true
	}
	// 简单实现：前缀匹配 "event:*" 匹配 "event:xxx"
	if len(pattern) > 1 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(channel) >= len(prefix) && channel[:len(prefix)] == prefix
	}
	return pattern == channel
}

// ────────────────────────────────────────────────────────────
// 预定义事件类型
// ────────────────────────────────────────────────────────────

const (
	// 频道定义
	ChannelRequest   = "event:request"   // 请求事件
	ChannelAlert     = "event:alert"     // 告警事件
	ChannelConfig    = "event:config"    // 配置变更事件
	ChannelChannel   = "event:channel"   // 渠道状态变更
	ChannelCircuit   = "event:circuit"   // 熔断器状态变更

	// 事件类型
	EventTypeRequestComplete = "request_complete" // 请求完成
	EventTypeRequestError    = "request_error"    // 请求失败
	EventTypeCacheHit        = "cache_hit"        // 缓存命中
	EventTypeCircuitOpen     = "circuit_open"     // 熔断器打开
	EventTypeCircuitClose    = "circuit_close"    // 熔断器关闭
	EventTypeChannelDisable  = "channel_disable"  // 渠道禁用
	EventTypeChannelEnable   = "channel_enable"   // 渠道启用
	EventTypeConfigUpdate    = "config_update"    // 配置更新
)

// 全局事件总线实例
var GlobalEventBus *EventBus

// InitEventBus 初始化全局事件总线。
func InitEventBus() {
	if Client == nil {
		return
	}
	GlobalEventBus = NewEventBus(Client)
	GlobalEventBus.Start()
	log.Println("[EventBus] 事件总线已启动")
}

// PublishEvent 发布事件的便捷函数。
func PublishEvent(channel string, eventType string, data interface{}) {
	if GlobalEventBus == nil {
		return
	}
	GlobalEventBus.Publish(context.Background(), channel, Event{
		Type:   eventType,
		Source: "ragent-router",
		Data:   data,
	})
}
