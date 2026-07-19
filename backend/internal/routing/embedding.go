// Package routing — Embedding 语义匹配层
//
// 提供文本向量化的抽象接口、余弦相似度计算、嵌入向量缓存
// 以及 OpenAI 兼容的 Embedding API 实现。
//
// # 为什么用 Embedding 做路由
//
// 关键词匹配的致命缺陷：无法理解同义改写。
//
//	"帮我排查这个并发Bug"               → 关键词匹配：0 命中（无"bug"/"debug"）
//	"这段代码在高并发场景下偶发panic"     → 关键词匹配：0 命中
//	"这个函数线程安全吗"                → 关键词匹配：0 命中
//
// 而基于 Embedding 的语义匹配，上述三个问题都能正确识别为 debugging 意图，
// 因为它们与 debugging 意图描述在向量空间中高度接近。
//
// # 余弦相似度
//
//	cos(θ) = A·B / (||A|| × ||B||)
//
// 两个向量方向越一致，余弦值越接近 1。
// 阈值默认 0.75：高于此值认为语义匹配，低于则视为不确定。
//
// # 缓存策略
//
//	用户连续提问往往围绕同一主题，提示词高度相似。
//	对 1000 个 prompt embedding 做简单 map 缓存，TTL 1 小时。
//	命中率预估 30-50%（基于同一对话轮次的重复/相似问题）。
package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"
)

// ────────────────────────────────────────────────────────────
// 核心类型
// ────────────────────────────────────────────────────────────

// Embedding 是一个浮点数向量。
//
// 维度取决于使用的模型：
//   - text-embedding-3-small：1536 维
//   - text-embedding-3-large：3072 维
//   - text-embedding-ada-002：1536 维
//
// 使用 []float64 而非固定长度数组以支持不同模型。
type Embedding []float64

// EmbeddingService 为文本生成向量表示。
//
// 实现者负责：
//   - 调用外部 Embedding API
//   - 处理网络错误和重试
//   - 遵守 context 超时
//
// 典型实现：OpenAIEmbeddingService、MockEmbeddingService（测试用）。
type EmbeddingService interface {
	// Embed 将文本转换为向量。
	// ctx 用于超时控制和请求取消。
	Embed(ctx context.Context, text string) (Embedding, error)
}

// ────────────────────────────────────────────────────────────
// 余弦相似度（纯 Go 实现，零依赖）
// ────────────────────────────────────────────────────────────

// CosineSimilarity 计算两个嵌入向量的余弦相似度。
//
// 数学公式：
//
//	similarity = (A·B) / (||A|| × ||B||)
//
// 其中：
//   - A·B = Σ(A[i] × B[i])    （内积/点积）
//   - ||A|| = √Σ(A[i]²)       （L2 范数）
//
// 返回值范围 [-1, 1]，1 表示方向完全相同（语义最接近）。
//
// 边界处理：
//   - 长度不匹配 → 返回 0（调用方应保证向量维度一致）
//   - 零向量（范数为 0）→ 返回 0（避免除零）
//   - 空向量 → 返回 0
//
// 复杂度：O(n)，其中 n 是向量维度（通常 1536）。
func CosineSimilarity(a, b Embedding) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ────────────────────────────────────────────────────────────
// 嵌入向量缓存
// ────────────────────────────────────────────────────────────

// cacheEntry 是缓存中的一条记录。
type cacheEntry struct {
	embedding Embedding
	expiresAt time.Time
}

// EmbeddingCache 是嵌入向量的内存缓存。
//
// 使用 sync.RWMutex 保护并发读写。
// 读多写少场景（同一对话中 prompt 高度重复），RLock 不阻塞并发读取。
//
// 淘汰策略：惰性淘汰（读取时检查 TTL）+ 后台定期清理。
type EmbeddingCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

// NewEmbeddingCache 创建一个嵌入向量缓存。
//
// ttl 是每条缓存记录的存活时间。推荐值：1 小时。
func NewEmbeddingCache(ttl time.Duration) *EmbeddingCache {
	c := &EmbeddingCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
	// 启动后台清理协程（每 10 分钟清理过期条目）。
	go c.evictLoop(10 * time.Minute)
	return c
}

// Get 从缓存中获取嵌入向量（返回副本，修改不影响缓存内部数据）。
func (c *EmbeddingCache) Get(key string) (Embedding, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}

	// 返回副本，防止调用方修改影响缓存一致性。
	result := make(Embedding, len(entry.embedding))
	copy(result, entry.embedding)
	return result, true
}

// Set 将嵌入向量写入缓存。
func (c *EmbeddingCache) Set(key string, emb Embedding) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 简单容量控制：超过 10000 条时随机淘汰 10%。
	// 注：更精确的实现应使用 LRU，但 prompt embedding 场景
	// 下 10000 条已远超实际需求（通常 < 1000 条）。
	if len(c.entries) >= 10000 {
		count := len(c.entries) / 10
		for k := range c.entries {
			delete(c.entries, k)
			count--
			if count <= 0 {
				break
			}
		}
	}

	c.entries[key] = cacheEntry{
		embedding: make(Embedding, len(emb)),
		expiresAt: time.Now().Add(c.ttl),
	}
	copy(c.entries[key].embedding, emb)
}

// Size 返回当前缓存的条目数（用于监控）。
func (c *EmbeddingCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// evictLoop 在后台定期清理过期条目。
func (c *EmbeddingCache) evictLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		c.evictExpired()
	}
}

// evictExpired 清理所有过期的缓存条目。
func (c *EmbeddingCache) evictExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// ────────────────────────────────────────────────────────────
// OpenAI 兼容的 Embedding 服务实现
// ────────────────────────────────────────────────────────────

// OpenAIEmbeddingService 通过 OpenAI 兼容 API 生成文本嵌入向量。
//
// 支持的 API：
//   - OpenAI: https://api.openai.com/v1/embeddings
//   - 任何兼容 OpenAI embedding 格式的服务
//
// 默认模型：text-embedding-3-small（1536 维，性价比最优）。
type OpenAIEmbeddingService struct {
	endpoint string       // API 端点，如 https://api.openai.com/v1/embeddings
	apiKey   string       // API 密钥
	model    string       // 模型名，如 text-embedding-3-small
	client   *http.Client // HTTP 客户端
}

// OpenAIEmbeddingConfig 是 OpenAI Embedding 服务的配置。
type OpenAIEmbeddingConfig struct {
	Endpoint string        // 可选，默认 https://api.openai.com/v1/embeddings
	APIKey   string        // 必填
	Model    string        // 可选，默认 text-embedding-3-small
	Timeout  time.Duration // 可选，默认 5 秒
}

// NewOpenAIEmbeddingService 创建 OpenAI 兼容的 Embedding 服务。
//
// 如果 cfg.Endpoint 为空，默认使用 OpenAI 官方端点。
// 如果 cfg.Model 为空，默认使用 text-embedding-3-small（性价比最高）。
func NewOpenAIEmbeddingService(cfg OpenAIEmbeddingConfig) *OpenAIEmbeddingService {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.openai.com/v1/embeddings"
	}
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small" // 1536 维，$0.02/1M tokens
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	return &OpenAIEmbeddingService{
		endpoint: cfg.Endpoint,
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		client: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// openaiEmbeddingReq 是 OpenAI Embedding API 的请求体。
type openaiEmbeddingReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// openaiEmbeddingResp 是 OpenAI Embedding API 的响应体。
type openaiEmbeddingResp struct {
	Data []struct {
		Embedding Embedding `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed 生成文本的嵌入向量。
//
// 调用 OpenAI /v1/embeddings API。
// ctx 用于超时控制（如果 ctx 的 deadline 比 HTTP client timeout 更紧，
// ctx 的 deadline 优先生效）。
func (s *OpenAIEmbeddingService) Embed(ctx context.Context, text string) (Embedding, error) {
	reqBody := openaiEmbeddingReq{
		Model: s.model,
		Input: text,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API call: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 限制 1MB
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var result openaiEmbeddingResp
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("embedding API error: %s", result.Error.Message)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned empty data")
	}

	return result.Data[0].Embedding, nil
}

// ────────────────────────────────────────────────────────────
// 测试用 Mock 实现
// ────────────────────────────────────────────────────────────

// MockEmbeddingService 是用于单元测试的 Embedding 服务。
//
// 不调用外部 API，通过预注册的映射表直接返回嵌入向量。
// 每个意图的嵌入向量中，对应维度设为 1.0，其余为 0——
// 这保证同意图的向量余弦相似度为 1，不同意图为 0。
type MockEmbeddingService struct {
	// 文本 → 嵌入向量的映射
	embeddings map[string]Embedding
	// 向量维度
	dim int
}

// NewMockEmbeddingService 创建一个 Mock 嵌入服务。
//
// dim 是生成的向量维度（测试用 4 或 8 即可）。
func NewMockEmbeddingService(dim int) *MockEmbeddingService {
	return &MockEmbeddingService{
		embeddings: make(map[string]Embedding),
		dim:        dim,
	}
}

// Register 注册一个文本的嵌入向量。
//
// index 决定向量的哪个位置为 1.0（0-indexed）。
// 这使得 CosineSimilarity：
//   - 相同 index → 1.0（完全匹配）
//   - 不同 index → 0.0（完全不匹配）
func (m *MockEmbeddingService) Register(text string, index int) {
	emb := make(Embedding, m.dim)
	if index >= 0 && index < m.dim {
		emb[index] = 1.0
	}
	m.embeddings[text] = emb
}

// Embed 返回预注册的嵌入向量。
func (m *MockEmbeddingService) Embed(_ context.Context, text string) (Embedding, error) {
	if emb, ok := m.embeddings[text]; ok {
		return emb, nil
	}
	// 未注册 → 返回零向量
	return make(Embedding, m.dim), nil
}
