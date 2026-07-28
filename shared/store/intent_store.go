// Package store — 意图树持久化
//
// 将用户自定义的意图层级结构存储在 SQLite 中。
// 前端 IntentPanel 通过 REST API 对意图树做 CRUD，
// 路由引擎从 DB 加载叶子节点参与三阶段混合路由。
//
// # 树形结构
//
//	level=0 Root     — 顶层域（如"编程助手"）
//	level=1 Category — 分类（如"复杂任务"、"简单任务"）
//	level=2 Leaf     — 叶子意图（如"架构设计"→Claude、"简单问答"→DeepSeek）
//
// 只有 level=2 且 provider_id 非空 + enabled=true 的叶子节点参与路由。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ────────────────────────────────────────────────────────────
// 数据模型
// ────────────────────────────────────────────────────────────

// IntentNodeRecord 是意图树中的一个节点（数据库行格式）。
type IntentNodeRecord struct {
	ID          int64     `json:"id"`           // 自增主键
	IntentCode  string    `json:"intent_code"`  // 唯一标识，如 "t-arch", "t-debug"
	ParentCode  *string   `json:"parent_code"`  // 父节点 intent_code（nil=根节点）
	Name        string    `json:"name"`         // 显示名称
	Description string    `json:"description"`  // 语义描述（AI 分类依据）
	Examples    string    `json:"examples"`     // JSON 数组字符串
	Level       int       `json:"level"`        // 0=Domain, 1=Category, 2=Topic(Leaf)
	ProviderID  *string   `json:"provider_id"`  // 绑定的供应商 ID（仅叶子节点）
	Enabled     bool      `json:"enabled"`      // 是否启用
	SortOrder   int       `json:"sort_order"`   // 同级排序
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IntentNode 是带子节点的树形结构（API 响应格式）。
type IntentNode struct {
	ID          int64         `json:"id"`
	IntentCode  string        `json:"intent_code"`
	ParentCode  *string       `json:"parent_code"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Examples    []string      `json:"examples"`
	Level       int           `json:"level"`
	ProviderID  *string       `json:"provider_id"`
	Enabled     bool          `json:"enabled"`
	SortOrder   int           `json:"sort_order"`
	Children    []*IntentNode `json:"children,omitempty"`
}

// ToTree 将平铺的记录列表构建为嵌套树。
//
// 算法：两次遍历。
//  1. 建立 code → node 的映射
//  2. 遍历所有节点，根据 parent_code 挂到父节点的 Children 下
//
// 根节点：parent_code 为空或 parent_code 指向不存在的节点。
func ToTree(records []IntentNodeRecord) []*IntentNode {
	// ── 第一步：转换并建立索引 ──
	nodeMap := make(map[string]*IntentNode, len(records))
	for _, r := range records {
		var examples []string
		if r.Examples != "" {
			if err := json.Unmarshal([]byte(r.Examples), &examples); err != nil {
				// 解析失败时使用空数组，避免因脏数据导致 panic
				examples = []string{}
			}
		}
		if examples == nil {
			examples = []string{}
		}
		nodeMap[r.IntentCode] = &IntentNode{
			ID:          r.ID,
			IntentCode:  r.IntentCode,
			ParentCode:  r.ParentCode,
			Name:        r.Name,
			Description: r.Description,
			Examples:    examples,
			Level:       r.Level,
			ProviderID:  r.ProviderID,
			Enabled:     r.Enabled,
			SortOrder:   r.SortOrder,
			Children:    []*IntentNode{},
		}
	}

	// ── 第二步：挂载子节点 ──
	var roots []*IntentNode
	for _, node := range nodeMap {
		if node.ParentCode == nil || *node.ParentCode == "" {
			roots = append(roots, node)
		} else if parent, ok := nodeMap[*node.ParentCode]; ok {
			parent.Children = append(parent.Children, node)
		} else {
			// 父节点不存在 → 提升为根节点。
			roots = append(roots, node)
		}
	}
	return roots
}

// ────────────────────────────────────────────────────────────
// IntentStore —— SQLite 持久化
// ────────────────────────────────────────────────────────────

// IntentStore 管理意图树的持久化存储。
type IntentStore struct {
	db *sql.DB
}

// NewIntentStore 创建意图存储实例并执行建表迁移。
func NewIntentStore(db *sql.DB) (*IntentStore, error) {
	if err := migrateIntents(db); err != nil {
		return nil, fmt.Errorf("migrate intents: %w", err)
	}
	return &IntentStore{db: db}, nil
}

// ── 查询 ──

// ListAll 返回所有意图节点（按 sort_order ASC）。
func (s *IntentStore) ListAll() ([]IntentNodeRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, intent_code, parent_code, name, description, examples,
		       level, provider_id, enabled, sort_order, created_at, updated_at
		FROM intent_nodes ORDER BY sort_order ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list intents: %w", err)
	}
	defer rows.Close()

	var result []IntentNodeRecord
	for rows.Next() {
		var r IntentNodeRecord
		err := rows.Scan(&r.ID, &r.IntentCode, &r.ParentCode, &r.Name, &r.Description,
			&r.Examples, &r.Level, &r.ProviderID, &r.Enabled, &r.SortOrder,
			&r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan intent: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetByCode 按 intent_code 获取单个节点。
func (s *IntentStore) GetByCode(code string) (*IntentNodeRecord, error) {
	var r IntentNodeRecord
	err := s.db.QueryRow(`
		SELECT id, intent_code, parent_code, name, description, examples,
		       level, provider_id, enabled, sort_order, created_at, updated_at
		FROM intent_nodes WHERE intent_code = ?
	`, code).Scan(&r.ID, &r.IntentCode, &r.ParentCode, &r.Name, &r.Description,
		&r.Examples, &r.Level, &r.ProviderID, &r.Enabled, &r.SortOrder,
		&r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get intent %s: %w", code, err)
	}
	return &r, nil
}

// ListLeaves 返回所有启用的叶子节点（level=2, enabled=true, provider_id 非空）。
func (s *IntentStore) ListLeaves() ([]IntentNodeRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, intent_code, parent_code, name, description, examples,
		       level, provider_id, enabled, sort_order, created_at, updated_at
		FROM intent_nodes
		WHERE level = 2 AND enabled = 1 AND provider_id IS NOT NULL AND provider_id != ''
		ORDER BY sort_order ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list leaves: %w", err)
	}
	defer rows.Close()

	var result []IntentNodeRecord
	for rows.Next() {
		var r IntentNodeRecord
		err := rows.Scan(&r.ID, &r.IntentCode, &r.ParentCode, &r.Name, &r.Description,
			&r.Examples, &r.Level, &r.ProviderID, &r.Enabled, &r.SortOrder,
			&r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan leaf: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ── 写入 ──

// Insert 创建新节点。
func (s *IntentStore) Insert(r *IntentNodeRecord) error {
	now := time.Now()
	r.CreatedAt = now
	r.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO intent_nodes
			(intent_code, parent_code, name, description, examples,
			 level, provider_id, enabled, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.IntentCode, r.ParentCode, r.Name, r.Description, r.Examples,
		r.Level, r.ProviderID, r.Enabled, r.SortOrder, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert intent %s: %w", r.IntentCode, err)
	}
	return nil
}

// Update 更新已有节点。
func (s *IntentStore) Update(code string, r *IntentNodeRecord) error {
	r.UpdatedAt = time.Now()
	_, err := s.db.Exec(`
		UPDATE intent_nodes
		SET parent_code=?, name=?, description=?, examples=?,
		    level=?, provider_id=?, enabled=?, sort_order=?, updated_at=?
		WHERE intent_code=?
	`, r.ParentCode, r.Name, r.Description, r.Examples,
		r.Level, r.ProviderID, r.Enabled, r.SortOrder, r.UpdatedAt, code)
	if err != nil {
		return fmt.Errorf("update intent %s: %w", code, err)
	}
	return nil
}

// Delete 删除节点及其所有子节点（级联删除，事务保护）。
func (s *IntentStore) Delete(code string) error {
	// 先递归收集所有需要删除的节点 code。
	codes, err := s.collectSubtreeCodes(code)
	if err != nil {
		return err
	}

	// 在事务中批量删除，中间失败自动回滚。
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, c := range codes {
		if _, err := tx.Exec("DELETE FROM intent_nodes WHERE intent_code = ?", c); err != nil {
			return fmt.Errorf("delete intent %s: %w", c, err)
		}
	}
	return tx.Commit()
}

// collectSubtreeCodes 收集指定节点及其所有后代节点的 intent_code。
func (s *IntentStore) collectSubtreeCodes(root string) ([]string, error) {
	codes := []string{root}
	// BFS：逐层收集子节点。
	queue := []string{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		rows, err := s.db.Query(
			"SELECT intent_code FROM intent_nodes WHERE parent_code = ?", parent)
		if err != nil {
			return nil, fmt.Errorf("collect children of %s: %w", parent, err)
		}
		for rows.Next() {
			var child string
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return nil, err
			}
			codes = append(codes, child)
			queue = append(queue, child)
		}
		rows.Close()
	}
	return codes, nil
}

// Count 返回节点总数。
func (s *IntentStore) Count() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(1) FROM intent_nodes").Scan(&n)
	return n, err
}

// ────────────────────────────────────────────────────────────
// 种子数据
// ────────────────────────────────────────────────────────────

// SeedDefaults 写入默认意图树（仅在表为空时执行）。
//
// 注意：此处的意图数据应与 routing/intents.go 的 DefaultIntents() 保持同步。
// 如果修改了任一处，请同步更新另一处。两处的字段关系：
//
//	DefaultIntents.Provider   (供应商名称)  ↔ provider_id (DB 外键)
//	DefaultIntents.Description + Examples  ↔ intent_nodes.description + examples
//	DefaultIntents.IntentCode / Name       ↔ intent_nodes.intent_code / name
//
// 默认结构：
//
//	编程助手 (Root)
//	├── 复杂任务 (Category)
//	│   ├── 架构设计 → Claude
//	│   ├── Bug 调试 → Claude
//	│   ├── 代码生成 → Claude
//	│   └── 代码审查 → Claude
//	└── 简单任务 (Category)
//	    ├── 简单问答 → DeepSeek
//	    └── 文档编写 → DeepSeek
func (s *IntentStore) SeedDefaults() error {
	count, err := s.Count()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // 已有数据，跳过。
	}

	rootParent := "root"
	complexParent := "complex"
	simpleParent := "simple"

	nodes := []IntentNodeRecord{
		// ── 容器节点 ──
		{IntentCode: rootParent, Name: "编程助手", Description: "AI 编码意图分类根节点",
			Level: 0, Enabled: true, SortOrder: 0},
		{IntentCode: complexParent, ParentCode: strPtr(rootParent),
			Name: "复杂任务", Description: "需要强推理能力的任务",
			Level: 1, Enabled: true, SortOrder: 1},
		{IntentCode: simpleParent, ParentCode: strPtr(rootParent),
			Name: "简单任务", Description: "低成本、快速响应的任务",
			Level: 1, Enabled: true, SortOrder: 2},

		// ── 复杂任务 ← Claude ──
		{IntentCode: "t-arch", ParentCode: strPtr(complexParent),
			Name: "架构设计", Level: 2, Enabled: true, SortOrder: 10,
			ProviderID: strPtr("claude-default"),
			Description: "系统架构设计、技术选型、重构方案、分布式系统设计、微服务拆分、数据库设计、API设计、高并发方案、system design、architecture、refactor、distributed、microservice、scalability",
			Examples:    jsonArr("设计一个支持百万并发的秒杀系统架构", "如何将单体应用拆分为微服务", "数据库分库分表的方案选型", "Design a highly available API gateway")},
		{IntentCode: "t-debug", ParentCode: strPtr(complexParent),
			Name: "Bug 调试", Level: 2, Enabled: true, SortOrder: 20,
			ProviderID: strPtr("claude-default"),
			Description: "Bug排查、错误调试、异常分析、崩溃定位、性能瓶颈排查、内存泄漏、死锁、并发问题、debug、troubleshoot、error、crash、exception、race condition、deadlock、memory leak",
			Examples:    jsonArr("这段代码在高并发下偶发panic，帮我排查", "帮我定位这个死锁问题", "为什么这个goroutine会泄漏", "Debug this race condition in my Go code")},
		{IntentCode: "t-codegen", ParentCode: strPtr(complexParent),
			Name: "代码生成", Level: 2, Enabled: true, SortOrder: 30,
			ProviderID: strPtr("claude-default"),
			Description: "编写代码、生成函数、实现功能、创建项目、写脚本、写单测、generate code、implement、create function、write unit test、build API",
			Examples:    jsonArr("帮我写一个带超时和重试的HTTP客户端", "实现一个LRU缓存", "生成这个接口的单元测试", "Implement a token bucket rate limiter in Go")},
		{IntentCode: "t-review", ParentCode: strPtr(complexParent),
			Name: "代码审查", Level: 2, Enabled: true, SortOrder: 40,
			ProviderID: strPtr("claude-default"),
			Description: "代码审查、代码优化、重构建议、安全审计、性能优化、最佳实践、code review、refactor、optimize、security audit、best practice、code smell、clean code",
			Examples:    jsonArr("帮我review这段代码有没有安全隐患", "这个函数的性能还能优化吗", "这段代码有什么设计问题", "Review this code for security vulnerabilities")},

		// ── 简单任务 ← DeepSeek ──
		{IntentCode: "t-qa", ParentCode: strPtr(simpleParent),
			Name: "简单问答", Level: 2, Enabled: true, SortOrder: 10,
			ProviderID: strPtr("deepseek-default"),
			Description: "简单问答、概念解释、知识查询、基础用法、快速参考、入门教程、what is、how to、explain、概念、定义、区别、对比",
			Examples:    jsonArr("什么是RESTful API", "Go的defer怎么用", "Redis和MySQL有什么区别", "What is dependency injection")},
		{IntentCode: "t-docs", ParentCode: strPtr(simpleParent),
			Name: "文档编写", Level: 2, Enabled: true, SortOrder: 20,
			ProviderID: strPtr("deepseek-default"),
			Description: "编写文档、生成注释、写README、API文档、技术说明、documentation、readme、doc、comment、API reference、changelog",
			Examples:    jsonArr("帮我写这个项目的README", "给这个函数生成JSDoc注释", "生成API接口文档", "Write API documentation for this endpoint")},
	}

	for i := range nodes {
		if err := s.Insert(&nodes[i]); err != nil {
			return fmt.Errorf("seed intent %s: %w", nodes[i].IntentCode, err)
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────
// 辅助函数
// ────────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

func jsonArr(items ...string) string {
	b, _ := json.Marshal(items)
	return string(b)
}

// ────────────────────────────────────────────────────────────
// 数据库迁移
// ────────────────────────────────────────────────────────────

// migrateIntents 创建 intent_nodes 表（幂等）。
func migrateIntents(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS intent_nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		intent_code TEXT NOT NULL UNIQUE,
		parent_code TEXT DEFAULT NULL,
		name TEXT NOT NULL,
		description TEXT DEFAULT '',
		examples TEXT DEFAULT '[]',
		level INTEGER NOT NULL DEFAULT 2,
		provider_id TEXT DEFAULT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_intent_nodes_parent ON intent_nodes(parent_code);
	CREATE INDEX IF NOT EXISTS idx_intent_nodes_level ON intent_nodes(level);
	`
	_, err := db.Exec(schema)
	return err
}

// compactPrompt 截断提示词用于展示（参见 sqlite.go 中的 CompactPrompt）。
// 按 rune 计数避免中文截断乱码。
func compactPrompt(prompt string, maxLen int) string {
	return CompactPrompt(prompt, maxLen)
}
