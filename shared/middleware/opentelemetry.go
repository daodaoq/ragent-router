package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ────────────────────────────────────────────────────────────
// OpenTelemetry 链路追踪（轻量级实现）
//
// 面试考点：
//  1. Trace：一次完整请求的调用链（如 HTTP→路由→代理→上游）
//  2. Span：调用链中的一个操作（如一次 RPC 调用、一次 DB 查询）
//  3. TraceContext 传播：通过 HTTP Header（traceparent / x-trace-id）跨服务透传
//  4. 采样策略：全量采样（调试）/ 概率采样（生产）/ 尾部采样（出错时全采）
//  5. OTel SDK：TracerProvider → Tracer → Span → Exporter（Jaeger/Zipkin）
// ────────────────────────────────────────────────────────────

const (
	HeaderTraceID      = "X-Trace-ID"
	HeaderSpanID       = "X-Span-ID"
	HeaderParentSpanID = "X-Parent-Span-ID"
	ctxKeyTraceID      = "trace_id"
	ctxKeySpanID       = "span_id"
)

// TraceContext 链路追踪上下文。
type TraceContext struct {
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	ServiceName  string `json:"service_name"`
	StartTime    time.Time
	EndTime      time.Time
	Attributes   map[string]string
}

// NewTraceContext 创建新的追踪上下文。
func NewTraceContext(serviceName string) *TraceContext {
	return &TraceContext{
		TraceID:     uuid.New().String(),
		SpanID:      uuid.New().String()[:8],
		ServiceName: serviceName,
		StartTime:   time.Now(),
		Attributes:  make(map[string]string),
	}
}

// ChildSpan 创建子 Span（用于跨服务调用）。
func (t *TraceContext) ChildSpan(serviceName string) *TraceContext {
	return &TraceContext{
		TraceID:      t.TraceID,
		SpanID:       uuid.New().String()[:8],
		ParentSpanID: t.SpanID,
		ServiceName:  serviceName,
		StartTime:    time.Now(),
		Attributes:   make(map[string]string),
	}
}

// Finish 结束 Span 并记录耗时。
func (t *TraceContext) Finish() {
	t.EndTime = time.Now()
	duration := t.EndTime.Sub(t.StartTime)
	log.Printf("[Trace] %s→%s | %s | %s | %v",
		t.TraceID[:8], t.SpanID, t.ServiceName,
		t.Attributes["method"], duration.Round(time.Millisecond))
}

// SetAttribute 设置 Span 属性。
func (t *TraceContext) SetAttribute(key, value string) {
	t.Attributes[key] = value
}

// TraceMiddleware Gin 链路追踪中间件。
//
// 功能：
//  1. 从请求头提取 TraceID（没有则生成新的）
//  2. 生成 SpanID
//  3. 注入到 Context
//  4. 写入响应头
func TraceMiddleware(serviceName string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			traceID := r.Header.Get(HeaderTraceID)
			if traceID == "" {
				traceID = uuid.New().String()
			}

			spanID := uuid.New().String()[:8]
			parentSpanID := r.Header.Get(HeaderSpanID)

			ctx := &TraceContext{
				TraceID:      traceID,
				SpanID:       spanID,
				ParentSpanID: parentSpanID,
				ServiceName:  serviceName,
				StartTime:    time.Now(),
				Attributes: map[string]string{
					"method": r.Method,
					"path":   r.URL.Path,
				},
			}

			// 写入响应头
			w.Header().Set(HeaderTraceID, traceID)
			w.Header().Set(HeaderSpanID, spanID)

			// 注入到 request context
			r = r.WithContext(context.WithValue(r.Context(), ctxKeyTraceID, ctx))
			next(w, r)

			ctx.Finish()
		}
	}
}

// InjectTraceToRequest 将追踪信息注入到 HTTP 请求头。
func InjectTraceToRequest(req *http.Request, trace *TraceContext) {
	req.Header.Set(HeaderTraceID, trace.TraceID)
	req.Header.Set(HeaderSpanID, trace.SpanID)
	if trace.ParentSpanID != "" {
		req.Header.Set(HeaderParentSpanID, trace.ParentSpanID)
	}
}

// ExtractTraceFromContext 从 Context 提取追踪信息。
func ExtractTraceFromContext(ctx context.Context) *TraceContext {
	if tc, ok := ctx.Value(ctxKeyTraceID).(*TraceContext); ok {
		return tc
	}
	return NewTraceContext("unknown")
}

// FormatTraceID 格式化 TraceID 用于日志。
func FormatTraceID(traceID string) string {
	if len(traceID) > 8 {
		return fmt.Sprintf("trace=%s..%s", traceID[:4], traceID[len(traceID)-4:])
	}
	return traceID
}
