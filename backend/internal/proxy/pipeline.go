package proxy

import (
	"context"
	"log"
	"net/http"
)

// Pipeline 是请求处理中间件管线。
// 在代理请求流转过程中插入自定义逻辑：
//   - ProcessRequest：路由之前执行（可修改 prompt、添加 metadata、拦截请求）
//   - ProcessResponse：响应返回给客户端之前执行（可修改响应、添加审计日志）
//
// 执行顺序按注册顺序，前一个中间件可中断后续执行（返回 error）。
type Pipeline struct {
	middlewares []Middleware
}

// Middleware 是管线中的单个处理单元。
// 接口设计参考 HTTP middleware 模式：每个中间件包装下一个。
type Middleware interface {
	// Name 返回中间件名称（用于日志和调试）。
	Name() string

	// ProcessRequest 在路由/上游调用之前执行。
	// 返回 error 可中断请求处理（如拦截恶意请求）。
	ProcessRequest(ctx context.Context, body map[string]interface{}, headers http.Header) (map[string]interface{}, error)

	// ProcessResponse 在响应返回给客户端之前执行（可选）。
	// SSE 流式场景下此方法在流结束前调用，可注入额外事件。
	ProcessResponse(ctx context.Context, w http.ResponseWriter, tracking *RequestTracking) error
}

// NewPipeline 创建中间件管线。
func NewPipeline(middlewares ...Middleware) *Pipeline {
	return &Pipeline{middlewares: middlewares}
}

// Add 注册新中间件（运行时添加）。
func (p *Pipeline) Add(m Middleware) {
	p.middlewares = append(p.middlewares, m)
}

// Len 返回已注册的中间件数量。
func (p *Pipeline) Len() int {
	return len(p.middlewares)
}

// ExecuteRequest 执行请求阶段的所有中间件。
// 返回修改后的请求体（中间件可注入额外字段）。
func (p *Pipeline) ExecuteRequest(ctx context.Context, body map[string]interface{}, headers http.Header) (map[string]interface{}, error) {
	result := body
	for _, m := range p.middlewares {
		modified, err := m.ProcessRequest(ctx, result, headers)
		if err != nil {
			log.Printf("[管线] 中间件 %s 中断请求: %v", m.Name(), err)
			return nil, err
		}
		result = modified
	}
	return result, nil
}

// ExecuteResponse 执行响应阶段的所有中间件。
func (p *Pipeline) ExecuteResponse(ctx context.Context, w http.ResponseWriter, tracking *RequestTracking) {
	for _, m := range p.middlewares {
		if err := m.ProcessResponse(ctx, w, tracking); err != nil {
			log.Printf("[管线] 中间件 %s 响应处理错误: %v", m.Name(), err)
		}
	}
}
