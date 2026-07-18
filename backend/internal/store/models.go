// Package store 提供请求日志的持久化存储与分析查询。
//
// 当前实现使用 SQLite（modernc.org/sqlite，纯 Go 无 CGO），
// 适合单机部署和本地 Dashboard。后续可扩展为 PostgreSQL/MySQL 后端。
package store

import "time"

// ────────────────────────────────────────────────────────────
// 请求日志
// ────────────────────────────────────────────────────────────

// RequestLogRecord 是一条完整的代理请求记录。
// 映射到 SQLite 的 request_logs 表。
type RequestLogRecord struct {
	ID                  string    `json:"id" db:"id"`                                     // 唯一标识
	Prompt              string    `json:"prompt" db:"prompt"`                             // 用户提示词（截取前 500 字符）
	PromptTokens        int       `json:"prompt_tokens" db:"prompt_tokens"`               // 输入 Token 数
	CompletionTokens    int       `json:"completion_tokens" db:"completion_tokens"`       // 输出 Token 数
	TotalTokens         int       `json:"total_tokens" db:"total_tokens"`                 // 总 Token 数
	CacheReadTokens     int       `json:"cache_read_tokens" db:"cache_read_tokens"`       // 缓存读取 Token 数
	CacheCreationTokens int       `json:"cache_creation_tokens" db:"cache_creation_tokens"` // 缓存创建 Token 数
	Model               string    `json:"model" db:"model"`                               // 实际使用的模型名
	Provider            string    `json:"provider" db:"provider"`                         // 供应商名称
	UpstreamURL         string    `json:"upstream_url" db:"upstream_url"`                 // 上游 API URL
	RouteReason         string    `json:"route_reason" db:"route_reason"`                 // 路由决策原因
	Status              string    `json:"status" db:"status"`                             // "ok" 或 "error"
	ErrorDetail         string    `json:"error_detail" db:"error_detail"`                 // 错误详情
	UpstreamRequestID   string    `json:"upstream_request_id" db:"upstream_request_id"`   // 上游返回的请求 ID
	CostUSD             float64   `json:"cost_usd" db:"cost_usd"`                         // 估算费用（美元）
	LatencyMs           int64     `json:"latency_ms" db:"latency_ms"`                     // 端到端延迟（毫秒）
	CreatedAt           time.Time `json:"created_at" db:"created_at"`                     // 请求时间
}

// ────────────────────────────────────────────────────────────
// Dashboard 数据模型
// ────────────────────────────────────────────────────────────

// DashboardOverview 是 Dashboard 首页的聚合统计。
type DashboardOverview struct {
	TodayCost     float64 `json:"today_cost"`     // 今日总费用（USD）
	MonthCost     float64 `json:"month_cost"`     // 本月总费用（USD）
	SavedAmount   float64 `json:"saved_amount"`   // 通过路由优化节省的费用（估算）
	SavingRate    float64 `json:"saving_rate"`    // 节省比例（%）
	TotalRequests int64   `json:"total_requests"` // 历史总请求数
}

// ModelDistribution 是单个模型的请求分布数据。
type ModelDistribution struct {
	Model      string  `json:"model"`      // 模型名
	Count      int64   `json:"count"`      // 请求次数
	Percentage float64 `json:"percentage"` // 占总请求的百分比
}

// RecentRoute 是 Dashboard "最近请求"列表的一行。
type RecentRoute struct {
	ID          string  `json:"id"`           // 请求 ID
	Prompt      string  `json:"prompt"`       // 提示词（截断到 200 字符）
	Model       string  `json:"model"`        // 模型名
	Provider    string  `json:"provider"`     // 供应商名
	RouteReason string  `json:"route_reason"` // 路由原因
	CostUSD     float64 `json:"cost_usd"`      // 费用（USD）
	LatencyMs   int64   `json:"latency_ms"`   // 延迟（毫秒）
	CreatedAt   string  `json:"created_at"`   // 时间（格式化为本地时间字符串）
}

// CostTrendPoint 是成本趋势图的一个数据点。
type CostTrendPoint struct {
	Date     string  `json:"date"`     // 日期（YYYY-MM-DD）
	Cost     float64 `json:"cost"`     // 当日总费用
	Requests int64   `json:"requests"` // 当日请求数
}

// ProxyHealth 是代理健康检查的响应模型。
type ProxyHealth struct {
	CCSwitchDBAvailable bool     `json:"ccswitch_db_available"` // 供应商数据库是否可用
	StateFileOK         bool     `json:"state_file_ok"`         // 状态文件是否正常
	ActiveProviderID    string   `json:"active_provider_id"`    // 当前活跃供应商 ID
	ActiveProviderValid bool     `json:"active_provider_valid"` // 当前供应商配置是否有效
	Warnings            []string `json:"warnings"`              // 警告信息列表
	ProxyReady          bool     `json:"proxy_ready"`           // 代理是否就绪
}
