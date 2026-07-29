// Package routing 定义路由引擎的共享类型和接口。
//
// 实际实现位于 services/router/routing/（HybridRouter），
// 本包只提供跨服务共享的接口和 DTO。
package routing

import "context"

// ────────────────────────────────────────────────────────────
// 接口定义（由 services/router/routing 实现）
// ────────────────────────────────────────────────────────────

// EmbeddingService Embedding 向量生成接口。
type EmbeddingService interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// IntentClassifier 意图分类器接口。
type IntentClassifier interface {
	Classify(ctx context.Context, prompt string) (string, float64, error)
}

// RouteMatcher 路由匹配接口。
type RouteMatcher interface {
	Match(ctx context.Context, prompt string, model string) *MatchResult
}

// RouterEngine 路由引擎接口（api 服务通过此接口引用路由引擎）。
//
// 面试考点：
//   - 接口隔离原则：api 服务只需知道路由引擎的能力，不需要知道实现细节
//   - 实际实现通过 RPC 调用 router 服务，此处接口用于类型约束
type RouterEngine interface {
	// Classify 执行路由分类，返回匹配结果。
	Classify(ctx context.Context, prompt string) *ClassifyResult
	// Stats 返回路由统计。
	Stats() RouteStats
	// CacheSize 返回缓存大小。
	CacheSize() int
	// GetEmbeddingService 返回嵌入服务。
	GetEmbeddingService() EmbeddingService
}

// ────────────────────────────────────────────────────────────
// 数据传输对象（DTO）
// ────────────────────────────────────────────────────────────

// MatchResult 路由匹配结果。
type MatchResult struct {
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string
	Strategy     string
	Confidence   float64
	Reason       string
}

// SwitchResult 供应商切换结果。
type SwitchResult struct {
	Success      bool   `json:"success"`
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name"`
	Detail       string `json:"detail"`
	Fallback     bool   `json:"fallback,omitempty"`
}

// ClassifyResult 分类结果。
type ClassifyResult struct {
	Matched         *MatchResult         `json:"matched,omitempty"`
	DefaultProvider *DefaultProviderInfo `json:"default_provider"`
	Switched        *SwitchResult        `json:"switched,omitempty"`
}

// DefaultProviderInfo 默认供应商信息。
type DefaultProviderInfo struct {
	Name string `json:"name"`
}

// RouteStats 路由统计。
type RouteStats struct {
	KeywordHits    int64 `json:"keyword_hits"`
	EmbeddingHits  int64 `json:"embedding_hits"`
	ClassifierHits int64 `json:"classifier_hits"`
	FallbackHits   int64 `json:"fallback_hits"`
}
