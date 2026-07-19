// Package routing — 混合智能路由引擎
//
// HybridRouter 是项目的核心路由组件，实现了三阶段渐进式路由策略：
//
//	关键词规则（0ms） → Embedding 语义匹配（~300ms） → LLM 分类器（~500ms） → 默认回退
//
// # 架构哲学
//
// 每一层都是一道"过滤网"：
//   - 关键词规则捕获明确的领域术语（"重构"、"debug" 等）
//   - Embedding 捕获语义相似但表述不同的意图（"帮我排查这个并发问题"）
//   - LLM 分类器处理歧义和边界样本（"帮我看看这个"）
//   - 默认回退保证在任何情况下服务都可用
//
// 层与层之间是"快速失败"模式——上一层未命中时自动穿透到下一层，
// 每层的失败不影响后续层，也不影响整体服务的可用性。
//
// # 并发安全
//
//   - 所有 Match 调用共享同一个缓存实例（EmbeddingCache 内部使用 sync.RWMutex）
//   - 意图嵌入在 Init 阶段一次性初始化，之后只读——无需锁保护
//   - 统计字段使用 sync/atomic 做无锁更新
//
// # 配置灵活性
//
// 每个策略层都是可插拔的 interface：
//   - EmbeddingService == nil → 跳过语义匹配层
//   - IntentClassifier == nil → 跳过 LLM 分类层
//   - 两层都 nil → 退化为纯关键词路由（向后兼容）
package routing

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ragent/router/internal/proxy"
)

// ────────────────────────────────────────────────────────────
// 混合路由引擎
// ────────────────────────────────────────────────────────────

// HybridRouter 是多策略融合的智能路由引擎。
//
// 零依赖运行：
//   - 不配置 EmbeddingService → 纯关键词 + LLM 分类
//   - 不配置 Classifier → 关键词 + Embedding
//   - 都不配置 → 纯关键词（等同于 RuleEngine）
type HybridRouter struct {
	// ── 策略层 1：关键词规则引擎（必定存在）──
	keywordEngine *RuleEngine

	// ── 策略层 2：Embedding 语义匹配（可选）──
	intents       []IntentEmbedding // 预计算的意图嵌入（Init/Reload 后填充）
	embeddingSvc  EmbeddingService
	cache         *EmbeddingCache
	simThreshold  float64 // 余弦相似度阈值（默认 0.75）

	// ── 策略层 3：LLM 分类器（可选）──
	classifier      IntentClassifier
	classifyIntents []Intent   // 传递给分类器的意图列表（热重载时更新）
	intentMu        sync.RWMutex // 保护 intents / classifyIntents 热重载

	// ── 供应商注册表 ──
	providers      map[string]*proxy.ProviderConfig
	defaultProvider string

	// ── 统计（atomic，无锁）──
	keywordHits   int64
	embeddingHits int64
	classifierHits int64
	fallbackHits  int64

	// ── 初始化 ──
	initOnce sync.Once
	initErr  error
}

// HybridConfig 是 HybridRouter 的配置参数。
type HybridConfig struct {
	// Keywords 是关键词路由规则集（必填，作为第一道防线）。
	Keywords []Rule

	// Intents 是意图定义列表（必填，用于 Embedding 和 LLM 分类）。
	Intents []Intent

	// EmbeddingService 是嵌入向量服务（nil = 禁用语义匹配）。
	EmbeddingService EmbeddingService

	// Classifier 是 LLM 意图分类器（nil = 禁用 LLM 分类）。
	Classifier IntentClassifier

	// SimilarityThreshold 是余弦相似度阈值（0.0-1.0）。
	// 低于此值时触发 LLM 分类器。
	// 默认 0.75。
	SimilarityThreshold float64

	// EmbeddingCacheTTL 是嵌入向量缓存的有效期。
	// 默认 1 小时。
	EmbeddingCacheTTL time.Duration

	// Providers 是供应商名称 → 配置的映射（必填）。
	Providers map[string]*proxy.ProviderConfig

	// DefaultProvider 是所有策略都失败时的默认供应商名称（必填）。
	DefaultProvider string
}

// NewHybridRouter 创建混合路由引擎。
//
// 创建后应调用 Init(ctx) 预热意图嵌入向量。
// 如果未配置 EmbeddingService，Init 为空操作。
func NewHybridRouter(cfg HybridConfig) *HybridRouter {
	if cfg.SimilarityThreshold <= 0 {
		cfg.SimilarityThreshold = 0.75
	}
	if cfg.EmbeddingCacheTTL <= 0 {
		cfg.EmbeddingCacheTTL = 1 * time.Hour
	}

	return &HybridRouter{
		keywordEngine:   NewRuleEngine(cfg.Keywords, cfg.Providers, cfg.DefaultProvider),
		intents:         nil, // Init 时填充
		embeddingSvc:    cfg.EmbeddingService,
		cache:           NewEmbeddingCache(cfg.EmbeddingCacheTTL),
		simThreshold:    cfg.SimilarityThreshold,
		classifier:      cfg.Classifier,
		classifyIntents: cfg.Intents,
		providers:       cfg.Providers,
		defaultProvider: cfg.DefaultProvider,
	}
}

// ────────────────────────────────────────────────────────────
// 初始化（预热）
// ────────────────────────────────────────────────────────────

// Init 预热所有意图的嵌入向量。
//
// 应在服务启动时调用，避免首次请求的冷启动延迟。
// 如果未配置 EmbeddingService，此方法为空操作。
//
// 预热过程：
//  1. 将每个 Intent 的 Description + Examples 拼接为文本
//  2. 调用 EmbeddingService.Embed() 生成向量
//  3. 缓存到 intentEmbs 切片（之后只读，无需锁保护）
//
// 超时控制：使用传入的 ctx（建议 30s）。
func (r *HybridRouter) Init(ctx context.Context) error {
	r.initOnce.Do(func() {
		r.initErr = r.warmupIntents(ctx)
	})
	return r.initErr
}

// warmupIntents 预计算所有意图的嵌入向量。
func (r *HybridRouter) warmupIntents(ctx context.Context) error {
	if r.embeddingSvc == nil {
		log.Println("[路由] Embedding 服务未配置，语义匹配层已禁用")
		return nil
	}

	intents := r.classifyIntents
	if len(intents) == 0 {
		return nil
	}

	r.intents = make([]IntentEmbedding, 0, len(intents))
	for _, intent := range intents {
		// 拼接描述和所有示例——这比纯描述更能捕捉意图的语义范围。
		text := intent.Description
		for _, ex := range intent.Examples {
			text += " | " + ex
		}

		emb, err := r.embeddingSvc.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("warmup intent %s: %w", intent.Name, err)
		}

		r.intents = append(r.intents, IntentEmbedding{
			Intent:    intent,
			Embedding: emb,
		})
		log.Printf("[路由] 意图嵌入已就绪: %-25s 维度=%d", intent.Name, len(emb))
	}
	log.Printf("[路由] 全部 %d 个意图嵌入预热完成", len(r.intents))
	return nil
}

// ReloadIntents 运行时热重载意图列表。
//
// 用户通过 Dashboard 修改意图树后调用此方法，无需重启服务。
// 会重新预热所有意图的 Embedding 向量，并清空 Prompt 缓存。
//
// 线程安全：内部通过 intentMu 写锁保护意图列表。
func (r *HybridRouter) ReloadIntents(ctx context.Context, intents []Intent) error {
	if r.embeddingSvc == nil {
		r.intentMu.Lock()
		r.classifyIntents = intents
		r.intentMu.Unlock()
		log.Println("[路由] 意图列表已重载（无 Embedding 服务，跳过预热）")
		return nil
	}

	// 预热新意图的 Embedding 向量。
	newEmbs := make([]IntentEmbedding, 0, len(intents))
	for _, intent := range intents {
		text := intent.Description
		for _, ex := range intent.Examples {
			text += " | " + ex
		}
		emb, err := r.embeddingSvc.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("reload intent %s: %w", intent.IntentCode, err)
		}
		newEmbs = append(newEmbs, IntentEmbedding{Intent: intent, Embedding: emb})
		log.Printf("[路由] 意图嵌入已重载: %-25s 维度=%d", intent.IntentCode, len(emb))
	}

	// 原子替换意图列表和分类意图列表。
	r.intentMu.Lock()
	r.intents = newEmbs
	r.classifyIntents = intents
	r.intentMu.Unlock()

	// 清空 Prompt 缓存（旧意图的嵌入向量可能已失效）。
	r.cache = NewEmbeddingCache(1 * time.Hour)

	log.Printf("[路由] 意图热重载完成: %d 个叶子节点", len(newEmbs))
	return nil
}

// ────────────────────────────────────────────────────────────
// 路由主入口
// ────────────────────────────────────────────────────────────

// Match 根据用户提示词和请求模型名选择目标供应商。
//
// 三阶段路由流程：
//
//	关键词命中? ──Yes──→ 返回（0 API 调用）
//	  │ No
//	  ▼
//	Embedding 匹配? ──Yes(>阈值)──→ 返回（1 次 Embedding API 调用）
//	  │ No/低置信度
//	  ▼
//	LLM 分类器? ──Yes──→ 返回（1 次 LLM API 调用）
//	  │ No/失败
//	  ▼
//	默认回退（0 API 调用）
//
// ctx 用于 Embedding API 和 LLM 分类器的超时控制。
// 关键词匹配层不需要 ctx（纯内存计算）。
func (r *HybridRouter) Match(ctx context.Context, prompt string, model string) *proxy.ProviderConfig {
	if prompt == "" {
		return r.fallback()
	}

	// ════════════════════════════════════════════════════════════
	// 策略 1：关键词规则匹配（0ms，0 API 调用，必定执行）
	// ════════════════════════════════════════════════════════════
	if prov := r.keywordEngine.Match(ctx, prompt, model); prov != nil {
		atomic.AddInt64(&r.keywordHits, 1)
		return prov
	}

	// ════════════════════════════════════════════════════════════
	// 策略 2：Embedding 语义匹配（~300ms，1 次 Embedding API 调用）
	// ════════════════════════════════════════════════════════════
	if r.embeddingSvc != nil && len(r.intents) > 0 {
		if prov := r.semanticMatch(ctx, prompt); prov != nil {
			atomic.AddInt64(&r.embeddingHits, 1)
			return prov
		}
	}

	// ════════════════════════════════════════════════════════════
	// 策略 3：LLM 意图分类器（~500ms，1 次 Chat API 调用，~$0.001）
	// ════════════════════════════════════════════════════════════
	if r.classifier != nil {
		if prov := r.classify(ctx, prompt); prov != nil {
			atomic.AddInt64(&r.classifierHits, 1)
			return prov
		}
	}

	// ════════════════════════════════════════════════════════════
	// 策略 4：默认回退
	// ════════════════════════════════════════════════════════════
	atomic.AddInt64(&r.fallbackHits, 1)
	return r.fallback()
}

// ────────────────────────────────────────────────────────────
// 分类 API（供 /api/intent/classify 使用）
// ────────────────────────────────────────────────────────────

// ClassifyCandidate 是一个分类候选结果。
type ClassifyCandidate struct {
	IntentCode   string  `json:"intent_code"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Score        float64 `json:"score"`
	Reason       string  `json:"reason"`
	ProviderID   string  `json:"provider_id,omitempty"`
	ProviderName string  `json:"provider_name,omitempty"`
}

// ClassifyResult 是分类 API 的完整响应。
type ClassifyResult struct {
	Question        string              `json:"question"`
	Candidates      []ClassifyCandidate `json:"candidates"`
	Top             []ClassifyCandidate `json:"top"`
	Matched         *ClassifyCandidate  `json:"matched"`
	DefaultProvider *DefaultProviderInfo `json:"default_provider"`
	Switched        *SwitchResult       `json:"switched,omitempty"`
}

// DefaultProviderInfo 是默认供应商信息。
type DefaultProviderInfo struct {
	Found bool   `json:"found"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
}

// SwitchResult 是自动切换结果。
type SwitchResult struct {
	Success      bool   `json:"success"`
	ProviderName string `json:"provider_name,omitempty"`
	Detail       string `json:"detail,omitempty"`
	Fallback     bool   `json:"fallback"`
}

// Classify 对用户问题执行完整的三阶段分类，返回详细候选列表。
//
// 与 Match 的区别：
//   - Match 只返回最佳供应商（用于实际路由）
//   - Classify 返回所有候选的分数和详情（供前端展示）
//
// 如果 autoSwitch=true 且匹配到供应商，结果中会包含切换信息。
func (r *HybridRouter) Classify(ctx context.Context, prompt string) *ClassifyResult {
	result := &ClassifyResult{
		Question:   prompt,
		Candidates: []ClassifyCandidate{},
		Top:        []ClassifyCandidate{},
		DefaultProvider: &DefaultProviderInfo{},
	}

	// 默认供应商信息。
	if prov, ok := r.providers[r.defaultProvider]; ok && prov.Enabled {
		result.DefaultProvider = &DefaultProviderInfo{
			Found: true,
			ID:    prov.ID,
			Name:  prov.Name,
		}
	}

	if prompt == "" {
		return result
	}

	// ═══ 1. 获取 Embedding 候选列表 ═══
	r.intentMu.RLock()
	embs := r.intents
	r.intentMu.RUnlock()

	var emb Embedding
	if r.embeddingSvc != nil && len(embs) > 0 {
		var ok bool
		if emb, ok = r.cache.Get(prompt); !ok {
			var err error
			emb, err = r.embeddingSvc.Embed(ctx, prompt)
			if err != nil {
				log.Printf("[路由-分类] 嵌入生成失败: %v", err)
			} else {
				r.cache.Set(prompt, emb)
			}
		}
	}

	// ═══ 2. 构建候选列表（基于 Embedding 相似度）═══
	type scored struct {
		ie    IntentEmbedding
		score float64
	}
	var matches []scored
	if emb != nil && len(embs) > 0 {
		matches = make([]scored, len(embs))
		for i, ie := range embs {
			matches[i] = scored{ie, CosineSimilarity(emb, ie.Embedding)}
		}
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].score > matches[j].score
		})
	}

	// 构建候选结果列表。
	for _, m := range matches {
		intent := m.ie.Intent
		provName := ""
		provID := ""
		if prov, ok := r.providers[intent.Provider]; ok && prov.Enabled {
			provName = prov.Name
			provID = prov.ID
		}
		result.Candidates = append(result.Candidates, ClassifyCandidate{
			IntentCode:   intent.IntentCode,
			Name:         intent.Name,
			Description:  intent.Description,
			Score:        m.score,
			Reason:       fmt.Sprintf("余弦相似度 %.3f", m.score),
			ProviderID:   provID,
			ProviderName: provName,
		})
	}

	// Top 取前 3 个。
	topN := 3
	if len(result.Candidates) < topN {
		topN = len(result.Candidates)
	}
	result.Top = result.Candidates[:topN]

	// ═══ 3. 确定最佳匹配 ═══
	if len(matches) > 0 && matches[0].score >= r.simThreshold {
		best := matches[0]
		provName := ""
		provID := ""
		if prov, ok := r.providers[best.ie.Intent.Provider]; ok && prov.Enabled {
			provName = prov.Name
			provID = prov.ID
		}
		result.Matched = &ClassifyCandidate{
			IntentCode:   best.ie.Intent.IntentCode,
			Name:         best.ie.Intent.Name,
			Description:  best.ie.Intent.Description,
			Score:        best.score,
			Reason:       fmt.Sprintf("语义匹配 (余弦相似度 %.3f ≥ 阈值 %.2f)", best.score, r.simThreshold),
			ProviderID:   provID,
			ProviderName: provName,
		}
	} else if r.classifier != nil {
		// Embedding 低置信度 → 尝试 LLM 分类。
		r.intentMu.RLock()
		clIntents := r.classifyIntents
		r.intentMu.RUnlock()

		if len(clIntents) > 0 {
			intentName, err := r.classifier.Classify(ctx, prompt, clIntents)
			if err == nil && intentName != "" && intentName != "unknown" {
				// 在意图列表中查找匹配。
				for _, intent := range clIntents {
					if intent.Name == intentName || intent.IntentCode == intentName {
						provName := ""
						provID := ""
						if prov, ok := r.providers[intent.Provider]; ok && prov.Enabled {
							provName = prov.Name
							provID = prov.ID
						}
						result.Matched = &ClassifyCandidate{
							IntentCode:   intent.IntentCode,
							Name:         intent.Name,
							Description:  intent.Description,
							Score:        0.85, // LLM 分类默认置信度
							Reason:       fmt.Sprintf("LLM 分类器判定为 %s", intent.Name),
							ProviderID:   provID,
							ProviderName: provName,
						}
						break
					}
				}
			}
		}
	}

	return result
}

// ────────────────────────────────────────────────────────────
// 公开的意图列表访问
// ────────────────────────────────────────────────────────────

// GetIntents 返回当前活跃的意图列表（线程安全）。
func (r *HybridRouter) GetIntents() []Intent {
	r.intentMu.RLock()
	defer r.intentMu.RUnlock()
	result := make([]Intent, len(r.classifyIntents))
	copy(result, r.classifyIntents)
	return result
}

// ────────────────────────────────────────────────────────────
// 策略 2：Embedding 语义匹配
// ────────────────────────────────────────────────────────────

// semanticMatch 使用嵌入向量的余弦相似度匹配最佳意图。
//
// 流程：
//  1. 检查缓存（同一 prompt 不重复生成 embedding）
//  2. 生成用户 prompt 的 embedding 向量
//  3. 与所有意图的预计算向量逐一计算余弦相似度
//  4. 取相似度最高的意图
//  5. 如果最高相似度 < 阈值 → 返回 nil（触发下一层 LLM 分类）
//
// 返回 nil 表示：
//   - Embedding 生成失败（网络错误等）
//   - 最高相似度低于阈值（语义不确定）
//   - 匹配到的意图对应的供应商不可用
func (r *HybridRouter) semanticMatch(ctx context.Context, prompt string) *proxy.ProviderConfig {
	var emb Embedding
	var ok bool

	// ── 检查缓存 ──
	if emb, ok = r.cache.Get(prompt); !ok {
		var err error
		emb, err = r.embeddingSvc.Embed(ctx, prompt)
		if err != nil {
			log.Printf("[路由-语义] 嵌入生成失败: %v", err)
			return nil
		}
		r.cache.Set(prompt, emb)
	}

	// ── 读锁获取当前意图嵌入列表 ──
	r.intentMu.RLock()
	intents := r.intents
	r.intentMu.RUnlock()

	// ── 计算与所有意图的余弦相似度 ──
	type scored struct {
		ie    IntentEmbedding
		score float64
	}
	matches := make([]scored, len(intents))
	for i, ie := range intents {
		matches[i] = scored{ie, CosineSimilarity(emb, ie.Embedding)}
	}

	// 按相似度降序排列。
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	best := matches[0]

	// ── 阈值门控 ──
	if best.score < r.simThreshold {
		log.Printf("[路由-语义] 最佳匹配=%.3f (意图=%s) < 阈值=%.2f，穿透到下一层",
			best.score, best.ie.Intent.Name, r.simThreshold)
		return nil
	}

	// ── 查找供应商 ──
	prov, ok := r.providers[best.ie.Intent.Provider]
	if !ok || !prov.Enabled {
		log.Printf("[路由-语义] 意图 %s 的供应商 %s 不可用",
			best.ie.Intent.Name, best.ie.Intent.Provider)
		return nil
	}

	log.Printf("[路由-语义] → %-10s (意图=%-20s 相似度=%.3f)",
		prov.Name, best.ie.Intent.Name, best.score)
	return prov
}

// ────────────────────────────────────────────────────────────
// 策略 3：LLM 意图分类
// ────────────────────────────────────────────────────────────

// classify 使用 LLM 分类器确定意图并返回对应供应商。
//
// 流程：
//  1. 调用 classifier.Classify(ctx, prompt, intents)
//  2. 解析返回的意图名称
//  3. 查找对应供应商
//
// 返回 nil 表示：
//   - LLM API 调用失败（网络错误、超时等）
//   - 分类结果解析失败
//   - 分类结果对应的供应商不可用
func (r *HybridRouter) classify(ctx context.Context, prompt string) *proxy.ProviderConfig {
	r.intentMu.RLock()
	clIntents := r.classifyIntents
	r.intentMu.RUnlock()

	intentName, err := r.classifier.Classify(ctx, prompt, clIntents)
	if err != nil {
		log.Printf("[路由-LLM] 分类失败: %v，穿透到默认回退", err)
		return nil
	}

	if intentName == "" || intentName == "unknown" {
		log.Printf("[路由-LLM] 分类结果=%s，穿透到默认回退", intentName)
		return nil
	}

	// 通过意图名或意图代码找到对应的供应商。
	for _, intent := range clIntents {
		if intent.Name == intentName || intent.IntentCode == intentName {
			prov, ok := r.providers[intent.Provider]
			if !ok || !prov.Enabled {
				log.Printf("[路由-LLM] 意图 %s 的供应商 %s 不可用", intentName, intent.Provider)
				return nil
			}
			log.Printf("[路由-LLM] → %-10s (分类=%s)", prov.Name, intentName)
			return prov
		}
	}

	log.Printf("[路由-LLM] 未知意图名: %s", intentName)
	return nil
}

// ────────────────────────────────────────────────────────────
// 默认回退
// ────────────────────────────────────────────────────────────

// fallback 返回默认供应商，或任意启用的供应商。
func (r *HybridRouter) fallback() *proxy.ProviderConfig {
	if prov, ok := r.providers[r.defaultProvider]; ok && prov.Enabled {
		log.Printf("[路由-默认] → %s", prov.Name)
		return prov
	}
	for _, prov := range r.providers {
		if prov.Enabled {
			log.Printf("[路由-默认] → %s (首个可用)", prov.Name)
			return prov
		}
	}
	log.Printf("[路由-默认] 无可用供应商！")
	return nil
}

// ────────────────────────────────────────────────────────────
// 统计
// ────────────────────────────────────────────────────────────

// RouteStats 记录各路由策略的命中次数。
type RouteStats struct {
	KeywordHits    int64 `json:"keyword_hits"`    // 关键词命中次数
	EmbeddingHits  int64 `json:"embedding_hits"`  // Embedding 语义命中次数
	ClassifierHits int64 `json:"classifier_hits"` // LLM 分类命中次数
	FallbackHits   int64 `json:"fallback_hits"`   // 默认回退次数
}

// Stats 返回各策略的累积命中统计。
func (r *HybridRouter) Stats() RouteStats {
	return RouteStats{
		KeywordHits:    atomic.LoadInt64(&r.keywordHits),
		EmbeddingHits:  atomic.LoadInt64(&r.embeddingHits),
		ClassifierHits: atomic.LoadInt64(&r.classifierHits),
		FallbackHits:   atomic.LoadInt64(&r.fallbackHits),
	}
}

// CacheSize 返回嵌入向量缓存的当前条目数。
func (r *HybridRouter) CacheSize() int {
	return r.cache.Size()
}

// ────────────────────────────────────────────────────────────
// 类型别名（消除 import 歧义）
// ────────────────────────────────────────────────────────────

// IntentEmbedding 是意图与其预计算嵌入向量的组合。
//
// 在 Init() 阶段一次性生成，之后只读访问。
// 不额外加锁——Go 的 happens-before 保证（Init 完成先于 Match 调用）
// 确保 Match 中看到的 intentEmbs 是完整初始化的。
type IntentEmbedding struct {
	Intent    Intent
	Embedding Embedding
}

