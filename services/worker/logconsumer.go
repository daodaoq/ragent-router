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
	"sync/atomic"
	"time"

	"github.com/ragent/router/shared/redis"
	"github.com/ragent/router/shared/store"
	goRedis "github.com/redis/go-redis/v9"
)

// ────────────────────────────────────────────────────────────
// Worker 指标（Prometheus 兼容）
// ────────────────────────────────────────────────────────────

// workerMetrics 内部计数器（线程安全，无外部依赖）。
//
// 面试考点：
//   - 使用 atomic 而非 Mutex，因为计数器场景只需原子操作
//   - 计数器可在 /metrics 端点暴露为 Prometheus 格式
type workerMetrics struct {
	consumedTotal  atomic.Int64 // 已消费消息总数
	dbWriteOK      atomic.Int64 // DB 写入成功数
	dbWriteFail    atomic.Int64 // DB 写入失败数
	esWriteOK      atomic.Int64 // ES 写入成功数
	esWriteFail    atomic.Int64 // ES 写入失败数
	latencySumMs   atomic.Int64 // 延迟累计值（用于计算平均值）
}

// Snapshot 指标快照。
type MetricSnapshot struct {
	ConsumedTotal int64 `json:"consumed_total"`
	DBWriteOK     int64 `json:"db_write_ok"`
	DBWriteFail   int64 `json:"db_write_fail"`
	ESWriteOK     int64 `json:"es_write_ok"`
	ESWriteFail   int64 `json:"es_write_fail"`
	AvgLatencyMs  int64 `json:"avg_latency_ms"`
}

// ────────────────────────────────────────────────────────────
// LogConsumerWorker
// ────────────────────────────────────────────────────────────

// LogConsumerWorker 日志消费者 Worker。
type LogConsumerWorker struct {
	consumer *redis.StreamConsumer
	mysql    *store.MySQLLogStore  // 可选，为 nil 时跳过 DB 写入
	es       *store.ESLogStore     // 可选，为 nil 时跳过 ES 写入
	metrics  workerMetrics
}

// WorkerConfig Worker 配置。
type WorkerConfig struct {
	ConsumerName string            // 消费者名称（用于 Consumer Group 内区分）
	MySQL        *store.MySQLLogStore // MySQL 存储（可选）
	ES           *store.ESLogStore    // ES 存储（可选）
}

// NewLogConsumerWorker 创建日志消费者。
func NewLogConsumerWorker(cfg WorkerConfig) *LogConsumerWorker {
	w := &LogConsumerWorker{
		mysql: cfg.MySQL,
		es:    cfg.ES,
	}
	w.consumer = redis.NewStreamConsumer(
		redis.StreamRequestLog,
		redis.GroupLogConsumer,
		cfg.ConsumerName,
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
	w.metrics.consumedTotal.Add(1)

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
	if err := w.writeToDB(msg.ID, entry); err != nil {
		log.Printf("[日志消费者] DB 写入失败: %v", err)
		return err
	}

	// ── 写入 Elasticsearch ──
	if err := w.writeToES(msg.ID, entry); err != nil {
		log.Printf("[日志消费者] ES 写入失败: %v", err)
		// ES 失败不阻塞，继续处理
	}

	// ── 更新指标 ──
	w.updateMetrics(entry)

	return nil
}

// writeToDB 写入 MySQL（使用 GORM）。
//
// 面试考点：
//   - 使用 GORM 的 Create 而非原生 SQL，自动处理字段映射和零值
//   - 生产环境应使用批量写入（InsertBatch）减少 DB 压力
//   - 失败时返回 error 触发消息重试（不 ACK → 留在 Pending List）
func (w *LogConsumerWorker) writeToDB(msgID string, entry redis.RequestLogEntry) error {
	if w.mysql == nil {
		// MySQL 未配置，仅打印日志
		log.Printf("[日志消费者] DB(skip): provider=%s, model=%s, latency=%dms",
			entry.Provider, entry.Model, entry.LatencyMs)
		return nil
	}

	record := &store.MySQLRequestLogRecord{
		ID:               msgID, // 使用 Redis Stream 消息 ID 作为唯一标识
		Model:            entry.Model,
		Provider:         entry.Provider,
		LatencyMs:        entry.LatencyMs,
		PromptTokens:     entry.PromptTokens,
		CompletionTokens: entry.CompletionTokens,
		TotalTokens:      entry.TotalTokens,
		CostUSD:          entry.CostUSD,
		CreatedAt:        time.UnixMilli(entry.Timestamp),
	}

	if entry.Status == "success" || entry.StatusCode >= 200 && entry.StatusCode < 300 {
		record.Status = "ok"
	} else {
		record.Status = "error"
	}

	if err := w.mysql.Insert(record); err != nil {
		w.metrics.dbWriteFail.Add(1)
		return err
	}
	w.metrics.dbWriteOK.Add(1)
	return nil
}

// writeToES 写入 Elasticsearch（全文检索）。
//
// 面试考点：
//   - ES 用于按 prompt 全文搜索，MySQL 做结构化查询，各司其职
//   - 使用 Bulk API 批量写入提升吞吐（此处为单条，生产环境应攒批）
//   - ES 写入失败不影响主流程，降级为仅 MySQL 存储
func (w *LogConsumerWorker) writeToES(msgID string, entry redis.RequestLogEntry) error {
	if w.es == nil {
		return nil
	}

	status := "ok"
	if entry.StatusCode >= 400 {
		status = "error"
	}

	doc := &store.ESDocument{
		ID:               msgID,
		PromptTokens:     entry.PromptTokens,
		CompletionTokens: entry.CompletionTokens,
		TotalTokens:      entry.TotalTokens,
		Model:            entry.Model,
		Provider:         entry.Provider,
		Status:           status,
		LatencyMs:        entry.LatencyMs,
		CostUSD:          entry.CostUSD,
	}

	if err := w.es.Index(context.Background(), doc); err != nil {
		w.metrics.esWriteFail.Add(1)
		return err
	}
	w.metrics.esWriteOK.Add(1)
	return nil
}

// updateMetrics 更新内部计数器。
//
// 面试考点：
//   - 原子操作（atomic）比 Mutex 轻量，适合纯计数场景
//   - 平均延迟 = latencySumMs / consumedTotal（无锁近似计算）
//   - 指标可通过 /metrics 端点暴露为 Prometheus text format
func (w *LogConsumerWorker) updateMetrics(entry redis.RequestLogEntry) {
	w.metrics.latencySumMs.Add(entry.LatencyMs)
}

// Metrics 返回当前指标快照。
func (w *LogConsumerWorker) Metrics() MetricSnapshot {
	consumed := w.metrics.consumedTotal.Load()
	latencySum := w.metrics.latencySumMs.Load()
	avgLatency := int64(0)
	if consumed > 0 {
		avgLatency = latencySum / consumed
	}
	return MetricSnapshot{
		ConsumedTotal: consumed,
		DBWriteOK:     w.metrics.dbWriteOK.Load(),
		DBWriteFail:   w.metrics.dbWriteFail.Load(),
		ESWriteOK:     w.metrics.esWriteOK.Load(),
		ESWriteFail:   w.metrics.esWriteFail.Load(),
		AvgLatencyMs:  avgLatency,
	}
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
