package mock

import (
	"context"
	"hash/fnv"
	"math"

	"github.com/ragent/router/internal/routing"
)

// MockEmbeddingService 返回确定性的 fake 嵌入向量。
// 用 FNV-64a 哈希生成伪随机向量，相同文本产生相同向量，
// 不同文本产生不同向量（余弦相似度 ≈ 0.0~0.3）。
//
// 这使得语义缓存的 Demo 可预测：
//   - 完全相同的 prompt → 余弦相似度 1.0 → 一定命中
//   - 不同的 prompt → 低相似度 → 一定不命中
type MockEmbeddingService struct {
	dim int // 向量维度（默认 128，比真实 1536 更轻量）
}

// NewMockEmbeddingService 创建 mock embedding 服务。
func NewMockEmbeddingService(dim int) *MockEmbeddingService {
	if dim <= 0 {
		dim = 128
	}
	return &MockEmbeddingService{dim: dim}
}

// Embed 生成确定性的伪随机向量。
func (m *MockEmbeddingService) Embed(_ context.Context, text string) (routing.Embedding, error) {
	vec := make(routing.Embedding, m.dim)

	// 对文本做 hash，用不同 seed 生成不同维度的值
	h := fnv.New64a()
	h.Write([]byte(text))
	base := h.Sum64()

	for i := 0; i < m.dim; i++ {
		// 确定性伪随机：使用 base + i 作为 seed
		seed := base + uint64(i*31)
		// 简单的伪随机生成：[-1, 1] 范围
		val := math.Sin(float64(seed)*0.01) + math.Cos(float64(seed)*0.007)
		vec[i] = val / 2.0 // 归一化到约 [-0.5, 0.5]
	}

	// 归一化到单位向量
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] /= norm
		}
	}

	return vec, nil
}
