package routing

import "context"

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

// MatchResult 路由匹配结果。
type MatchResult struct {
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string
	Strategy     string
	Confidence   float64
	Reason       string
}

// HybridRouter 三级路由引擎（类型占位，实际实现在 services/router/routing）。
// api 服务通过此类型引用路由引擎，具体实现通过 RPC 调用。
type HybridRouter struct{}

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
	Matched         *MatchResult        `json:"matched,omitempty"`
	DefaultProvider *DefaultProviderInfo `json:"default_provider"`
	Switched        *SwitchResult       `json:"switched,omitempty"`
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

// CacheSize 返回缓存大小（占位实现）。
func (r *HybridRouter) CacheSize() int { return 0 }

// Stats 返回路由统计（占位实现）。
func (r *HybridRouter) Stats() RouteStats { return RouteStats{} }

// Classify 执行分类（占位实现）。
func (r *HybridRouter) Classify(ctx context.Context, prompt string) *ClassifyResult {
	return &ClassifyResult{
		DefaultProvider: &DefaultProviderInfo{Name: "DeepSeek"},
	}
}

// GetEmbeddingService 返回嵌入服务（占位实现）。
func (r *HybridRouter) GetEmbeddingService() EmbeddingService { return nil }
