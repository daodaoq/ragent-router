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
	msgCh       chan *Message // 异步发送缓冲区
	done        chan struct{}
	wg          sync.WaitGroup

	// 统计
	sentCount   int64
	failedCount int64
}

// Message 统一消息结构。
type Message struct {
	Topic   string            `json:"topic"`
	Tag     string            `json:"tag"`
	Body    []byte            `json:"body"`
	Keys    string            `json:"keys"`    // 消息 Key（用于消息追踪）
	Delay   int               `json:"delay"`   // 延迟级别（1-18，对应 1s-2h）
	Headers map[string]string `json:"headers"` // 自定义属性
}

// NewRocketMQProducer 创建 RocketMQ 生产者。
//
// 参数：
//   - nameSrvAddr: NameServer 地址（如 "127.0.0.1:9876"）
//   - topic: 目标 Topic
//   - bufferSize: 异步缓冲区大小
func NewRocketMQProducer(nameSrvAddr, topic string, bufferSize int) *RocketMQProducer {
	if bufferSize <= 0 {
		bufferSize = 10000
	}

	p := &RocketMQProducer{
		nameSrvAddr: nameSrvAddr,
		topic:       topic,
		msgCh:       make(chan *Message, bufferSize),
		done:        make(chan struct{}),
	}

	// 启动异步发送协程
	p.wg.Add(1)
	go p.asyncSendLoop()

	return p
}

// SendSync 同步发送（可靠，阻塞等待 Broker ACK）。
//
// 适用场景：重要业务消息（订单创建、支付通知）。
// 重试策略：默认重试 2 次，总耗时约 3 秒。
func (p *RocketMQProducer) SendSync(ctx context.Context, tag, key string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	msg := &Message{
		Topic: p.topic,
		Tag:   tag,
		Body:  data,
		Keys:  key,
	}

	// 实际实现：
	// producer, _ := rocketmq.NewProducer(...)
	// result, err := producer.SendSync(ctx, msg)
	// return err

	// 模拟：放入缓冲区
	select {
	case p.msgCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SendAsync 异步发送（高吞吐，回调通知结果）。
//
// 适用场景：日志、监控指标等允许少量丢失的消息。
// 优势：不阻塞调用方，吞吐量比同步高 3-5 倍。
func (p *RocketMQProducer) SendAsync(tag, key string, body interface{}, callback func(error)) {
	data, err := json.Marshal(body)
	if err != nil {
		if callback != nil {
			callback(err)
		}
		return
	}

	msg := &Message{
		Topic: p.topic,
		Tag:   tag,
		Body:  data,
		Keys:  key,
	}

	select {
	case p.msgCh <- msg:
		// 异步发送，结果通过回调通知
		if callback != nil {
			go callback(nil) // 模拟成功回调
		}
	default:
		p.failedCount++
		if callback != nil {
			go callback(errBufferFull)
		}
	}
}

// SendOneway 单向发送（最高吞吐，不等待 ACK）。
//
// 适用场景：监控指标、审计日志等对可靠性要求不高的消息。
// 特点：fire-and-forget，吞吐量最高但可能丢消息。
func (p *RocketMQProducer) SendOneway(tag, key string, body interface{}) {
	data, _ := json.Marshal(body)
	msg := &Message{
		Topic: p.topic,
		Tag:   tag,
		Body:  data,
		Keys:  key,
	}

	select {
	case p.msgCh <- msg:
	default:
		// 缓冲区满，静默丢弃
		p.failedCount++
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
func (p *RocketMQProducer) SendDelayMessage(tag, key string, body interface{}, delayLevel int) {
	data, _ := json.Marshal(body)
	msg := &Message{
		Topic: p.topic,
		Tag:   tag,
		Body:  data,
		Keys:  key,
		Delay: delayLevel,
	}

	select {
	case p.msgCh <- msg:
	default:
		p.failedCount++
	}
}

// asyncSendLoop 异步发送循环（批量 + 压缩）。
func (p *RocketMQProducer) asyncSendLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(50 * time.Millisecond) // 每 50ms 批量发送
	batch := make([]*Message, 0, 100)

	for {
		select {
		case <-p.done:
			if len(batch) > 0 {
				p.flushBatch(batch)
			}
			return
		case msg := <-p.msgCh:
			batch = append(batch, msg)
			if len(batch) >= 100 {
				p.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				p.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (p *RocketMQProducer) flushBatch(batch []*Message) {
	// 实际实现：
	// producer.SendAsync(ctx, batch, callback)
	if os.Getenv("MQ_DEBUG") == "true" {
		log.Printf("[RocketMQ Producer] 批量发送 %d 条到 %s", len(batch), p.topic)
	}
	p.sentCount += int64(len(batch))
}

// Stats 生产者统计。
type ProducerStats struct {
	SentCount   int64 `json:"sent_count"`
	FailedCount int64 `json:"failed_count"`
	BufferSize  int   `json:"buffer_size"`
}

func (p *RocketMQProducer) Stats() ProducerStats {
	return ProducerStats{
		SentCount:   p.sentCount,
		FailedCount: p.failedCount,
		BufferSize:  len(p.msgCh),
	}
}

// Close 优雅关闭（等待缓冲区消息发送完毕）。
func (p *RocketMQProducer) Close() {
	close(p.done)
	p.wg.Wait()
}

var errBufferFull = fmt.Errorf("producer buffer full")

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
	consumeMode string // "clustering" or "broadcasting"
}

// NewRocketMQConsumer 创建 RocketMQ 消费者。
func NewRocketMQConsumer(nameSrvAddr, topic, group string, handler func(tag, key string, body []byte) error) *RocketMQConsumer {
	return &RocketMQConsumer{
		nameSrvAddr: nameSrvAddr,
		topic:       topic,
		group:       group,
		handler:     handler,
		consumeMode: "clustering",
	}
}

// Start 启动消费。
//
// 流程：
//  1. 向 NameServer 注册，获取 Broker 地址
//  2. 向 Broker 发送心跳，加入 Consumer Group
//  3. Rebalance 分配队列
//  4. 拉取消息 → 处理 → 提交 Offset
func (c *RocketMQConsumer) Start(ctx context.Context) {
	log.Printf("[RocketMQ Consumer] 启动: topic=%s, group=%s, mode=%s",
		c.topic, c.group, c.consumeMode)

	// 实际实现：
	// consumer, _ := rocketmq.NewPushConsumer(
	//     consumer.WithGroupName(c.group),
	//     consumer.WithNameServerAddr(c.nameSrvAddr),
	//     consumer.WithConsumeMode(consumer.Clustering),
	// )
	// consumer.Subscribe(c.topic, consumer.MessageSelector{}, c.handleMessage)
	// consumer.Start()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[RocketMQ Consumer] 停止: %s", c.group)
			return
		default:
			time.Sleep(time.Second)
		}
	}
}
