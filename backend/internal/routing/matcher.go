// Package routing 提供基于规则的关键词匹配路由引擎。
//
// # 路由流程
//
//	用户提示词 + 请求模型名 → RuleEngine.Match() → 目标供应商
//
// 规则按优先级从高到低依次评估，首次匹配成功即返回。
// 如果没有任何规则命中，回退到默认供应商。
//
// # 规则设计
//
// 每条规则包含：
//   - Keywords：触发关键词（大小写不敏感，对提示词做子串匹配）
//   - Priority：优先级（数值越大越先评估）
//   - Provider：命中的目标供应商名称
//   - MinTokens：最小 Token 阈值（可选，短提示词不触发）
//
// # 扩展方向
//
// 当前实现是基于子串匹配的简单规则引擎。后续可以扩展为：
//   - Embedding 相似度匹配：将提示词向量化，与每个 intent 的
//     示例向量做余弦相似度比较
//   - LLM 分类器：用小模型快速分类提示词的 intent → 路由
//   - 复杂度评估：根据 Token 数、嵌套深度、技术术语密度评估问题复杂度
package routing

import (
	"context"
	"sort"
	"strings"

	"github.com/ragent/router/internal/proxy"
)

// ────────────────────────────────────────────────────────────
// 规则定义
// ────────────────────────────────────────────────────────────

// Rule 定义了从用户提示词到供应商的一条路由规则。
type Rule struct {
	// Name 是规则的可读名称，用于日志和调试。
	Name string `json:"name"`

	// Keywords 是触发此规则的关键词列表。
	// 匹配时对提示词做大小写不敏感的子串匹配。
	// 只要提示词包含任意一个关键词，规则即命中。
	Keywords []string `json:"keywords"`

	// Provider 是命中此规则后的目标供应商名称。
	Provider string `json:"provider"`

	// Priority 是规则优先级，数值越大越先评估。
	// 两条规则同时命中时，Priority 高的胜出。
	Priority int `json:"priority"`

	// MinTokens 是触发此规则的最小 Token 数阈值（估算值≈字符数/4）。
	// 设为 0 表示不限制。
	// 用途：简单问题即使包含关键词也不走复杂模型。
	MinTokens int `json:"min_tokens,omitempty"`
}

// ────────────────────────────────────────────────────────────
// 规则引擎
// ────────────────────────────────────────────────────────────

// RuleEngine 是基于优先级的关键词路由引擎。
//
// 规则集在创建时按优先级降序排列。
// Match 方法在规则列表中线性扫描，首次匹配即返回。
//
// 复杂度：O(N) 其中 N 是规则数。当前默认规则数为 6，
// 线性扫描足够。如果规则数增长到数百条，可考虑使用
// Aho-Corasick 或 trie 优化关键词匹配。
type RuleEngine struct {
	rules           []Rule                          // 按优先级降序排列的规则列表
	providers       map[string]*proxy.ProviderConfig // 供应商注册表
	defaultProvider string                          // 无规则命中时的回退供应商
}

// NewRuleEngine 创建规则引擎。
//
// 参数：
//   - rules：路由规则集（会被复制，不会被修改）
//   - providers：供应商名称 → 配置的映射
//   - defaultProvider：无规则命中时的默认供应商名称
func NewRuleEngine(rules []Rule, providers map[string]*proxy.ProviderConfig, defaultProvider string) *RuleEngine {
	// 复制并排序（不修改调用方的切片）。
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	return &RuleEngine{
		rules:           sorted,
		providers:       providers,
		defaultProvider: defaultProvider,
	}
}

// Match 根据用户提示词和请求模型名选择目标供应商。
//
// 匹配逻辑（按顺序，首个成功即返回）：
//  1. 按优先级从高到低匹配规则
//  2. 如果请求了特定模型（model != "" 且 != "auto"），
//     寻找名称中包含该模型名的供应商
//  3. 回退到默认供应商
//  4. 返回任意一个已启用的供应商
//
// 返回值 nil 表示没有任何可用供应商。
// Match 实现基于关键词规则的路由匹配。
// ctx 参数为接口统一保留，关键词匹配不需要 context（纯内存计算）。
//
// 仅做关键词匹配，未命中返回 nil。
// 默认回退逻辑已移至 HybridRouter.Match() 的策略 4 层。
func (e *RuleEngine) Match(_ context.Context, prompt string, model string) *proxy.ProviderConfig {
	promptLower := strings.ToLower(prompt)

	// 阶段 1：按优先级从高到低匹配关键词规则。
	for _, rule := range e.rules {
		if !e.ruleMatches(rule, promptLower) {
			continue
		}
		if prov, ok := e.providers[rule.Provider]; ok && prov.Enabled {
			return prov
		}
	}

	// 阶段 2：如果请求了特定模型（model != "" 且 != "auto"），
	// 寻找名称中包含该模型名的供应商。
	if model != "" && model != "auto" {
		for _, prov := range e.providers {
			if prov.Enabled && strings.Contains(strings.ToLower(model), strings.ToLower(prov.Name)) {
				return prov
			}
		}
	}

	// 无关键词命中 → 返回 nil，由上层 HybridRouter 继续尝试其他策略。
	return nil
}

// ruleMatches 检查一条规则是否匹配给定提示词。
func (e *RuleEngine) ruleMatches(rule Rule, promptLower string) bool {
	// 检查 Token 阈值约束。
	if rule.MinTokens > 0 {
		// 估算：平均 1 Token ≈ 4 个英文字符（中文 ≈ 1.5 个字符/Token，
		// 但为了简单使用统一估算）。
		estimatedTokens := len(promptLower) / 4
		if estimatedTokens < rule.MinTokens {
			return false
		}
	}

	// 检查关键词（子串匹配，大小写不敏感）。
	if len(rule.Keywords) == 0 {
		return false
	}
	for _, kw := range rule.Keywords {
		if strings.Contains(promptLower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────
// 运行时规则管理
// ────────────────────────────────────────────────────────────

// AddRule 动态添加规则并自动按优先级重排。
// 注意：仅用于初始化阶段，不支持与 Match 并发调用。
func (e *RuleEngine) AddRule(rule Rule) {
	e.rules = append(e.rules, rule)
	sort.Slice(e.rules, func(i, j int) bool {
		return e.rules[i].Priority > e.rules[j].Priority
	})
}

// RemoveRule 按名称删除规则。
// 注意：仅用于初始化阶段，不支持与 Match 并发调用。
func (e *RuleEngine) RemoveRule(name string) {
	for i, r := range e.rules {
		if r.Name == name {
			e.rules = append(e.rules[:i], e.rules[i+1:]...)
			return
		}
	}
}

// Rules 返回所有规则（按优先级降序）。
func (e *RuleEngine) Rules() []Rule {
	result := make([]Rule, len(e.rules))
	copy(result, e.rules)
	return result
}

// ────────────────────────────────────────────────────────────
// 默认规则集
// ────────────────────────────────────────────────────────────

// DefaultRules 返回一套面向 AI 编码助手的默认规则。
//
// 设计原则：
//   - 复杂任务（架构、调试、代码生成）→ Claude（质量优先）
//   - 简单任务（问答、文档）→ DeepSeek（成本优先）
//   - 未命中 → DeepSeek（成本最敏感）
func DefaultRules() []Rule {
	return []Rule{
		{
			Name:     "架构与设计",
			Keywords: []string{"architecture", "design", "refactor", "架构", "设计", "重构", "system design", "distributed", "microservice"},
			Provider: "Claude",
			Priority: 100,
		},
		{
			Name:     "Bug 修复与调试",
			Keywords: []string{"bug", "fix", "debug", "error", "修复", "调试", "issue", "crash", "exception"},
			Provider: "Claude",
			Priority: 90,
		},
		{
			Name:     "代码生成",
			Keywords: []string{"generate", "create", "implement", "生成", "创建", "write code", "build a"},
			Provider: "Claude",
			Priority: 80,
		},
		{
			Name:     "复杂分析",
			Keywords: []string{"analyze", "analysis", "review", "explain", "分析", "审查"},
			Provider: "Claude",
			Priority: 70,
			// 仅长提示词触发，短问题走 DeepSeek。
			MinTokens: 300,
		},
		{
			Name:     "简单问答",
			Keywords: []string{"explain", "what is", "how to", "解释", "什么是", "how does", "why is"},
			Provider: "DeepSeek",
			Priority: 50,
		},
		{
			Name:     "文档",
			Keywords: []string{"document", "readme", "doc", "comment", "文档", "README", "documentation"},
			Provider: "DeepSeek",
			Priority: 40,
		},
	}
}
