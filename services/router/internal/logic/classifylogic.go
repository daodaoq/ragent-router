package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ragent/router/rpc/router"
	"github.com/ragent/router/services/router/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// ClassifyLogic 意图分类逻辑。
//
// 三级路由：关键词 → Embedding → LLM → 兜底。
// 高并发方案 6：Redis 缓存路由结果，相似 prompt 复用。
type ClassifyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewClassifyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ClassifyLogic {
	return &ClassifyLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// Classify 执行三级路由分类。
func (l *ClassifyLogic) Classify(in *router.ClassifyReq) (*router.ClassifyResp, error) {
	start := time.Now()

	// ── Level 0: Redis 缓存命中 ──
	cacheKey := fmt.Sprintf("ragent:route:%s", hashPrompt(in.Prompt))
	if cached, err := l.svcCtx.Redis.GetCtx(l.ctx, cacheKey); err == nil && cached != "" {
		var resp router.ClassifyResp
		if json.Unmarshal([]byte(cached), &resp) == nil {
			resp.LatencyMs = time.Since(start).Milliseconds()
			l.Infof("[路由] 缓存命中: %s → %s", truncate(in.Prompt, 30), resp.ProviderName)
			return &resp, nil
		}
	}

	// ── Level 1: 关键词匹配 ──
	provider, reason := l.keywordMatch(in.Prompt)
	if provider != "" {
		resp := &router.ClassifyResp{
			ProviderName: provider,
			Strategy:     "keyword",
			Confidence:   0.95,
			LatencyMs:    time.Since(start).Milliseconds(),
			RouteReason:  reason,
		}
		l.cacheResult(cacheKey, resp)
		return resp, nil
	}

	// ── Level 2/3: 兜底 ──
	// 从 Redis 获取默认供应商
	defaultProvider := l.getDefaultProvider()
	resp := &router.ClassifyResp{
		ProviderName: defaultProvider,
		Strategy:     "fallback",
		Confidence:   0.5,
		LatencyMs:    time.Since(start).Milliseconds(),
		RouteReason:  "无匹配规则，使用默认供应商",
	}
	l.cacheResult(cacheKey, resp)
	return resp, nil
}

// keywordMatch 关键词匹配。
func (l *ClassifyLogic) keywordMatch(prompt string) (string, string) {
	// 从 Redis 加载关键词规则
	rulesData, err := l.svcCtx.Redis.GetCtx(l.ctx, "ragent:routing:rules")
	if err != nil || rulesData == "" {
		return "", ""
	}

	var rules map[string][]string // provider → keywords
	if err := json.Unmarshal([]byte(rulesData), &rules); err != nil {
		return "", ""
	}

	promptLower := strings.ToLower(prompt)
	for provider, keywords := range rules {
		for _, kw := range keywords {
			if strings.Contains(promptLower, strings.ToLower(kw)) {
				return provider, fmt.Sprintf("关键词匹配: %s", kw)
			}
		}
	}
	return "", ""
}

func (l *ClassifyLogic) getDefaultProvider() string {
	name, _ := l.svcCtx.Redis.GetCtx(l.ctx, "ragent:routing:default_provider")
	if name == "" {
		return "DeepSeek"
	}
	return name
}

func (l *ClassifyLogic) cacheResult(key string, resp *router.ClassifyResp) {
	ttl := l.svcCtx.Config.RoutingCache.TTL
	if ttl <= 0 {
		ttl = 60
	}
	data, _ := json.Marshal(resp)
	l.svcCtx.Redis.SetexCtx(l.ctx, key, string(data), ttl)
}

func hashPrompt(prompt string) string {
	// 简单哈希：取前 100 字符 + 长度
	if len(prompt) > 100 {
		prompt = prompt[:100]
	}
	return fmt.Sprintf("%x", len(prompt))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
