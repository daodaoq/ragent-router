package mock

import (
	"log"
	"time"

	"github.com/ragent/router/internal/proxy"
	"github.com/ragent/router/internal/store"
)

// Setup 配置 Mock 模式的全部基础设施。
// 返回 mock 上游服务器地址、mock embedding 服务、预配置的供应商列表。
//
// 调用方需要提供 logStore 用于预填充演示数据。
func Setup(logStore *store.LogStore) (upstreamAddr string, embedder *MockEmbeddingService, providers []proxy.ProviderConfig) {
	// ── 1. 启动 Mock 上游 AI API 服务器 ──
	var cleanup func()
	upstreamAddr, cleanup = StartUpstreamServer()
	// cleanup 在程序退出时调用（通过 defer 在 main 中处理）
	_ = cleanup

	log.Printf("[Mock] 上游服务器已启动: %s", upstreamAddr)

	// ── 2. Mock Embedding 服务 ──
	embedder = NewMockEmbeddingService(128)

	// ── 3. Mock 供应商配置（指向本地 mock 服务器）──
	providers = []proxy.ProviderConfig{
		{
			ID:      "claude-mock",
			Name:    "Claude",
			BaseURL: upstreamAddr,
			APIKey:  "mock-key-claude",
			Model:   "claude-sonnet-4-20250514",
			Enabled: true,
		},
		{
			ID:      "deepseek-mock",
			Name:    "DeepSeek",
			BaseURL: upstreamAddr,
			APIKey:  "mock-key-deepseek",
			Model:   "deepseek-chat",
			Enabled: true,
		},
	}

	// ── 4. 预填充 Dashboard 演示数据 ──
	seedDemoData(logStore)

	return
}

// seedDemoData 写入模拟历史数据，让 Dashboard 首次打开就有内容展示。
func seedDemoData(logStore *store.LogStore) {
	now := time.Now()
	prompts := []struct {
		prompt    string
		provider  string
		model     string
		status    string
		cost      float64
		latencyMs int64
		tokens    int
		daysAgo   int
	}{
		{"设计一个分布式缓存系统架构", "Claude", "claude-sonnet-4-20250514", "ok", 0.045, 3200, 15000, 0},
		{"这段代码在高并发下偶发panic，帮我排查", "Claude", "claude-sonnet-4-20250514", "ok", 0.038, 2800, 12000, 0},
		{"什么是RESTful API设计原则", "DeepSeek", "deepseek-chat", "ok", 0.002, 800, 3000, 0},
		{"Go的defer关键字怎么用", "DeepSeek", "deepseek-chat", "ok", 0.001, 500, 2000, 1},
		{"帮我写一个LRU缓存的Go实现", "Claude", "claude-sonnet-4-20250514", "ok", 0.052, 3500, 18000, 1},
		{"Redis和Memcached的区别", "DeepSeek", "deepseek-chat", "ok", 0.001, 600, 2500, 1},
		{"这个SQL查询为什么这么慢", "Claude", "claude-sonnet-4-20250514", "ok", 0.028, 2100, 9000, 1},
		{"如何优化这段Python代码的性能", "Claude", "claude-sonnet-4-20250514", "ok", 0.035, 2600, 11000, 2},
		{"git merge和rebase的区别", "DeepSeek", "deepseek-chat", "ok", 0.001, 450, 1800, 3},
		{"设计一个支持百万并发的秒杀系统", "Claude", "claude-sonnet-4-20250514", "ok", 0.068, 4500, 22000, 3},
		{"怎么用Docker部署Go应用", "DeepSeek", "deepseek-chat", "ok", 0.002, 700, 3500, 4},
		{"高并发下数据一致性如何保证", "Claude", "claude-sonnet-4-20250514", "ok", 0.042, 3000, 14000, 5},
		{"Python装饰器的原理", "DeepSeek", "deepseek-chat", "ok", 0.001, 550, 2200, 6},
		{"微服务之间如何做认证授权", "Claude", "claude-sonnet-4-20250514", "ok", 0.038, 2700, 12500, 6},
		{"Kubernetes Pod一直Pending怎么排查", "DeepSeek", "deepseek-chat", "error", 0, 30000, 0, 7},
	}

	for _, p := range prompts {
		t := now.AddDate(0, 0, -p.daysAgo)
		_ = logStore.Insert(&store.RequestLogRecord{
			ID:                mockID(p.daysAgo, p.provider),
			Prompt:            p.prompt,
			PromptTokens:      p.tokens / 3 * 2,
			CompletionTokens:  p.tokens / 3,
			TotalTokens:       p.tokens,
			Model:             p.model,
			Provider:          p.provider,
			RouteReason:       "keyword_match",
			Status:            p.status,
			CostUSD:           p.cost,
			LatencyMs:         p.latencyMs,
			CreatedAt:         t,
		})
	}

	log.Printf("[Mock] 已写入 %d 条模拟请求日志", len(prompts))
}

func mockID(daysAgo int, provider string) string {
	return provider + "-" + time.Now().AddDate(0, 0, -daysAgo).Format("150405")
}
