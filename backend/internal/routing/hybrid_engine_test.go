// Package routing — 混合路由引擎单元测试
//
// 使用 Mock 嵌入服务和 Mock 分类器验证三阶段路由的正确性：
//   - 关键词匹配优先
//   - Embedding 语义匹配（相似度门控）
//   - LLM 分类器（低置信度兜底）
//   - 默认回退
package routing

import (
	"context"
	"testing"
	"time"

	"github.com/ragent/router/internal/proxy"
)

// ────────────────────────────────────────────────────────────
// 测试辅助函数
// ────────────────────────────────────────────────────────────

// newTestProviderMap 创建测试用的供应商注册表。
func newTestProviderMap() map[string]*proxy.ProviderConfig {
	return map[string]*proxy.ProviderConfig{
		"Claude":   {Name: "Claude", BaseURL: "https://api.anthropic.com", Enabled: true},
		"DeepSeek": {Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Enabled: true},
		"OpenAI":   {Name: "OpenAI", BaseURL: "https://api.openai.com", Enabled: true},
	}
}

// newTestKeywords 创建测试用的关键词规则。
func newTestKeywords() []Rule {
	return []Rule{
		{Name: "重构", Keywords: []string{"refactor", "重构"}, Provider: "Claude", Priority: 100},
		{Name: "调试", Keywords: []string{"debug", "调试", "bug"}, Provider: "Claude", Priority: 90},
		{Name: "简单问答", Keywords: []string{"what is", "什么是", "怎么"}, Provider: "DeepSeek", Priority: 50},
	}
}

// ────────────────────────────────────────────────────────────
// 余弦相似度测试
// ────────────────────────────────────────────────────────────

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     Embedding
		expected float64
	}{
		{
			name:     "相同向量→1.0",
			a:        Embedding{1, 2, 3},
			b:        Embedding{1, 2, 3},
			expected: 1.0,
		},
		{
			name:     "正交向量→0",
			a:        Embedding{1, 0, 0},
			b:        Embedding{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "相反方向→-1",
			a:        Embedding{1, 0, 0},
			b:        Embedding{-1, 0, 0},
			expected: -1.0,
		},
		{
			name:     "零向量→0",
			a:        Embedding{0, 0, 0},
			b:        Embedding{1, 2, 3},
			expected: 0.0,
		},
		{
			name:     "空向量→0",
			a:        Embedding{},
			b:        Embedding{1, 2, 3},
			expected: 0.0,
		},
		{
			name:     "长度不匹配→0",
			a:        Embedding{1, 2},
			b:        Embedding{1, 2, 3},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if !floatApprox(got, tt.expected, 0.0001) {
				t.Errorf("CosineSimilarity = %v, want %v", got, tt.expected)
			}
		})
	}
}

func floatApprox(a, b, tolerance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < tolerance
}

// ────────────────────────────────────────────────────────────
// 混合路由引擎测试
// ────────────────────────────────────────────────────────────

func TestHybridRouter_KeywordMatch(t *testing.T) {
	// 仅配置关键词（无 Embedding、无 Classifier）。
	router := NewHybridRouter(HybridConfig{
		Keywords:        newTestKeywords(),
		Intents:         DefaultIntents(),
		Providers:       newTestProviderMap(),
		DefaultProvider: "DeepSeek",
	})

	ctx := context.Background()

	// 关键词命中：包含 "refactor" → Claude。
	prov := router.Match(ctx, "help me refactor this class", "")
	if prov == nil {
		t.Fatal("expected non-nil provider")
	}
	if prov.Name != "Claude" {
		t.Errorf("expected Claude, got %s", prov.Name)
	}

	// 关键词命中：包含 "调试" → Claude。
	prov = router.Match(ctx, "我需要调试这段代码", "")
	if prov == nil {
		t.Fatal("expected non-nil provider")
	}
	if prov.Name != "Claude" {
		t.Errorf("expected Claude, got %s", prov.Name)
	}

	// 无关键词命中 → 默认回退。
	// "今天天气怎么样" 包含"怎么"→命中简单问答规则，使用不含关键词的输入。
	prov = router.Match(ctx, "xyzzy xyzzy xyzzy", "")
	if prov == nil {
		t.Fatal("expected non-nil provider (fallback)")
	}
	if prov.Name != "DeepSeek" {
		t.Errorf("expected DeepSeek (fallback), got %s", prov.Name)
	}

	// 验证统计。
	stats := router.Stats()
	if stats.KeywordHits != 2 {
		t.Errorf("expected 2 keyword hits, got %d", stats.KeywordHits)
	}
	if stats.FallbackHits != 1 {
		t.Errorf("expected 1 fallback hit, got %d", stats.FallbackHits)
	}
}

func TestHybridRouter_SemanticMatch(t *testing.T) {
	// 创建 Mock 嵌入服务。
	mockEmb := NewMockEmbeddingService(8)

	// 注册意图嵌入（index=0→debugging, index=1→simple_qa 等）。
	intents := DefaultIntents()
	for i, intent := range intents {
		text := intent.Description
		for _, ex := range intent.Examples {
			text += " | " + ex
		}
		mockEmb.Register(text, i)
	}

	// 注册用户 prompt 嵌入：与 debugging (index=1) 方向一致。
	mockEmb.Register("帮我排查这个并发问题", 1) // debugging 的 index

	router := NewHybridRouter(HybridConfig{
		Keywords:            newTestKeywords(),
		Intents:             intents,
		EmbeddingService:    mockEmb,
		Providers:           newTestProviderMap(),
		DefaultProvider:     "DeepSeek",
		SimilarityThreshold: 0.5, // 低于默认 0.75，便于测试
	})

	// 预热意图嵌入。
	if err := router.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx := context.Background()

	// prompt 不包含任何关键词，但语义上与 debugging 意图的 embedding 方向一致（余弦相似度=1.0）。
	prov := router.Match(ctx, "帮我排查这个并发问题", "")
	if prov == nil {
		t.Fatal("expected non-nil provider")
	}
	if prov.Name != "Claude" {
		t.Errorf("expected Claude (semantic→debugging), got %s", prov.Name)
	}

	stats := router.Stats()
	if stats.EmbeddingHits != 1 {
		t.Errorf("expected 1 embedding hit, got %d", stats.EmbeddingHits)
	}
}

func TestHybridRouter_SemanticLowConfidence(t *testing.T) {
	// 低置信度场景：prompt 的 embedding 与所有意图都正交（全零向量）。
	mockEmb := NewMockEmbeddingService(8)
	intents := DefaultIntents()
	for i, intent := range intents {
		text := intent.Description
		for _, ex := range intent.Examples {
			text += " | " + ex
		}
		mockEmb.Register(text, i)
	}
	// 不注册用户 prompt → Embed() 返回零向量 → 与所有意图的余弦相似度=0 < 阈值。

	// 使用 Mock 分类器（捕捉分类器调用）。
	mockCls := &mockClassifier{result: "Bug 调试"}

	router := NewHybridRouter(HybridConfig{
		Keywords:            newTestKeywords(),
		Intents:             intents,
		EmbeddingService:    mockEmb,
		Classifier:          mockCls,
		Providers:           newTestProviderMap(),
		DefaultProvider:     "DeepSeek",
		SimilarityThreshold: 0.5,
	})

	if err := router.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// prompt 不含关键词 + embedding 相似度=0 < 阈值 → 触发 LLM 分类器
	prov := router.Match(context.Background(), "一个非常模糊的问题描述", "")
	if prov == nil {
		t.Fatal("expected non-nil provider")
	}
	if prov.Name != "Claude" {
		t.Errorf("expected Claude (classifier→debugging), got %s", prov.Name)
	}

	stats := router.Stats()
	if stats.ClassifierHits != 1 {
		t.Errorf("expected 1 classifier hit, got %d", stats.ClassifierHits)
	}
}

func TestHybridRouter_DefaultFallback(t *testing.T) {
	// 所有层都不配置 → 纯关键词 + 默认回退。
	router := NewHybridRouter(HybridConfig{
		Keywords:        newTestKeywords(),
		Intents:         DefaultIntents(),
		Providers:       newTestProviderMap(),
		DefaultProvider: "DeepSeek",
	})

	ctx := context.Background()

	// 不匹配任何关键词 → 默认回退。
	prov := router.Match(ctx, "完全不相关的输入", "")
	if prov == nil {
		t.Fatal("expected non-nil provider (fallback)")
	}
	if prov.Name != "DeepSeek" {
		t.Errorf("expected DeepSeek, got %s", prov.Name)
	}

	stats := router.Stats()
	if stats.FallbackHits != 1 {
		t.Errorf("expected 1 fallback hit, got %d", stats.FallbackHits)
	}
}

func TestHybridRouter_EmptyPrompt(t *testing.T) {
	router := NewHybridRouter(HybridConfig{
		Keywords:        newTestKeywords(),
		Intents:         DefaultIntents(),
		Providers:       newTestProviderMap(),
		DefaultProvider: "DeepSeek",
	})

	prov := router.Match(context.Background(), "", "")
	if prov == nil {
		t.Fatal("expected non-nil provider for empty prompt")
	}
	if prov.Name != "DeepSeek" {
		t.Errorf("expected DeepSeek, got %s", prov.Name)
	}
}

func TestHybridRouter_CacheHit(t *testing.T) {
	mockEmb := NewMockEmbeddingService(8)
	intents := DefaultIntents()
	for i, intent := range intents {
		text := intent.Description
		for _, ex := range intent.Examples {
			text += " | " + ex
		}
		mockEmb.Register(text, i)
	}
	mockEmb.Register("帮我排查这个并发问题", 1)

	router := NewHybridRouter(HybridConfig{
		Keywords:            newTestKeywords(),
		Intents:             intents,
		EmbeddingService:    mockEmb,
		Providers:           newTestProviderMap(),
		DefaultProvider:     "DeepSeek",
		SimilarityThreshold: 0.5,
	})

	if err := router.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx := context.Background()

	// 第一次调用：生成 embedding 并缓存。
	prov := router.Match(ctx, "帮我排查这个并发问题", "")
	if prov == nil || prov.Name != "Claude" {
		t.Fatalf("first call failed")
	}

	// 缓存应该有一条记录。
	if size := router.CacheSize(); size != 1 {
		t.Errorf("expected cache size 1, got %d", size)
	}

	// 第二次调用：应该命中缓存。
	prov = router.Match(ctx, "帮我排查这个并发问题", "")
	if prov == nil || prov.Name != "Claude" {
		t.Fatalf("second call (cache hit) failed")
	}

	// 缓存大小应该仍是 1（不重复生成）。
	if size := router.CacheSize(); size != 1 {
		t.Errorf("expected cache size still 1, got %d", size)
	}
}

func TestHybridRouter_NoEmbeddingService(t *testing.T) {
	// 不配置 Embedding 服务 → Init 是空操作，不报错。
	router := NewHybridRouter(HybridConfig{
		Keywords:        newTestKeywords(),
		Intents:         DefaultIntents(),
		Providers:       newTestProviderMap(),
		DefaultProvider: "DeepSeek",
	})

	if err := router.Init(context.Background()); err != nil {
		t.Fatalf("Init should succeed without embedding service: %v", err)
	}

	// 仍然可以正常路由（关键词 + 默认回退）。
	prov := router.Match(context.Background(), "help me refactor this", "")
	if prov == nil || prov.Name != "Claude" {
		t.Errorf("expected Claude from keyword match")
	}
}

// ────────────────────────────────────────────────────────────
// Mock 分类器
// ────────────────────────────────────────────────────────────

// mockClassifier 是用于测试的假分类器。
type mockClassifier struct {
	result string // 预设的分类结果
}

func (m *mockClassifier) Classify(_ context.Context, _ string, _ []Intent) (string, error) {
	return m.result, nil
}

// ────────────────────────────────────────────────────────────
// EmbeddingCache 测试
// ────────────────────────────────────────────────────────────

func TestEmbeddingCache_Hit(t *testing.T) {
	cache := NewEmbeddingCache(1 * time.Hour)
	emb := Embedding{1.0, 2.0, 3.0}

	cache.Set("key1", emb)
	got, ok := cache.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != len(emb) || got[0] != 1.0 || got[1] != 2.0 || got[2] != 3.0 {
		t.Errorf("cache returned wrong value: %v", got)
	}
}

func TestEmbeddingCache_Miss(t *testing.T) {
	cache := NewEmbeddingCache(1 * time.Hour)

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestEmbeddingCache_CopyProtection(t *testing.T) {
	// 验证缓存返回的是副本，修改不影响缓存内部数据。
	cache := NewEmbeddingCache(1 * time.Hour)
	original := Embedding{1.0, 2.0, 3.0}
	cache.Set("key", original)

	got, _ := cache.Get("key")
	got[0] = 999.0 // 尝试修改返回值

	got2, _ := cache.Get("key")
	if got2[0] != 1.0 {
		t.Errorf("cache should return copy, got %v", got2)
	}
}

func TestEmbeddingCache_Size(t *testing.T) {
	cache := NewEmbeddingCache(1 * time.Hour)

	cache.Set("a", Embedding{1})
	cache.Set("b", Embedding{2})
	cache.Set("c", Embedding{3})

	if size := cache.Size(); size != 3 {
		t.Errorf("expected size 3, got %d", size)
	}
}

