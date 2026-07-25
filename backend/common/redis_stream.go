package common

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
)

// ────────────────────────────────────────────────────────────
// Redis Streams 消息队列
//
// 面试考点：
//  1. Redis Streams vs Kafka vs RocketMQ？（轻量级 vs 重量级，适用场景不同）
//  2. 消费者组（Consumer Group）的作用？（负载均衡、消息不重复消费）
//  3. ACK 机制？（XACK 确认消息已处理，XCLAIM 转移超时消息）
//  4. 消息持久化？（Streams 持久化到 RDB/AOF，Pub/Sub 不持久化）
//  5. 如何保证消息可靠？（生产者确认 + 消费者 ACK + 死信队列）
// ────────────────────────────────────────────────────────────

const (
	// Stream 名称
	StreamRequestLog = "stream:request_log" // 请求日志流
	StreamEvent      = "stream:event"       // 事件流
	StreamAlert      = "stream:alert"       // 告警流

	// 消费者组名称
	ConsumerGroupLog    = "group:log_processor"
	ConsumerGroupEvent  = "group:event_processor"
	ConsumerGroupAlert  = "group:alert_processor"

	// 死信队列
	StreamDeadLetter = "stream:dead_letter"
)

// StreamMessage 流消息结构。
type StreamMessage struct {
	ID     string                 `json:"id"`     // 消息 ID
	Fields map[string]interface{} `json:"fields"` // 消息内容
}

// StreamProducer 消息生产者。
type StreamProducer struct {
	client *redis.Client
	stream string
}

// NewStreamProducer 创建生产者。
func NewStreamProducer(client *redis.Client, stream string) *StreamProducer {
	return &StreamProducer{
		client: client,
		stream: stream,
	}
}

// Publish 发布消息到流。
//
// 返回值：消息 ID（由 Redis 自动生成，格式 "时间戳-序号"）
func (p *StreamProducer) Publish(ctx context.Context, data interface{}) (string, error) {
	if p.client == nil {
		return "", nil
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	result, err := p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		MaxLen: 10000, // 最多保留 10000 条消息
		Approx: true,  // 近似裁剪，性能更好
		Values: map[string]interface{}{
			"data":      string(jsonData),
			"timestamp": time.Now().UnixMilli(),
		},
	}).Result()
	if err != nil {
		return "", err
	}

	return result, nil
}

// PublishBatch 批量发布消息。
func (p *StreamProducer) PublishBatch(ctx context.Context, messages []interface{}) ([]string, error) {
	if p.client == nil {
		return nil, nil
	}

	pipe := p.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(messages))

	for i, msg := range messages {
		jsonData, _ := json.Marshal(msg)
		cmds[i] = pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: p.stream,
			MaxLen: 10000,
			Approx: true,
			Values: map[string]interface{}{
				"data":      string(jsonData),
				"timestamp": time.Now().UnixMilli(),
			},
		})
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	ids := make([]string, len(messages))
	for i, cmd := range cmds {
		ids[i], _ = cmd.Result()
	}
	return ids, nil
}

// StreamConsumer 消息消费者。
type StreamConsumer struct {
	client       *redis.Client
	stream       string
	group        string
	consumer     string
	handler      func(msg StreamMessage) error
	batchSize    int64
	blockTimeout time.Duration
}

// NewStreamConsumer 创建消费者。
func NewStreamConsumer(client *redis.Client, stream, group, consumer string) *StreamConsumer {
	return &StreamConsumer{
		client:       client,
		stream:       stream,
		group:        group,
		consumer:     consumer,
		batchSize:    10,
		blockTimeout: 5 * time.Second,
	}
}

// SetHandler 设置消息处理函数。
func (c *StreamConsumer) SetHandler(handler func(msg StreamMessage) error) {
	c.handler = handler
}

// Start 启动消费者（阻塞）。
//
// 消费流程：
//  1. 创建消费者组（如果不存在）
//  2. 循环读取消息（XREADGROUP）
//  3. 处理消息
//  4. 确认消息（XACK）
//  5. 处理失败的消息进入死信队列
func (c *StreamConsumer) Start(ctx context.Context) error {
	if c.client == nil {
		return nil
	}

	// 创建消费者组（如果不存在）
	c.client.XGroupCreateMkStream(ctx, c.stream, c.group, "0")
	// 忽略 "BUSYGROUP" 错误（组已存在）

	log.Printf("[Stream] 消费者启动: stream=%s, group=%s, consumer=%s", c.stream, c.group, c.consumer)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// 读取消息
		streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.consumer,
			Streams:  []string{c.stream, ">"},
			Count:    c.batchSize,
			Block:    c.blockTimeout,
		}).Result()

		if err == redis.Nil {
			continue
		}
		if err != nil {
			log.Printf("[Stream] 读取消息失败: %v", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				c.processMessage(ctx, msg)
			}
		}
	}
}

// processMessage 处理单条消息。
func (c *StreamConsumer) processMessage(ctx context.Context, msg redis.XMessage) {
	streamMsg := StreamMessage{
		ID:     msg.ID,
		Fields: msg.Values,
	}

	if c.handler != nil {
		if err := c.handler(streamMsg); err != nil {
			log.Printf("[Stream] 消息处理失败: id=%s, err=%v", msg.ID, err)
			// 进入死信队列
			c.sendToDeadLetter(ctx, msg, err)
			return
		}
	}

	// 确认消息
	c.client.XAck(ctx, c.stream, c.group, msg.ID)
}

// sendToDeadLetter 发送到死信队列。
func (c *StreamConsumer) sendToDeadLetter(ctx context.Context, msg redis.XMessage, err error) {
	data, _ := json.Marshal(map[string]interface{}{
		"original_stream": c.stream,
		"original_id":     msg.ID,
		"error":           err.Error(),
		"payload":         msg.Values,
	})

	c.client.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamDeadLetter,
		MaxLen: 1000,
		Approx: true,
		Values: map[string]interface{}{
			"data":      string(data),
			"timestamp": time.Now().UnixMilli(),
		},
	})
}

// ────────────────────────────────────────────────────────────
// 便捷函数
// ────────────────────────────────────────────────────────────

// 全局生产者实例
var (
	LogProducer   *StreamProducer // 日志生产者
	EventProducer *StreamProducer // 事件生产者
)

// InitStreamProducers 初始化流生产者。
func InitStreamProducers() {
	if RedisClient == nil {
		return
	}
	LogProducer = NewStreamProducer(RedisClient, StreamRequestLog)
	EventProducer = NewStreamProducer(RedisClient, StreamEvent)
	log.Println("[Stream] 流生产者已初始化")
}

// PublishLog 发布日志消息。
func PublishLog(ctx context.Context, data interface{}) (string, error) {
	if LogProducer == nil {
		return "", nil
	}
	return LogProducer.Publish(ctx, data)
}
