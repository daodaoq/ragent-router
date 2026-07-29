// Package mq 提供消息队列集成（RocketMQ + Redis Streams 双通道）。
//
// 高并发方案 4 扩展：RocketMQ 作为重量级消息队列。
//
// 面试考点：
//   - RocketMQ vs Kafka：RocketMQ 原生支持延迟消息、事务消息，更适合业务场景
//   - Topic/Tag 二级分类：Topic 按业务划分，Tag 按消息类型细分
//   - Consumer Group：集群消费模式，多实例负载均衡
//   - Offset 管理：自动/手动提交，保证 at-least-once
//   - 同步/异步/单向发送：三种发送模式适用不同场景
//   - 顺序消息：通过 MessageQueueSelector 保证分区有序
//   - 事务消息：Half Message + 本地事务 + Commit/Rollback
package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/apache/rocketmq-client-go/v2"
	"github.com/apache/rocketmq-client-go/v2/consumer"
	"github.com/apache/rocketmq-client-go/v2/primitive"
	"github.com/apache/rocketmq-client-go/v2/producer"
)

// ────────────────────────────────────────────────────────────
// 消息结构
// ────────────────────────────────────────────────────────────

const (
	// TopicRequestLog 请求日志 Topic。
	TopicRequestLog = "ragent_request_log"
	// TopicProviderEvent 供应商事件 Topic。
	TopicProviderEvent = "ragent_provider_event"
	// TagHealth 健康检查事件 Tag。
	TagHealth = "health"
	// TagRateLimit 限流事件 Tag。
	TagRateLimit = "rate_limit"
	// TagCircuitBreaker 熔断事件 Tag。
	TagCircuitBreaker = "circuit_breaker"
)

// RequestLogMessage 请求日志消息。
type RequestLogMessage struct {
	RequestID        string  `json:"request_id"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	Status           string  `json:"status"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	LatencyMs        int64   `json:"latency_ms"`
	Timestamp        int64   `json:"ts"`
}

// ProviderEventMessage 供应商事件消息。
type ProviderEventMessage struct {
	ProviderID   int    `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	Action       string `json:"action"` // enable / disable / health_change
	Reason       string `json:"reason"`
	Timestamp    int64  `json:"ts"`
}

// ────────────────────────────────────────────────────────────
// 全局 Producer（单例）
// ────────────────────────────────────────────────────────────

var (
	globalProducerOnce sync.Once
	globalProducer     *RocketMQProducer
)

// InitGlobalProducer 初始化全局 RocketMQ Producer（应在服务启动时调用一次）。
//
// 参数：
//   - nameSrvAddr: NameServer 地址（如 "127.0.0.1:9876"）
//   - topic: 目标 Topic
//   - group: 生产者组名
//
// 如果 nameSrvAddr 为空，Producer 不会初始化（降级为仅 Redis Streams）。
func InitGlobalProducer(nameSrvAddr, topic, group string) {
	if nameSrvAddr == "" {
		log.Printf("[RocketMQ] NameServer 未配置，跳过初始化（仅使用 Redis Streams）")
		return
	}
	globalProducerOnce.Do(func() {
		globalProducer = NewRocketMQProducer(nameSrvAddr, topic, group, 10000)
	})
}

// GetGlobalProducer 获取全局 Producer（可能为 nil）。
func GetGlobalProducer() *RocketMQProducer {
	return globalProducer
}

// CloseGlobalProducer 关闭全局 Producer（服务优雅退出时调用）。
func CloseGlobalProducer() {
	if globalProducer != nil {
		globalProducer.Close()
	}
}

// ────────────────────────────────────────────────────────────
// RocketMQ Producer（生产者）
// ────────────────────────────────────────────────────────────

// RocketMQProducer RocketMQ 生产者。
//
// 面试考点：
//   - 同步发送（SendSync）：可靠，适合重要消息（订单、支付）
//   - 异步发送（SendAsync）：高吞吐，适合日志（本项目使用）
//   - 单向发送（SendOneway）：最高吞吐，不保证送达（监控指标）
//   - 批量发送：减少网络 RTT，提升吞吐
//   - 消息压缩：GZIP/SNAPPY，减少带宽
type RocketMQProducer struct {
	nameSrvAddr string
	topic       string
	group       string
	producer    rocketmq.Producer

	mu          sync.Mutex
	sentCount   int64
	failedCount int64
}

// NewRocketMQProducer 创建 RocketMQ 生产者。
//
// 参数：
//   - nameSrvAddr: NameServer 地址（如 "127.0.0.1:9876"）
//   - topic: 目标 Topic
//   - group: 生产者组名
//   - bufferSize: 异步发送缓冲区大小
func NewRocketMQProducer(nameSrvAddr, topic, group string, bufferSize int) *RocketMQProducer {
	if bufferSize <= 0 {
		bufferSize = 10000
	}

	p := &RocketMQProducer{
		nameSrvAddr: nameSrvAddr,
		topic:       topic,
		group:       group,
	}

	// 创建真实 RocketMQ Producer
	p.producer, _ = rocketmq.NewProducer(
		producer.WithNameServer([]string{nameSrvAddr}),
		producer.WithGroupName(group),
		producer.WithRetry(2),
		producer.WithSendMsgTimeout(3*time.Second),
	)

	if err := p.producer.Start(); err != nil {
		log.Printf("[RocketMQ Producer] 启动失败: %v（降级为无 MQ 模式）", err)
		p.producer = nil
	} else {
		log.Printf("[RocketMQ Producer] 启动成功: namesrv=%s, topic=%s, group=%s",
			nameSrvAddr, topic, group)
	}

	return p
}

// SendSync 同步发送（可靠，阻塞等待 Broker ACK）。
//
// 适用场景：重要业务消息（订单创建、支付通知）。
// 重试策略：默认重试 2 次，总耗时约 3 秒。
func (p *RocketMQProducer) SendSync(ctx context.Context, tag, key string, body interface{}) error {
	if p.producer == nil {
		return fmt.Errorf("rocketmq producer not initialized")
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	msg := &primitive.Message{
		Topic: p.topic,
		Body:  data,
	}
	msg.WithTag(tag)
	if key != "" {
		msg.WithKeys([]string{key})
	}

	result, err := p.producer.SendSync(ctx, msg)
	if err != nil {
		p.mu.Lock()
		p.failedCount++
		p.mu.Unlock()
		return fmt.Errorf("send sync: %w", err)
	}

	p.mu.Lock()
	p.sentCount++
	p.mu.Unlock()

	if os.Getenv("MQ_DEBUG") == "true" {
		log.Printf("[RocketMQ Producer] SendSync: topic=%s tag=%s key=%s msgID=%s",
			p.topic, tag, key, result.MsgID)
	}
	return nil
}

// SendAsync 异步发送（高吞吐，回调通知结果）。
//
// 适用场景：日志、监控指标等允许少量丢失的消息。
// 优势：不阻塞调用方，吞吐量比同步高 3-5 倍。
func (p *RocketMQProducer) SendAsync(ctx context.Context, tag, key string, body interface{}, callback func(error)) {
	if p.producer == nil {
		if callback != nil {
			callback(fmt.Errorf("rocketmq producer not initialized"))
		}
		return
	}

	data, err := json.Marshal(body)
	if err != nil {
		if callback != nil {
			callback(fmt.Errorf("marshal message: %w", err))
		}
		return
	}

	msg := &primitive.Message{
		Topic: p.topic,
		Body:  data,
	}
	msg.WithTag(tag)
	if key != "" {
		msg.WithKeys([]string{key})
	}

	err = p.producer.SendAsync(ctx, func(ctx context.Context, result *primitive.SendResult, err error) {
		if err != nil {
			p.mu.Lock()
			p.failedCount++
			p.mu.Unlock()
			log.Printf("[RocketMQ Producer] SendAsync 失败: topic=%s tag=%s err=%v",
				p.topic, tag, err)
		} else {
			p.mu.Lock()
			p.sentCount++
			p.mu.Unlock()
			if os.Getenv("MQ_DEBUG") == "true" {
				log.Printf("[RocketMQ Producer] SendAsync: topic=%s tag=%s key=%s msgID=%s",
					p.topic, tag, key, result.MsgID)
			}
		}
		if callback != nil {
			callback(err)
		}
	}, msg)

	if err != nil {
		p.mu.Lock()
		p.failedCount++
		p.mu.Unlock()
		if callback != nil {
			callback(fmt.Errorf("send async: %w", err))
		}
	}
}

// SendOneway 单向发送（最高吞吐，不等待 ACK）。
//
// 适用场景：监控指标、审计日志等对可靠性要求不高的消息。
// 特点：fire-and-forget，吞吐量最高但可能丢消息。
func (p *RocketMQProducer) SendOneway(ctx context.Context, tag, key string, body interface{}) {
	if p.producer == nil {
		return
	}

	data, err := json.Marshal(body)
	if err != nil {
		p.mu.Lock()
		p.failedCount++
		p.mu.Unlock()
		return
	}

	msg := &primitive.Message{
		Topic: p.topic,
		Body:  data,
	}
	msg.WithTag(tag)
	if key != "" {
		msg.WithKeys([]string{key})
	}

	if err := p.producer.SendOneWay(ctx, msg); err != nil {
		p.mu.Lock()
		p.failedCount++
		p.mu.Unlock()
		log.Printf("[RocketMQ Producer] SendOneway 失败: %v", err)
	} else {
		p.mu.Lock()
		p.sentCount++
		p.mu.Unlock()
	}
}

// SendDelayMessage 发送延迟消息。
//
// RocketMQ 原生支持 18 个延迟级别：
//
//	1=1s, 2=5s, 3=10s, 4=30s, 5=1m, 6=2m, 7=3m, 8=4m, 9=5m,
//	10=6m, 11=7m, 12=8m, 13=9m, 14=10m, 15=20m, 16=30m, 17=1h, 18=2h
//
// 面试考点：延迟消息的实现原理——Timer + 时间轮算法。
func (p *RocketMQProducer) SendDelayMessage(ctx context.Context, tag, key string, body interface{}, delayLevel int) {
	if p.producer == nil {
		return
	}

	data, err := json.Marshal(body)
	if err != nil {
		return
	}

	msg := &primitive.Message{
		Topic: p.topic,
		Body:  data,
	}
	msg.WithTag(tag)
	if key != "" {
		msg.WithKeys([]string{key})
	}
	msg.WithDelayTimeLevel(delayLevel)

	if err := p.producer.SendOneWay(ctx, msg); err != nil {
		p.mu.Lock()
		p.failedCount++
		p.mu.Unlock()
		log.Printf("[RocketMQ Producer] SendDelay 失败: %v", err)
	} else {
		p.mu.Lock()
		p.sentCount++
		p.mu.Unlock()
	}
}

// Stats 生产者统计。
type ProducerStats struct {
	SentCount   int64 `json:"sent_count"`
	FailedCount int64 `json:"failed_count"`
}

func (p *RocketMQProducer) Stats() ProducerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProducerStats{
		SentCount:   p.sentCount,
		FailedCount: p.failedCount,
	}
}

// Close 优雅关闭。
func (p *RocketMQProducer) Close() {
	if p.producer != nil {
		if err := p.producer.Shutdown(); err != nil {
			log.Printf("[RocketMQ Producer] 关闭失败: %v", err)
		} else {
			log.Printf("[RocketMQ Producer] 已关闭 (sent=%d, failed=%d)", p.sentCount, p.failedCount)
		}
	}
}

// ────────────────────────────────────────────────────────────
// RocketMQ Consumer（消费者）
// ────────────────────────────────────────────────────────────

// RocketMQConsumer RocketMQ 消费者。
//
// 面试考点：
//   - 集群消费（Clustering）：同一 Group 内只有一个实例消费每条消息
//   - 广播消费（Broadcasting）：每个实例都消费所有消息
//   - Push vs Pull：Push 模式由 Broker 推送（实时性高），Pull 模式消费者主动拉取（可控性强）
//   - Rebalance：消费者加入/离开时自动重平衡队列分配
//   - 并发消费 vs 顺序消费：并发消费多线程，顺序消费单线程保序
type RocketMQConsumer struct {
	nameSrvAddr string
	topic       string
	group       string
	handler     func(tag, key string, body []byte) error
	pushConsumer rocketmq.PushConsumer
}

// NewRocketMQConsumer 创建 RocketMQ 消费者。
func NewRocketMQConsumer(nameSrvAddr, topic, group string, handler func(tag, key string, body []byte) error) *RocketMQConsumer {
	return &RocketMQConsumer{
		nameSrvAddr: nameSrvAddr,
		topic:       topic,
		group:       group,
		handler:     handler,
	}
}

// Start 启动消费。
//
// 流程：
//  1. 向 NameServer 注册，获取 Broker 地址
//  2. 向 Broker 发送心跳，加入 Consumer Group
//  3. Rebalance 分配队列
//  4. 拉取消息 → 处理 → 提交 Offset
func (c *RocketMQConsumer) Start(ctx context.Context) error {
	log.Printf("[RocketMQ Consumer] 启动: topic=%s, group=%s", c.topic, c.group)

	c.pushConsumer, _ = rocketmq.NewPushConsumer(
		consumer.WithNameServer([]string{c.nameSrvAddr}),
		consumer.WithGroupName(c.group),
	)

	err := c.pushConsumer.Subscribe(c.topic, consumer.MessageSelector{},
		func(ctx context.Context, msgs ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
			for _, msg := range msgs {
				tag := msg.GetTags()
				key := msg.MsgId
				if err := c.handler(tag, key, msg.Body); err != nil {
					log.Printf("[RocketMQ Consumer] 处理失败: topic=%s tag=%s err=%v",
						c.topic, tag, err)
					return consumer.ConsumeRetryLater, nil
				}
			}
			return consumer.ConsumeSuccess, nil
		})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	if err := c.pushConsumer.Start(); err != nil {
		return fmt.Errorf("start consumer: %w", err)
	}

	<-ctx.Done()
	log.Printf("[RocketMQ Consumer] 停止: %s", c.group)
	return c.pushConsumer.Shutdown()
}

// Stop 停止消费。
func (c *RocketMQConsumer) Stop() {
	if c.pushConsumer != nil {
		c.pushConsumer.Shutdown()
	}
}
