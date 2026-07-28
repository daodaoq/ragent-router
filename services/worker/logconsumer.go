// Package worker 提供异步日志消费者。
//
// 高并发方案 4 的消费者端：
//   - 从 Redis Streams 消费请求日志
//   - 写入 MySQL 持久化
//   - 写入 Elasticsearch 全文检索
//   - 更新 Prometheus 指标
//
// 面试考点：
//   - 为什么要异步？同步写 DB 会增加请求延迟 5-50ms
//   - Consumer Group 保证每条消息只被一个 worker 处理
//   - XACK 确认机制防止消息丢失
//   - Pending List 重试机制处理消费失败的消息
package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/ragent/router/shared/redis"
	goRedis "github.com/redis/go-redis/v9"
)

// LogConsumerWorker 日志消费者 Worker。
type LogConsumerWorker struct {
	consumer *redis.StreamConsumer
}

// NewLogConsumerWorker 创建日志消费者。
func NewLogConsumerWorker(consumerName string) *LogConsumerWorker {
	w := &LogConsumerWorker{}
	w.consumer = redis.NewStreamConsumer(
		redis.StreamRequestLog,
		redis.GroupLogConsumer,
		consumerName,
		w.handleMessage,
	)
	return w
}

// Start 启动消费。
func (w *LogConsumerWorker) Start(ctx context.Context) {
	log.Println("[日志消费者] 启动")

	// 启动主消费循环
	go w.consumer.Start(ctx)

	// 启动 Pending 消息恢复（处理消费者崩溃遗留的消息）
	go w.recoverPending(ctx)
}

// handleMessage 处理单条日志消息。
func (w *LogConsumerWorker) handleMessage(msg goRedis.XMessage) error {
	data, ok := msg.Values["data"].(string)
	if !ok {
		return nil
	}

	var entry redis.RequestLogEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		log.Printf("[日志消费者] JSON 解析失败: %v", err)
		return err
	}

	// ── 写入 MySQL ──
	if err := w.writeToDB(entry); err != nil {
		log.Printf("[日志消费者] DB 写入失败: %v", err)
		return err
	}

	// ── 写入 Elasticsearch ──
	if err := w.writeToES(entry); err != nil {
		log.Printf("[日志消费者] ES 写入失败: %v", err)
		// ES 失败不阻塞，继续处理
	}

	// ── 更新 Prometheus 指标 ──
	w.updateMetrics(entry)

	return nil
}

func (w *LogConsumerWorker) writeToDB(entry redis.RequestLogEntry) error {
	// 实际实现：调用 GORM 写入 request_logs 表
	log.Printf("[日志消费者] DB: provider=%s, model=%s, latency=%dms",
		entry.Provider, entry.Model, entry.LatencyMs)
	return nil
}

func (w *LogConsumerWorker) writeToES(entry redis.RequestLogEntry) error {
	// 实际实现：调用 ES client IndexDocument
	return nil
}

func (w *LogConsumerWorker) updateMetrics(entry redis.RequestLogEntry) {
	// 实际实现：更新 Prometheus counter/histogram
}

// recoverPending 定期恢复 Pending 消息。
//
// 当消费者崩溃时，它未 ACK 的消息会留在 Pending List。
// 其他消费者通过 XCLAIM 接管这些消息。
func (w *LogConsumerWorker) recoverPending(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 接管超过 60 秒未确认的消息
			if err := w.consumer.ClaimPendingMessages(ctx, 60*time.Second); err != nil {
				log.Printf("[日志消费者] Pending 恢复失败: %v", err)
			}
		}
	}
}
