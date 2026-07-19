// Package semcache 实现语义缓存服务——
// 通过 Embedding 向量余弦相似度匹配，复用历史 API 响应。
//
// # 工作原理
//
//	1. 用户 prompt → Embedding API 生成向量
//	2. 与缓存库中的所有向量计算余弦相似度
//	3. 相似度 > 阈值 → 直接返回缓存响应（不调上游 API）
//	4. 相似度 < 阈值 → 正常路由 → 新响应写入缓存
//
// # 设计权衡
//
//   - 相似度阈值 0.92：平衡精度与召回率。太高则命中率低，太低则可能返回不相关答案。
//   - 暴力搜索 O(N×D)：N≤1000 时性能可接受；超大规模时需引入向量索引（FAISS/HNSW）。
//   - 缓存 TTL 24h：短时效内容（代码问答）适合长 TTL，创意内容不适合缓存。
package semcache

import (
	"context"
	"log"

	"github.com/ragent/router/internal/routing"
	"github.com/ragent/router/internal/store"
)

// Service 实现 proxy.SemanticCache 接口。
type Service struct {
	store    *store.SemanticCacheStore
	embedder routing.EmbeddingService // 复用已有的 Embedding 服务
}

// New 创建语义缓存服务。
func New(store *store.SemanticCacheStore, embedder routing.EmbeddingService) *Service {
	return &Service{store: store, embedder: embedder}
}

// Lookup 查找语义相似的缓存响应。
// 返回缓存响应体、相似度、是否命中。
func (s *Service) Lookup(ctx context.Context, prompt string) ([]byte, float64, bool) {
	if s.embedder == nil {
		return nil, 0, false
	}

	// 生成 prompt 的 embedding 向量
	emb, err := s.embedder.Embed(ctx, prompt)
	if err != nil {
		log.Printf("[缓存] Embedding 生成失败: %v", err)
		return nil, 0, false
	}

	// 在缓存中查找相似条目
	entry, ok := s.store.FindSimilar(emb)
	if !ok {
		return nil, 0, false
	}

	return []byte(entry.Response), entry.Similarity, true
}

// Store 将上游响应保存到缓存。
func (s *Service) Store(prompt string, responseBody []byte, provider, model string, tokens int) {
	// 需要生成 embedding 才能存储——这里使用与 Lookup 相同的 embedder
	// 但 Store 是异步调用的，如果 embedder 不可用则跳过
	if s.embedder == nil {
		return
	}

	// 注意：这里同步生成 embedding 可能增加延迟。
	// 生产环境可改为异步 goroutine + channel。
	emb, err := s.embedder.Embed(context.Background(), prompt)
	if err != nil {
		log.Printf("[缓存] 存储失败（embedding 生成错误）: %v", err)
		return
	}

	entry := &store.SemCacheEntry{
		PromptEmbedding: emb,
		PromptText:      prompt,
		Response:        string(responseBody),
		Provider:        provider,
		Model:           model,
		Tokens:          tokens,
	}

	if err := s.store.Insert(entry); err != nil {
		log.Printf("[缓存] 存储失败: %v", err)
	}
}

// Stats 返回缓存命中统计。
func (s *Service) Stats() (*store.SemCacheStats, error) {
	return s.store.Stats()
}

// Count 返回当前缓存条目数。
func (s *Service) Count() (int64, error) {
	return s.store.Count()
}

// Clear 清空缓存。
func (s *Service) Clear() error {
	return s.store.Clear()
}
