package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ────────────────────────────────────────────────────────────
// Prometheus 指标采集（轻量级实现，不依赖 prometheus 客户端库）
//
// 面试考点：
//  1. 四种指标类型：Counter（只增）、Gauge（可增减）、Histogram（分位数）、Summary（滑动窗口）
//  2. 标准格式：metric_name{label="value"} float_value timestamp
//  3. Pull 模型：Prometheus 定期来拉 /metrics 端点
//  4. 常用指标：QPS、延迟 P50/P99、错误率、活跃连接数
// ────────────────────────────────────────────────────────────

var (
	// 计数器
	requestTotal    = newCounterVec("http_requests_total", "总请求数", "method", "path", "status")
	requestDuration = newHistogramVec("http_request_duration_seconds", "请求延迟", []float64{0.01, 0.05, 0.1, 0.5, 1, 5}, "method", "path")

	// 仪表盘
	activeRequests = newGauge("http_active_requests", "当前活跃请求数")
)

// MetricsMiddleware HTTP 指标采集中间件。
//
// 自动采集：
//   - http_requests_total{method, path, status} — 请求计数
//   - http_request_duration_seconds{method, path} — 延迟直方图
//   - http_active_requests — 活跃请求数
func MetricsMiddleware() func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			activeRequests.Inc()
			defer activeRequests.Dec()

			// 包装 ResponseWriter 以捕获状态码
			rw := &responseWriter{ResponseWriter: w, statusCode: 200}
			next(rw, r)

			duration := time.Since(start).Seconds()
			status := strconv.Itoa(rw.statusCode)

			requestTotal.Inc(r.Method, r.URL.Path, status)
			requestDuration.Observe(duration, r.Method, r.URL.Path)
		}
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// MetricsHandler 返回 /metrics 端点的 Handler。
//
// 输出格式：Prometheus 文本格式（标准 exposition format）。
func MetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(requestTotal.Metrics()))
		w.Write([]byte(requestDuration.Metrics()))
		w.Write([]byte(activeRequests.Metrics()))
	}
}

// ────────────────────────────────────────────────────────────
// 指标实现
// ────────────────────────────────────────────────────────────

// counterVec 带标签的计数器。
type counterVec struct {
	name   string
	help   string
	labels []string
	data   map[string]int64
}

func newCounterVec(name, help string, labels ...string) *counterVec {
	return &counterVec{name: name, help: help, labels: labels, data: make(map[string]int64)}
}

func (c *counterVec) Inc(labelValues ...string) {
	key := joinLabels(labelValues)
	c.data[key]++
}

func (c *counterVec) Metrics() string {
	s := fmt.Sprintf("# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	for k, v := range c.data {
		s += fmt.Sprintf("%s{%s} %d\n", c.name, k, v)
	}
	return s
}

// histogramVec 带标签的直方图。
type histogramVec struct {
	name    string
	help    string
	buckets []float64
	labels  []string
	data    map[string][]float64
}

func newHistogramVec(name string, help string, buckets []float64, labels ...string) *histogramVec {
	return &histogramVec{name: name, help: help, buckets: buckets, labels: labels, data: make(map[string][]float64)}
}

func (h *histogramVec) Observe(value float64, labelValues ...string) {
	key := joinLabels(labelValues)
	h.data[key] = append(h.data[key], value)
}

func (h *histogramVec) Metrics() string {
	s := fmt.Sprintf("# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	for k, values := range h.data {
		var sum float64
		for _, v := range values {
			sum += v
			for _, bucket := range h.buckets {
				if v <= bucket {
					s += fmt.Sprintf("%s_bucket{%s,le=\"%g\"} %d\n", h.name, k, bucket, len(values))
				}
			}
		}
		s += fmt.Sprintf("%s_bucket{%s,le=\"+Inf\"} %d\n", h.name, k, len(values))
		s += fmt.Sprintf("%s_sum{%s} %g\n", h.name, k, sum)
		s += fmt.Sprintf("%s_count{%s} %d\n", h.name, k, len(values))
	}
	return s
}

// gauge 仪表盘。
type gauge struct {
	name  string
	help  string
	value int64
}

func newGauge(name, help string) *gauge {
	return &gauge{name: name, help: help}
}

func (g *gauge) Inc()    { g.value++ }
func (g *gauge) Dec()    { g.value-- }
func (g *gauge) Set(v int64) { g.value = v }

func (g *gauge) Metrics() string {
	return fmt.Sprintf("# HELP %s %s\n# TYPE %s gauge\n%s %d\n", g.name, g.help, g.name, g.name, g.value)
}

func joinLabels(values []string) string {
	s := ""
	for i, v := range values {
		if i > 0 {
			s += ","
		}
		// 简化：直接用 "label_name="value"" 格式
		s += fmt.Sprintf("l%d=\"%s\"", i, v)
	}
	return s
}
