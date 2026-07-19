package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	// modernc.org/sqlite 是纯 Go 实现的 SQLite 驱动，无需 CGO。
	// 选择原因：零 C 依赖，跨平台编译无痛，适合本地嵌入式数据库场景。
	_ "modernc.org/sqlite"
)

// LogStore 是请求日志的 SQLite 持久化存储。
//
// # 为什么用 SQLite
//
//   - 本地 Dashboard 场景不需要独立的数据库服务
//   - 纯 Go 驱动，无 CGO，交叉编译无痛
//   - WAL 模式 + 单连接足够支撑 Dashboard 的读负载
//   - 零运维成本——不需要额外安装 MySQL/PostgreSQL
//
// # 连接管理
//
// SQLite 是单写者模型，SetMaxOpenConns(1) 避免写冲突。
// WAL 模式下读不阻塞写，Dashboard 查询不影响代理日志写入。
type LogStore struct {
	db *sql.DB
}

// NewLogStore 打开（或创建）SQLite 数据库并自动建表。
//
// 连接字符串参数说明：
//   - _journal_mode=WAL：Write-Ahead Logging，读不阻塞写
//   - _busy_timeout=5000：写入冲突时等待 5 秒而非立即返回 SQLITE_BUSY
func NewLogStore(path string) (*LogStore, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// WAL 模式下允许多个读连接并发，1 个写连接即可。
	// 开 4 个连接允许 Dashboard 查询与日志写入并行。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &LogStore{db: db}, nil
}

// Close 关闭数据库连接。
func (s *LogStore) Close() error {
	return s.db.Close()
}

// DB 返回底层 *sql.DB（供 IntentStore 等复用同一连接）。
func (s *LogStore) DB() *sql.DB {
	return s.db
}

// ────────────────────────────────────────────────────────────
// 写入操作
// ────────────────────────────────────────────────────────────

// Insert 保存一条请求日志。
func (s *LogStore) Insert(log *RequestLogRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO request_logs
			(id, prompt, prompt_tokens, completion_tokens, total_tokens,
			 cache_read_tokens, cache_creation_tokens, model, provider,
			 upstream_url, route_reason, status, error_detail,
			 upstream_request_id, cost_usd, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		log.ID, log.Prompt, log.PromptTokens, log.CompletionTokens,
		log.TotalTokens, log.CacheReadTokens, log.CacheCreationTokens,
		log.Model, log.Provider, log.UpstreamURL, log.RouteReason,
		log.Status, log.ErrorDetail, log.UpstreamRequestID,
		log.CostUSD, log.LatencyMs, log.CreatedAt,
	)
	return err
}

// ────────────────────────────────────────────────────────────
// Dashboard 查询
// ────────────────────────────────────────────────────────────

// DashboardOverview 返回 Dashboard 首页的聚合统计数据。
//
// 计算逻辑：
//   - todayCost：当日所有请求的费用总和
//   - monthCost：本月所有请求的费用总和
//   - totalRequests：历史总请求数
//   - savedAmount：通过规则路由到低价模型而节省的费用（估算为月费的 25%）
func (s *LogStore) DashboardOverview() (*DashboardOverview, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var overview DashboardOverview

	// 今日费用
	err := s.db.QueryRow(
		"SELECT COALESCE(SUM(cost_usd), 0) FROM request_logs WHERE created_at >= ?",
		todayStart,
	).Scan(&overview.TodayCost)
	if err != nil {
		return nil, fmt.Errorf("today cost: %w", err)
	}

	// 本月费用
	err = s.db.QueryRow(
		"SELECT COALESCE(SUM(cost_usd), 0) FROM request_logs WHERE created_at >= ?",
		monthStart,
	).Scan(&overview.MonthCost)
	if err != nil {
		return nil, fmt.Errorf("month cost: %w", err)
	}

	// 总请求数
	err = s.db.QueryRow("SELECT COUNT(1) FROM request_logs").Scan(&overview.TotalRequests)
	if err != nil {
		return nil, fmt.Errorf("total requests: %w", err)
	}

	// 节省估算：对比"全部走 Claude"与"实际混合路由"的费用差。
	// 本月如果全部请求走 Claude 的费用 ≈ 总Token数 × Claude 费率。
	var totalPrompt, totalCompletion int64
	s.db.QueryRow(
		"SELECT COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0) FROM request_logs WHERE created_at >= ?",
		monthStart,
	).Scan(&totalPrompt, &totalCompletion)
	// Claude 费率: $3.00/M input, $15.00/M output
	estimatedClaudeCost := (float64(totalPrompt)*3.00 + float64(totalCompletion)*15.00) / 1_000_000
	overview.SavedAmount = estimatedClaudeCost - overview.MonthCost
	if overview.SavedAmount < 0 {
		overview.SavedAmount = 0
	}
	if estimatedClaudeCost > 0 {
		overview.SavingRate = float64(int(overview.SavedAmount/estimatedClaudeCost*1000)) / 10 // 保留1位小数
	} else {
		overview.SavingRate = 0
	}

	return &overview, nil
}

// ModelDistribution 返回各模型的请求分布（按请求次数降序）。
func (s *LogStore) ModelDistribution() ([]ModelDistribution, error) {
	rows, err := s.db.Query(`
		SELECT model, COUNT(1) as cnt
		FROM request_logs
		GROUP BY model
		ORDER BY cnt DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("model distribution: %w", err)
	}
	defer rows.Close()

	var total int64
	type item struct {
		Model string
		Count int64
	}
	var items []item

	for rows.Next() {
		var i item
		if err := rows.Scan(&i.Model, &i.Count); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		items = append(items, i)
		total += i.Count
	}

	// 计算百分比。
	var result []ModelDistribution
	for _, it := range items {
		pct := float64(0)
		if total > 0 {
			pct = float64(it.Count) / float64(total) * 100
		}
		result = append(result, ModelDistribution{
			Model:      it.Model,
			Count:      it.Count,
			Percentage: float64(int(pct*10)) / 10, // 保留 1 位小数
		})
	}
	return result, nil
}

// RecentRoutes 返回最近 N 条请求日志（按时间降序）。
func (s *LogStore) RecentRoutes(limit int) ([]RecentRoute, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT id, prompt, model, provider, route_reason, cost_usd, latency_ms, created_at
		FROM request_logs
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent routes: %w", err)
	}
	defer rows.Close()

	var result []RecentRoute
	for rows.Next() {
		var r RecentRoute
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.Prompt, &r.Model, &r.Provider,
			&r.RouteReason, &r.CostUSD, &r.LatencyMs, &createdAt); err != nil {
			return nil, fmt.Errorf("scan route: %w", err)
		}
		// 提示词按 rune 截断到 200 字符用于展示（避免中文乱码）。
		if utf8.RuneCountInString(r.Prompt) > 200 {
			runes := []rune(r.Prompt)
			r.Prompt = string(runes[:200])
		}
		r.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		result = append(result, r)
	}
	return result, nil
}

// CostTrend 返回过去 N 天的每日费用和请求数趋势。
func (s *LogStore) CostTrend(days int) ([]CostTrendPoint, error) {
	if days <= 0 {
		days = 7
	}

	since := time.Now().AddDate(0, 0, -days)

	rows, err := s.db.Query(`
		SELECT DATE(created_at) as day, SUM(cost_usd) as cost, COUNT(1) as requests
		FROM request_logs
		WHERE created_at >= ?
		GROUP BY day
		ORDER BY day ASC
	`, since)
	if err != nil {
		return nil, fmt.Errorf("cost trend: %w", err)
	}
	defer rows.Close()

	var result []CostTrendPoint
	for rows.Next() {
		var p CostTrendPoint
		if err := rows.Scan(&p.Date, &p.Cost, &p.Requests); err != nil {
			return nil, fmt.Errorf("scan trend: %w", err)
		}
		result = append(result, p)
	}
	return result, nil
}

// MonitorOverview 返回监控页面的聚合数据（今日实时统计）。
func (s *LogStore) MonitorOverview() (*MonitorOverviewData, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var data MonitorOverviewData
	var avgLatencyMs float64

	// 今日请求总数
	if err := s.db.QueryRow(
		"SELECT COUNT(1) FROM request_logs WHERE created_at >= ?", todayStart,
	).Scan(&data.TodayRequests); err != nil {
		return nil, fmt.Errorf("today requests: %w", err)
	}

	// 今日错误数
	if err := s.db.QueryRow(
		"SELECT COUNT(1) FROM request_logs WHERE created_at >= ? AND status = 'error'", todayStart,
	).Scan(&data.ErrorCount); err != nil {
		return nil, fmt.Errorf("error count: %w", err)
	}

	// 今日总 Token 数
	if err := s.db.QueryRow(
		"SELECT COALESCE(SUM(total_tokens), 0) FROM request_logs WHERE created_at >= ?", todayStart,
	).Scan(&data.TotalTokens); err != nil {
		return nil, fmt.Errorf("total tokens: %w", err)
	}

	// 今日平均延迟
	if err := s.db.QueryRow(
		"SELECT COALESCE(AVG(latency_ms), 0) FROM request_logs WHERE created_at >= ?", todayStart,
	).Scan(&avgLatencyMs); err != nil {
		return nil, fmt.Errorf("avg latency: %w", err)
	}
	data.AvgLatencyMs = int(avgLatencyMs)

	return &data, nil
}

// ByModel 返回各模型的详细统计（按请求次数降序）。
//
// 统计维度：供应商、请求数、输入/输出 Token、缓存 Token、
// 费用、平均/最小/最大延迟。
func (s *LogStore) ByModel() ([]map[string]interface{}, error) {
	rows, err := s.db.Query(`
		SELECT
			model,
			provider,
			COUNT(1) as requests,
			COALESCE(SUM(prompt_tokens), 0) as input_tokens,
			COALESCE(SUM(completion_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(cost_usd), 0) as cost,
			COALESCE(AVG(latency_ms), 0) as avg_latency,
			COALESCE(MIN(latency_ms), 0) as min_latency,
			COALESCE(MAX(latency_ms), 0) as max_latency
		FROM request_logs
		GROUP BY model, provider
		ORDER BY requests DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("by model: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var model, provider string
		var requests, inputTokens, outputTokens, cacheTokens, minLat, maxLat int64
		var cost, avgLat float64
		if err := rows.Scan(&model, &provider, &requests, &inputTokens, &outputTokens,
			&cacheTokens, &cost, &avgLat, &minLat, &maxLat); err != nil {
			return nil, fmt.Errorf("scan by model: %w", err)
		}
		result = append(result, map[string]interface{}{
			"model":             model,
			"provider":          provider,
			"requests":          requests,
			"input_tokens":      inputTokens,
			"output_tokens":     outputTokens,
			"cache_read_tokens": cacheTokens,
			"cost_usd":          float64(int(cost*10000)) / 10000,
			"avg_latency_ms":    int(avgLat),
			"min_latency_ms":    minLat,
			"max_latency_ms":    maxLat,
		})
	}
	return result, nil
}

// ModelPerformance 返回各模型的效果分析数据（延迟、成本、使用量）。
func (s *LogStore) ModelPerformance() ([]map[string]interface{}, error) {
	rows, err := s.db.Query(`
		SELECT model, provider,
			COUNT(1) as requests,
			COALESCE(AVG(latency_ms), 0) as avg_latency,
			COALESCE(AVG(prompt_tokens), 0) + COALESCE(AVG(completion_tokens), 0) as avg_tokens,
			COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(AVG(cost_usd), 0) as avg_cost,
			COALESCE(SUM(total_tokens), 0) as total_tokens
		FROM request_logs
		WHERE created_at >= datetime('now', '-30 days')
		GROUP BY model, provider
		ORDER BY requests DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("model performance: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var model, provider string
		var requests int64
		var avgLatency, avgTokens, totalCost, avgCost, totalTokens float64
		if err := rows.Scan(&model, &provider, &requests, &avgLatency, &avgTokens, &totalCost, &avgCost, &totalTokens); err != nil {
			return nil, fmt.Errorf("scan performance: %w", err)
		}
		result = append(result, map[string]interface{}{
			"model":        model,
			"provider":     provider,
			"requests":     requests,
			"avg_latency_ms": float64(int(avgLatency)),
			"avg_tokens":   float64(int(avgTokens)),
			"total_cost_usd": float64(int(totalCost*10000)) / 10000,
			"avg_cost_usd": float64(int(avgCost*1000000)) / 1000000,
			"total_tokens": int(totalTokens),
		})
	}
	return result, rows.Err()
}

// ────────────────────────────────────────────────────────────
// 数据库迁移
// ────────────────────────────────────────────────────────────

// migrate 创建数据库表结构（幂等，仅 CREATE TABLE IF NOT EXISTS）。
//
// 索引设计：
//   - created_at：Dashboard 时间范围查询的核心索引
//   - provider：按供应商过滤和分析
//   - model：按模型统计分布和延迟
//
// 注意：索引会降低写入速度，但本项目写入模式是单条 INSERT
// （非批量），影响可忽略。
func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS request_logs (
		id TEXT PRIMARY KEY,
		prompt TEXT DEFAULT '',
		prompt_tokens INTEGER DEFAULT 0,
		completion_tokens INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		cache_read_tokens INTEGER DEFAULT 0,
		cache_creation_tokens INTEGER DEFAULT 0,
		model TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		upstream_url TEXT DEFAULT '',
		route_reason TEXT DEFAULT '',
		status TEXT DEFAULT 'ok',
		error_detail TEXT DEFAULT '',
		upstream_request_id TEXT DEFAULT '',
		cost_usd REAL DEFAULT 0.0,
		latency_ms INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at);
	CREATE INDEX IF NOT EXISTS idx_request_logs_provider ON request_logs(provider);
	CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs(model);
	`
	_, err := db.Exec(schema)
	return err
}

// CompactPrompt 截断提示词用于展示。
// 保留前 maxLen 个字符（按 rune 而非 byte 计数），超出部分用 "..." 替代。
// 避免中文字符（3 字节/字符）在字节边界被截断产生乱码。
func CompactPrompt(prompt string, maxLen int) string {
	if utf8.RuneCountInString(prompt) <= maxLen {
		return prompt
	}
	runes := []rune(prompt)
	if len(runes) <= maxLen {
		return prompt
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}
