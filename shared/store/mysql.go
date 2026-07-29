// MySQLLogStore 是请求日志的 MySQL/GORM 持久化存储。
//
// 适用场景：多实例部署、需要事务和复杂查询的生产环境。
// 与 SQLite LogStore 共享同一套查询接口（DashboardOverview 等），
// 但底层使用 GORM，支持 MySQL/PostgreSQL。
package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// MySQLRequestLogRecord GORM 模型（映射到 request_logs 表）。
//
// 字段与 SQLite 版 RequestLogRecord 保持一致，
// 但使用 GORM tag 而非 db tag。
type MySQLRequestLogRecord struct {
	ID                  string    `gorm:"column:id;primaryKey;type:varchar(64)" json:"id"`
	Prompt              string    `gorm:"column:prompt;type:text" json:"prompt"`
	PromptTokens        int       `gorm:"column:prompt_tokens;default:0" json:"prompt_tokens"`
	CompletionTokens    int       `gorm:"column:completion_tokens;default:0" json:"completion_tokens"`
	TotalTokens         int       `gorm:"column:total_tokens;default:0" json:"total_tokens"`
	CacheReadTokens     int       `gorm:"column:cache_read_tokens;default:0" json:"cache_read_tokens"`
	CacheCreationTokens int       `gorm:"column:cache_creation_tokens;default:0" json:"cache_creation_tokens"`
	Model               string    `gorm:"column:model;type:varchar(128);not null" json:"model"`
	Provider            string    `gorm:"column:provider;type:varchar(128);not null;default:''" json:"provider"`
	UpstreamURL         string    `gorm:"column:upstream_url;type:varchar(512)" json:"upstream_url"`
	RouteReason         string    `gorm:"column:route_reason;type:varchar(256)" json:"route_reason"`
	Status              string    `gorm:"column:status;type:varchar(16);default:'ok'" json:"status"`
	ErrorDetail         string    `gorm:"column:error_detail;type:text" json:"error_detail"`
	UpstreamRequestID   string    `gorm:"column:upstream_request_id;type:varchar(128)" json:"upstream_request_id"`
	CostUSD             float64   `gorm:"column:cost_usd;type:double;default:0" json:"cost_usd"`
	LatencyMs           int64     `gorm:"column:latency_ms;default:0" json:"latency_ms"`
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

// TableName 指定表名。
func (MySQLRequestLogRecord) TableName() string {
	return "request_logs"
}

// MySQLLogStore 使用 GORM 操作 MySQL/PostgreSQL。
type MySQLLogStore struct {
	db *gorm.DB
}

// NewMySQLLogStore 创建 MySQL 存储并自动建表。
func NewMySQLLogStore(db *gorm.DB) (*MySQLLogStore, error) {
	if err := db.AutoMigrate(&MySQLRequestLogRecord{}); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	return &MySQLLogStore{db: db}, nil
}

// Close 关闭数据库连接（GORM 底层的 *sql.DB）。
func (s *MySQLLogStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// ────────────────────────────────────────────────────────────
// 写入操作
// ────────────────────────────────────────────────────────────

// Insert 保存一条请求日志。
func (s *MySQLLogStore) Insert(record *MySQLRequestLogRecord) error {
	return s.db.Create(record).Error
}

// InsertBatch 批量保存请求日志（高吞吐场景）。
func (s *MySQLLogStore) InsertBatch(records []*MySQLRequestLogRecord) error {
	if len(records) == 0 {
		return nil
	}
	return s.db.CreateInBatches(records, 500).Error
}

// ────────────────────────────────────────────────────────────
// Dashboard 查询（与 SQLite LogStore 接口一致）
// ────────────────────────────────────────────────────────────

// DashboardOverview 返回 Dashboard 首页的聚合统计数据。
func (s *MySQLLogStore) DashboardOverview() (*DashboardOverview, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var overview DashboardOverview

	// 今日费用
	if err := s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", todayStart).
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&overview.TodayCost).Error; err != nil {
		return nil, fmt.Errorf("today cost: %w", err)
	}

	// 本月费用
	if err := s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", monthStart).
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&overview.MonthCost).Error; err != nil {
		return nil, fmt.Errorf("month cost: %w", err)
	}

	// 总请求数
	if err := s.db.Model(&MySQLRequestLogRecord{}).
		Count(&overview.TotalRequests).Error; err != nil {
		return nil, fmt.Errorf("total requests: %w", err)
	}

	// 节省估算
	var totalPrompt, totalCompletion int64
	s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", monthStart).
		Select("COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0)").
		Row().Scan(&totalPrompt, &totalCompletion)

	estimatedClaudeCost := (float64(totalPrompt)*3.00 + float64(totalCompletion)*15.00) / 1_000_000
	overview.SavedAmount = estimatedClaudeCost - overview.MonthCost
	if overview.SavedAmount < 0 {
		overview.SavedAmount = 0
	}
	if estimatedClaudeCost > 0 {
		overview.SavingRate = float64(int(overview.SavedAmount/estimatedClaudeCost*1000)) / 10
	}

	return &overview, nil
}

// ModelDistribution 返回各模型的请求分布（按请求次数降序）。
func (s *MySQLLogStore) ModelDistribution() ([]ModelDistribution, error) {
	type result struct {
		Model string
		Count int64
	}
	var items []result

	if err := s.db.Model(&MySQLRequestLogRecord{}).
		Select("model, COUNT(1) as count").
		Group("model").
		Order("count DESC").
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("model distribution: %w", err)
	}

	var total int64
	for _, it := range items {
		total += it.Count
	}

	var dist []ModelDistribution
	for _, it := range items {
		pct := float64(0)
		if total > 0 {
			pct = float64(it.Count) / float64(total) * 100
		}
		dist = append(dist, ModelDistribution{
			Model:      it.Model,
			Count:      it.Count,
			Percentage: float64(int(pct*10)) / 10,
		})
	}
	return dist, nil
}

// RecentRoutes 返回最近 N 条请求日志。
func (s *MySQLLogStore) RecentRoutes(limit int) ([]RecentRoute, error) {
	if limit <= 0 {
		limit = 20
	}

	var records []MySQLRequestLogRecord
	if err := s.db.Order("created_at DESC").Limit(limit).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("recent routes: %w", err)
	}

	var result []RecentRoute
	for _, r := range records {
		result = append(result, RecentRoute{
			ID:          r.ID,
			Prompt:      CompactPrompt(r.Prompt, 200),
			Model:       r.Model,
			Provider:    r.Provider,
			RouteReason: r.RouteReason,
			CostUSD:     r.CostUSD,
			LatencyMs:   r.LatencyMs,
			CreatedAt:   r.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return result, nil
}

// CostTrend 返回过去 N 天的每日费用和请求数趋势。
func (s *MySQLLogStore) CostTrend(days int) ([]CostTrendPoint, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days)

	type trendRow struct {
		Date     string
		Cost     float64
		Requests int64
	}
	var rows []trendRow

	if err := s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", since).
		Select("DATE(created_at) as date, SUM(cost_usd) as cost, COUNT(1) as requests").
		Group("date").
		Order("date ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("cost trend: %w", err)
	}

	var result []CostTrendPoint
	for _, r := range rows {
		result = append(result, CostTrendPoint{
			Date:     r.Date,
			Cost:     r.Cost,
			Requests: r.Requests,
		})
	}
	return result, nil
}

// MonitorOverview 返回监控页面的聚合数据。
func (s *MySQLLogStore) MonitorOverview() (*MonitorOverviewData, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var data MonitorOverviewData

	// 今日请求总数
	s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", todayStart).
		Count(&data.TodayRequests)

	// 今日错误数
	s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ? AND status = 'error'", todayStart).
		Count(&data.ErrorCount)

	// 今日总 Token 数
	s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", todayStart).
		Select("COALESCE(SUM(total_tokens), 0)").
		Scan(&data.TotalTokens)

	// 今日平均延迟
	var avgLatency float64
	s.db.Model(&MySQLRequestLogRecord{}).
		Where("created_at >= ?", todayStart).
		Select("COALESCE(AVG(latency_ms), 0)").
		Scan(&avgLatency)
	data.AvgLatencyMs = int(avgLatency)

	return &data, nil
}
