// Package redis — Redis Streams 异步消息队列。
//
// 高并发方案 4：基于 Redis Streams 的异步日志和事件处理。
//
// 面试考点：
//   - Redis Streams vs Kafka：轻量级 vs 重量级，适合中小规模 vs 大规模
//   - Consumer Group：多消费者负载均衡，消息不丢失
//   - XACK：消息确认机制，防止重复消费
//   - 持久化：Redis AOF 保证消息不丢
package redis

import (
	"context"
	"encoding/json"
	"log"
	"time"

	goRedis "github.com/redis/go-redis/v9"
)

// ────────────────────────────────────────────────────────────
// Stream Producer（生产者）
// ────────────────────────────────────────────────────────────

const (
	// StreamRequestLog 请求日志流。
	StreamRequestLog = "ragent:stream:requests"
	// StreamProviderEvent 供应商事件流。
	StreamProviderEvent = "ragent:stream:provider_events"
	// GroupLogConsumer 日志消费者组。
	GroupLogConsumer = "log_consumers"
	// GroupEventConsumer 事件消费者组。
	GroupEventConsumer = "event_consumers"
)

// RequestLogEntry 请求日志条目。
type RequestLogEntry struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	Status           string  `json:"status"`
	StatusCode       int     `json:"status_code"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	LatencyMs        int64   `json:"latency_ms"`
	Timestamp        int64   `json:"ts"`
}

// PublishRequestLog 发布请求日志到 Redis Streams。
//
// 使用 XADD 命令，消息自动分配唯一 ID（时间戳-序号）。
// 消息会持久化到 Redis AOF，即使消费者离线也不会丢失。
func PublishRequestLog(ctx context.Context, entry RequestLogEntry) error {
	if Client == nil {
		return nil
	}

	entry.Timestamp = time.Now().UnixMilli()
	data, _ := json.Marshal(entry)

	// XADD stream * field1 value1 field2 value2 ...
	// * 表示让 Redis 自动生成 ID
	return Client.XAdd(ctx, &goRedis.XAddArgs{
		Stream: StreamRequestLog,
		MaxLen: 100000, // 最多保留 10 万条（近似裁剪）
		Approx: true,   // 近似裁剪，性能更好
		Values: map[string]interface{}{
			"data": string(data),
		},
	}).Err()
}

// PublishProviderEvent 发布供应商事件到 Redis Streams。
func PublishProviderEvent(ctx context.Context, event map[string]interface{}) error {
	if Client == nil {
		return nil
	}

	values := make(map[string]interface{})
	for k, v := range event {
		values[k] = v
	}
	values["ts"] = time.Now().UnixMilli()

	return Client.XAdd(ctx, &goRedis.XAddArgs{
		Stream: StreamProviderEvent,
		MaxLen: 10000,
		Approx: true,
		Values: values,
	}).Err()
}

// ────────────────────────────────────────────────────────────
// Stream Consumer（消费者）
// ────────────────────────────────────────────────────────────

// StreamConsumer Redis Streams 消费者。
//
// 使用 Consumer Group 实现：
//   - 多个消费者实例自动负载均衡
//   - 每条消息只被一个消费者处理
//   - XACK 确认后消息标记为已处理
//   - 消费者崩溃后，未确认的消息可以被其他消费者接管（Pending Claim）
type StreamConsumer struct {
	client    *goRedis.Client
	stream    string
	group     string
	consumer  string
	handler   func(msg goRedis.XMessage) error
}

// NewStreamConsumer 创建消费者。
func NewStreamConsumer(stream, group, consumer string, handler func(goRedis.XMessage) error) *StreamConsumer {
	return &StreamConsumer{
		client:   Client,
		stream:   stream,
		group:    group,
		consumer: consumer,
		handler:  handler,
	}
}

// Start 启动消费循环。
//
// 流程：
//  1. 创建 Consumer Group（如果不存在）
//  2. 循环 XREADGROUP 读取新消息
//  3. 处理消息 → XACK 确认
//  4. 处理失败的消息进入 Pending List，等待重试
func (c *StreamConsumer) Start(ctx context.Context) {
	if c.client == nil {
		log.Printf("[Stream Consumer] Redis 未连接，跳过")
		return
	}

	// 创建 Consumer Group（幂等操作）
	c.client.XGroupCreateMkStream(ctx, c.stream, c.group, "0")

	log.Printf("[Stream Consumer] 启动: stream=%s, group=%s, consumer=%s",
		c.stream, c.group, c.consumer)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Stream Consumer] 停止: %s", c.consumer)
			return
		default:
		}

		// XREADGROUP GROUP group consumer COUNT 10 BLOCK 2000 STREAMS stream >
		// > 表示只读取未分配的新消息
		msgs, err := c.client.XReadGroup(ctx, &goRedis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.consumer,
			Streams:  []string{c.stream, ">"},
			Count:    10,
			Block:    2 * time.Second,
		}).Result()

		if err != nil {
			if err == goRedis.Nil {
				continue // 超时，无新消息
			}
			log.Printf("[Stream Consumer] XREADGROUP 错误: %v", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				if err := c.handler(msg); err != nil {
					log.Printf("[Stream Consumer] 处理失败: id=%s, err=%v", msg.ID, err)
					// 不 ACK → 消息留在 Pending List，后续可重试
					continue
				}
				// 处理成功 → ACK
				c.client.XAck(ctx, c.stream, c.group, msg.ID)
			}
		}
	}
}

// ClaimPendingMessages 接管超时的未确认消息（消费者崩溃恢复）。
//
// 当消费者 A 崩溃时，它未 ACK 的消息会留在 Pending List。
// 消费者 B 可以通过 XCLAIM 接管这些消息。
func (c *StreamConsumer) ClaimPendingMessages(ctx context.Context, minIdleTime time.Duration) error {
	if c.client == nil {
		return nil
	}

	// XPENDING 查询未确认的消息
	pending, err := c.client.XPendingExt(ctx, &goRedis.XPendingExtArgs{
		Stream:   c.stream,
		Group:    c.group,
		Idle:     minIdleTime,
		Start:    "-",
		End:      "+",
		Count:    100,
	}).Result()

	if err != nil {
		return err
	}

	for _, p := range pending {
		// XCLAIM 接管消息
		msgs, err := c.client.XClaim(ctx, &goRedis.XClaimArgs{
			Stream:   c.stream,
			Group:    c.group,
			Consumer: c.consumer,
			MinIdle:  minIdleTime,
			Messages: []string{p.ID},
		}).Result()

		if err != nil {
			log.Printf("[Stream Consumer] XCLAIM 失败: %v", err)
			continue
		}

		for _, msg := range msgs {
			log.Printf("[Stream Consumer] 接管消息: id=%s", msg.ID)
			if err := c.handler(msg); err != nil {
				log.Printf("[Stream Consumer] 重试处理失败: %v", err)
				continue
			}
			c.client.XAck(ctx, c.stream, c.group, msg.ID)
		}
	}

	return nil
}
