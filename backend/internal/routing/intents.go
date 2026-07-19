// Package routing — 意图定义
//
// 意图树由用户通过 Dashboard 自定义管理，持久化在 SQLite 中。
// 路由引擎从 DB 加载启用的叶子节点（level=2 + enabled + 有 provider）参与匹配。
//
// # 树形结构
//
//	level=0 Root     — 顶层域
//	level=1 Category — 分类容器（不参与路由）
//	level=2 Leaf     — 叶子意图（参与 Embedding 匹配 + LLM 分类）
//
// # 热重载
//
// 用户修改意图树后调用 HybridRouter.ReloadIntents() 即可生效，
// 无需重启服务。新意图的 Embedding 向量会在重载时预热。
//
// # 默认种子数据
//
// 首次启动时自动写入 10 个节点（2 容器 + 8 叶子），
// 覆盖架构设计/Bug调试/代码生成/代码审查→Claude，简单问答/文档→DeepSeek。
// 种子数据定义在 store/intent_store.go 的 SeedDefaults() 中。
package routing

// ────────────────────────────────────────────────────────────
// 意图类型定义
// ────────────────────────────────────────────────────────────

// Intent 描述一个参与路由的叶子意图。
//
// 字段说明：
//   - IntentCode：唯一标识，如 "t-arch"、"t-debug"
//   - Name：显示名称，如 "架构设计"
//   - Description：语义描述（中英双语关键词），用于生成 Embedding 向量
//   - Examples：典型问法示例，拼接后送入 Embedding API
//   - Provider：命中此意图后的目标供应商名称
//   - Priority：优先级（多个意图匹配时选 Priority 最高的）
type Intent struct {
	IntentCode  string   `json:"intent_code"`  // 唯一标识
	Name        string   `json:"name"`         // 显示名称
	Description string   `json:"description"`  // 语义描述
	Examples    []string `json:"examples"`     // 典型问法
	Provider    string   `json:"provider"`     // 目标供应商名称
	Priority    int      `json:"priority"`     // 优先级（越高越优先）
}

// IntentNode 是意图树节点（含子节点，用于 API 序列化）。
type IntentNode struct {
	IntentCode  string        `json:"intent_code"`
	ParentCode  *string       `json:"parent_code"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Examples    []string      `json:"examples"`
	Level       int           `json:"level"`
	ProviderID  *string       `json:"provider_id"`
	Enabled     bool          `json:"enabled"`
	Children    []*IntentNode `json:"children,omitempty"`
}

// ────────────────────────────────────────────────────────────
// 工具函数
// ────────────────────────────────────────────────────────────

// FlattenLeaves 从意图树中提取所有启用的叶子节点。
//
// 叶子定义：level=2 且 Enabled=true 且 Provider 非空。
// ProviderID 被映射为 Intent.Provider（供应商名称由调用方通过
// provider_id→name 映射表转换后传入）。
//
// 返回的 Intent 列表可直接传给 HybridRouter.ReloadIntents()。
func FlattenLeaves(nodes []*IntentNode, providerIDToName map[string]string) []Intent {
	var result []Intent
	var walk func(ns []*IntentNode)
	walk = func(ns []*IntentNode) {
		for _, n := range ns {
			if n.Level == 2 && n.Enabled && n.ProviderID != nil && *n.ProviderID != "" {
				providerName := "unknown"
				if name, ok := providerIDToName[*n.ProviderID]; ok {
					providerName = name
				}
				result = append(result, Intent{
					IntentCode:  n.IntentCode,
					Name:        n.Name,
					Description: n.Description,
					Examples:    n.Examples,
					Provider:    providerName,
					Priority:    100 - len(result), // 按遍历顺序递减优先级
				})
			}
			if n.Children != nil {
				walk(n.Children)
			}
		}
	}
	walk(nodes)
	return result
}

// ────────────────────────────────────────────────────────────
// 默认意图集（已废弃——仅作 fallback 参考）
// ────────────────────────────────────────────────────────────

// DefaultIntents 返回硬编码的默认意图集。
//
// 注意：生产路由使用 IntentStore 中的持久化数据。
// 此函数仅在 DB 不可用时的极端回退场景中使用，
// 或作为首次种子数据的参考模板。
func DefaultIntents() []Intent {
	return []Intent{
		{
			IntentCode:  "t-arch",
			Name:        "架构设计",
			Description: "系统架构设计、技术选型、重构方案、分布式系统设计、微服务拆分、数据库设计、API设计、高并发方案、system design、architecture、refactor、distributed、microservice、scalability",
			Examples:    []string{"设计一个支持百万并发的秒杀系统架构", "如何将单体应用拆分为微服务", "数据库分库分表的方案选型", "Design a highly available API gateway"},
			Provider:    "Claude",
			Priority:    100,
		},
		{
			IntentCode:  "t-debug",
			Name:        "Bug 调试",
			Description: "Bug排查、错误调试、异常分析、崩溃定位、性能瓶颈排查、内存泄漏、死锁、并发问题、debug、troubleshoot、error、crash、exception、race condition、deadlock、memory leak",
			Examples:    []string{"这段代码在高并发下偶发panic，帮我排查", "帮我定位这个死锁问题", "为什么这个goroutine会泄漏", "Debug this race condition in my Go code"},
			Provider:    "Claude",
			Priority:    90,
		},
		{
			IntentCode:  "t-codegen",
			Name:        "代码生成",
			Description: "编写代码、生成函数、实现功能、创建项目、写脚本、写单测、generate code、implement、create function、write unit test、build API",
			Examples:    []string{"帮我写一个带超时和重试的HTTP客户端", "实现一个LRU缓存", "生成这个接口的单元测试", "Implement a token bucket rate limiter in Go"},
			Provider:    "Claude",
			Priority:    80,
		},
		{
			IntentCode:  "t-review",
			Name:        "代码审查",
			Description: "代码审查、代码优化、重构建议、安全审计、性能优化、最佳实践、code review、refactor、optimize、security audit、best practice、code smell、clean code",
			Examples:    []string{"帮我review这段代码有没有安全隐患", "这个函数的性能还能优化吗", "这段代码有什么设计问题", "Review this code for security vulnerabilities"},
			Provider:    "Claude",
			Priority:    70,
		},
		{
			IntentCode:  "t-qa",
			Name:        "简单问答",
			Description: "简单问答、概念解释、知识查询、基础用法、快速参考、入门教程、what is、how to、explain、概念、定义、区别、对比",
			Examples:    []string{"什么是RESTful API", "Go的defer怎么用", "Redis和MySQL有什么区别", "What is dependency injection"},
			Provider:    "DeepSeek",
			Priority:    50,
		},
		{
			IntentCode:  "t-docs",
			Name:        "文档编写",
			Description: "编写文档、生成注释、写README、API文档、技术说明、documentation、readme、doc、comment、API reference、changelog",
			Examples:    []string{"帮我写这个项目的README", "给这个函数生成JSDoc注释", "生成API接口文档", "Write API documentation for this endpoint"},
			Provider:    "DeepSeek",
			Priority:    40,
		},
	}
}
