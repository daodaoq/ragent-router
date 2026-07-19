// Package orchestrator 实现多模型编排——让多个 AI 模型协作完成复杂任务。
//
// 当前支持 review 模式：一个模型生成，另一个模型审查。
// 未来可扩展 best_of_n（N 选一）、merge（融合）等模式。
//
// # SSE 流式协议
//
// 编排的 SSE 流包含特殊的 phase_change 事件，标记阶段切换：
//
//	event: phase_change
//	data: {"phase":"generation","provider":"DeepSeek"}
//
//	[上游 SSE chunks...]
//
//	event: phase_change
//	data: {"phase":"review","provider":"Claude"}
//
//	[上游 SSE chunks...]
package orchestrator

import (
	"context"
	"net/http"
)

// Strategy 定义编排策略名称。
type Strategy string

const (
	// StrategyReview：模型A生成 → 模型B审查 → 返回审查后结果。
	StrategyReview Strategy = "review"
)

// Provider 是编排中使用的供应商信息（简化版，避免导入 proxy 包）。
type Provider struct {
	Name    string
	BaseURL string
	APIKey  string
	Model   string
	Headers map[string]string
}

// Request 是一次编排请求。
type Request struct {
	Generator *Provider       // 生成模型
	Reviewer  *Provider       // 审查模型
	Body      map[string]interface{} // Anthropic 格式请求体
	Strategy  Strategy
}

// Phase 表示编排中的一个阶段。
type Phase struct {
	Name     string `json:"phase"`     // "generation" | "review"
	Provider string `json:"provider"`  // 供应商名
}

// UpstreamCaller 是调用上游 AI API 的抽象。
// 编排引擎不直接依赖 proxy 包，通过此接口解耦。
type UpstreamCaller interface {
	// Call 调用上游 API 并以 SSE 流式写回。返回 Token 用量统计。
	Call(ctx context.Context, w http.ResponseWriter, provider *Provider, body map[string]interface{}) (*Usage, error)
}

// Usage 是单次上游调用的 Token 统计。
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CostUSD      float64
}
