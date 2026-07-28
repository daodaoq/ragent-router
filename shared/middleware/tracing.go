package middleware

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ────────────────────────────────────────────────────────────
// 分布式链路追踪（轻量级实现）
//
// 面试考点：
//  1. OpenTelemetry 是什么？（统一的可观测性框架：Traces + Metrics + Logs）
//  2. TraceID vs SpanID？（TraceID 标识整条链路，SpanID 标识单个操作）
//  3. 如何跨服务透传？（HTTP Header: traceparent / x-trace-id）
//  4. 采样策略？（全量采样/概率采样/尾部采样）
//  5. 与日志的关系？（TraceID 关联日志，实现链路级日志查询）
// ────────────────────────────────────────────────────────────

const (
	// Header 名称
	HeaderTraceID      = "X-Trace-ID"
	HeaderSpanID       = "X-Span-ID"
	HeaderParentSpanID = "X-Parent-Span-ID"

	// Context key
	ctxKeyTraceID = "trace_id"
	ctxKeySpanID  = "span_id"
)

// TraceContext 链路追踪上下文。
type TraceContext struct {
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
}

// NewTraceContext 创建新的追踪上下文。
func NewTraceContext() *TraceContext {
	return &TraceContext{
		TraceID: uuid.New().String(),
		SpanID:  uuid.New().String()[:8],
	}
}

// ChildSpan 创建子 Span。
func (t *TraceContext) ChildSpan() *TraceContext {
	return &TraceContext{
		TraceID:      t.TraceID,
		SpanID:       uuid.New().String()[:8],
		ParentSpanID: t.SpanID,
	}
}

// TraceMiddleware Gin 链路追踪中间件。
//
// 功能：
//  1. 从请求头提取 TraceID（没有则生成新的）
//  2. 生成 SpanID
//  3. 注入到 Gin Context
//  4. 写入响应头
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(HeaderTraceID)
		if traceID == "" {
			traceID = uuid.New().String()
		}

		spanID := uuid.New().String()[:8]
		_ = c.GetHeader(HeaderSpanID) // 提取父 SpanID（当前未使用，保留接口）

		// 注入到 Context
		c.Set(ctxKeyTraceID, traceID)
		c.Set(ctxKeySpanID, spanID)

		// 写入响应头
		c.Header(HeaderTraceID, traceID)
		c.Header(HeaderSpanID, spanID)

		c.Next()
	}
}

// GetTraceID 从 Gin Context 获取 TraceID。
func GetTraceID(c *gin.Context) string {
	if id, ok := c.Get(ctxKeyTraceID); ok {
		return id.(string)
	}
	return ""
}

// GetSpanID 从 Gin Context 获取 SpanID。
func GetSpanID(c *gin.Context) string {
	if id, ok := c.Get(ctxKeySpanID); ok {
		return id.(string)
	}
	return ""
}

// InjectTraceToRequest 将追踪信息注入到 HTTP 请求头。
func InjectTraceToRequest(req *http.Request, trace *TraceContext) {
	req.Header.Set(HeaderTraceID, trace.TraceID)
	req.Header.Set(HeaderSpanID, trace.SpanID)
	if trace.ParentSpanID != "" {
		req.Header.Set(HeaderParentSpanID, trace.ParentSpanID)
	}
}

// ExtractTraceFromRequest 从 HTTP 请求头提取追踪信息。
func ExtractTraceFromRequest(req *http.Request) *TraceContext {
	return &TraceContext{
		TraceID:      req.Header.Get(HeaderTraceID),
		SpanID:       req.Header.Get(HeaderSpanID),
		ParentSpanID: req.Header.Get(HeaderParentSpanID),
	}
}

// TraceFromContext 从标准 context.Context 提取追踪信息。
func TraceFromContext(ctx context.Context) *TraceContext {
	if traceID, ok := ctx.Value(ctxKeyTraceID).(string); ok {
		return &TraceContext{
			TraceID: traceID,
			SpanID:  uuid.New().String()[:8],
		}
	}
	return NewTraceContext()
}

// ContextWithTrace 将追踪信息注入到标准 context.Context。
func ContextWithTrace(ctx context.Context, trace *TraceContext) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTraceID, trace.TraceID)
	ctx = context.WithValue(ctx, ctxKeySpanID, trace.SpanID)
	return ctx
}

// InitTracing 初始化链路追踪。
func InitTracing() {
	log.Println("[Tracing] 链路追踪已启用（基于 Header 透传）")
}
