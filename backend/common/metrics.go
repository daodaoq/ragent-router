package common

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ────────────────────────────────────────────────────────────
// 轻量级 Prometheus 指标采集器（不依赖 prometheus 客户端库）
//
// 面试考点：
//  1. Prometheus 四种指标类型？（Counter/Gauge/Histogram/Summary）
//  2. Pull vs Push 模型？（Prometheus 主动拉取 / Pushgateway 推送）
//  3. PromQL 查询？（rate/increase/histogram_quantile/avg）
//  4. 指标命名规范？（namespace_subsystem_name_unit）
//  5. 如何避免指标爆炸？（控制 label 基数，避免高基数 label）
// ────────────────────────────────────────────────────────────

// MetricsCollector Prometheus 指标采集器。
//
// 指标列表：
//   - ragent_requests_total (Counter): 总请求数（按 method/status/provider/model 分）
//   - ragent_request_duration_seconds (Histogram): 请求延迟分布
//   - ragent_request_errors_total (Counter): 错误请求数
//   - ragent_active_requests (Gauge): 当前活跃请求数
//   - ragent_tokens_total (Counter): 总 Token 用量（input/output 分开）
//   - ragent_cost_usd_total (Counter): 总费用
//   - ragent_cache_hits_total (Counter): 缓存命中次数
//   - ragent_circuit_breaker_state (Gauge): 熔断器状态（0=Closed,1=Open,2=HalfOpen）
//   - ragent_rate_limit_rejected_total (Counter): 限流拒绝次数
type MetricsCollector struct {
	mu sync.RWMutex

	// Counter 类指标
	requestsTotal   map[string]float64 // label → count
	errorsTotal     map[string]float64
	tokensTotal     map[string]float64
	costTotal       float64
	cacheHitsTotal  float64
	rateLimitReject float64

	// Gauge 类指标
	activeRequests float64
	breakerState   map[string]float64 // provider → state

	// Histogram 类指标
	durationBuckets []float64                // 桶边界
	durationCounts  map[string][]float64     // label → bucket counts
	durationSums    map[string]float64       // label → sum
	durationCountsN map[string]float64       // label → count
}

// NewMetricsCollector 创建指标采集器。
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		requestsTotal:   make(map[string]float64),
		errorsTotal:     make(map[string]float64),
		tokensTotal:     make(map[string]float64),
		breakerState:    make(map[string]float64),
		durationBuckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		durationCounts:  make(map[string][]float64),
		durationSums:    make(map[string]float64),
		durationCountsN: make(map[string]float64),
	}
}

// 全局指标采集器
var Metrics *MetricsCollector

// InitMetrics 初始化指标采集器。
func InitMetrics() {
	Metrics = NewMetricsCollector()
	log.Println("[Metrics] 指标采集器已初始化")
}

// ────────────────────────────────────────────────────────────
// 计数器操作
// ────────────────────────────────────────────────────────────

// IncrRequests 记录请求。
func (m *MetricsCollector) IncrRequests(method, status, provider, model string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("method=%s,status=%s,provider=%s,model=%s", method, status, provider, model)
	m.requestsTotal[key]++
}

// IncrErrors 记录错误。
func (m *MetricsCollector) IncrErrors(provider, model string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("provider=%s,model=%s", provider, model)
	m.errorsTotal[key]++
}

// AddTokens 累加 Token 用量。
func (m *MetricsCollector) AddTokens(inputTokens, outputTokens int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokensTotal["input"] += float64(inputTokens)
	m.tokensTotal["output"] += float64(outputTokens)
}

// AddCost 累加费用。
func (m *MetricsCollector) AddCost(cost float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.costTotal += cost
}

// IncrCacheHits 记录缓存命中。
func (m *MetricsCollector) IncrCacheHits() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cacheHitsTotal++
}

// IncrRateLimitReject 记录限流拒绝。
func (m *MetricsCollector) IncrRateLimitReject() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitReject++
}

// ────────────────────────────────────────────────────────────
// Gauge 操作
// ────────────────────────────────────────────────────────────

// IncrActiveRequests 增加活跃请求。
func (m *MetricsCollector) IncrActiveRequests() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests++
}

// DecrActiveRequests 减少活跃请求。
func (m *MetricsCollector) DecrActiveRequests() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests--
}

// SetBreakerState 设置熔断器状态。
func (m *MetricsCollector) SetBreakerState(provider string, state float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.breakerState[provider] = state
}

// ────────────────────────────────────────────────────────────
// Histogram 操作
// ────────────────────────────────────────────────────────────

// ObserveDuration 记录请求延迟。
func (m *MetricsCollector) ObserveDuration(provider, model string, duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("provider=%s,model=%s", provider, model)
	seconds := duration.Seconds()

	// 初始化桶计数
	if _, ok := m.durationCounts[key]; !ok {
		m.durationCounts[key] = make([]float64, len(m.durationBuckets)+1) // +1 for +Inf
	}

	// 找到对应的桶
	for i, bucket := range m.durationBuckets {
		if seconds <= bucket {
			m.durationCounts[key][i]++
		}
	}
	m.durationCounts[key][len(m.durationBuckets)]++ // +Inf 桶

	m.durationSums[key] += seconds
	m.durationCountsN[key]++
}

// ────────────────────────────────────────────────────────────
// Prometheus 文本格式输出
// ────────────────────────────────────────────────────────────

// ToPrometheusText 输出 Prometheus 文本格式。
//
// 格式参考：https://prometheus.io/docs/instrumenting/exposition_formats/
func (m *MetricsCollector) ToPrometheusText() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sb strings.Builder

	// Counter: requests_total
	sb.WriteString("# HELP ragent_requests_total Total number of requests\n")
	sb.WriteString("# TYPE ragent_requests_total counter\n")
	for labels, count := range m.requestsTotal {
		sb.WriteString(fmt.Sprintf("ragent_requests_total{%s} %g\n", labels, count))
	}

	// Counter: errors_total
	sb.WriteString("# HELP ragent_request_errors_total Total number of error requests\n")
	sb.WriteString("# TYPE ragent_request_errors_total counter\n")
	for labels, count := range m.errorsTotal {
		sb.WriteString(fmt.Sprintf("ragent_request_errors_total{%s} %g\n", labels, count))
	}

	// Gauge: active_requests
	sb.WriteString("# HELP ragent_active_requests Number of active requests\n")
	sb.WriteString("# TYPE ragent_active_requests gauge\n")
	sb.WriteString(fmt.Sprintf("ragent_active_requests %g\n", m.activeRequests))

	// Counter: tokens_total
	sb.WriteString("# HELP ragent_tokens_total Total tokens used\n")
	sb.WriteString("# TYPE ragent_tokens_total counter\n")
	for direction, count := range m.tokensTotal {
		sb.WriteString(fmt.Sprintf("ragent_tokens_total{direction=\"%s\"} %g\n", direction, count))
	}

	// Counter: cost_usd_total
	sb.WriteString("# HELP ragent_cost_usd_total Total cost in USD\n")
	sb.WriteString("# TYPE ragent_cost_usd_total counter\n")
	sb.WriteString(fmt.Sprintf("ragent_cost_usd_total %g\n", m.costTotal))

	// Counter: cache_hits_total
	sb.WriteString("# HELP ragent_cache_hits_total Total cache hits\n")
	sb.WriteString("# TYPE ragent_cache_hits_total counter\n")
	sb.WriteString(fmt.Sprintf("ragent_cache_hits_total %g\n", m.cacheHitsTotal))

	// Counter: rate_limit_rejected_total
	sb.WriteString("# HELP ragent_rate_limit_rejected_total Total rate limit rejections\n")
	sb.WriteString("# TYPE ragent_rate_limit_rejected_total counter\n")
	sb.WriteString(fmt.Sprintf("ragent_rate_limit_rejected_total %g\n", m.rateLimitReject))

	// Gauge: circuit_breaker_state
	sb.WriteString("# HELP ragent_circuit_breaker_state Circuit breaker state (0=Closed, 1=Open, 2=HalfOpen)\n")
	sb.WriteString("# TYPE ragent_circuit_breaker_state gauge\n")
	for provider, state := range m.breakerState {
		sb.WriteString(fmt.Sprintf("ragent_circuit_breaker_state{provider=\"%s\"} %g\n", provider, state))
	}

	// Histogram: request_duration_seconds
	sb.WriteString("# HELP ragent_request_duration_seconds Request duration in seconds\n")
	sb.WriteString("# TYPE ragent_request_duration_seconds histogram\n")
	for labels, counts := range m.durationCounts {
		cumulative := float64(0)
		for i, bucket := range m.durationBuckets {
			cumulative += counts[i]
			sb.WriteString(fmt.Sprintf("ragent_request_duration_seconds_bucket{%s,le=\"%g\"} %g\n", labels, bucket, cumulative))
		}
		cumulative += counts[len(m.durationBuckets)]
		sb.WriteString(fmt.Sprintf("ragent_request_duration_seconds_bucket{%s,le=\"+Inf\"} %g\n", labels, cumulative))
		sb.WriteString(fmt.Sprintf("ragent_request_duration_seconds_sum{%s} %g\n", labels, m.durationSums[labels]))
		sb.WriteString(fmt.Sprintf("ragent_request_duration_seconds_count{%s} %g\n", labels, m.durationCountsN[labels]))
	}

	return sb.String()
}

// ────────────────────────────────────────────────────────────
// 便捷函数
// ────────────────────────────────────────────────────────────

// RecordRequest 记录一个完整的请求指标。
func RecordRequest(method, status, provider, model string, duration time.Duration, tokensIn, tokensOut int, cost float64) {
	if Metrics == nil {
		return
	}
	Metrics.IncrRequests(method, status, provider, model)
	Metrics.ObserveDuration(provider, model, duration)
	Metrics.AddTokens(tokensIn, tokensOut)
	Metrics.AddCost(cost)
	if status == "error" {
		Metrics.IncrErrors(provider, model)
	}
}

