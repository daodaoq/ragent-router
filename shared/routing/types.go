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
	ProviderName string
	Strategy     string
	Confidence   float64
	Reason       string
}
