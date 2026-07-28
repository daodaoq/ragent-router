package model

import (
	"fmt"
	"time"
)

// RequestLog 请求日志模型。
type RequestLog struct {
	Id                int       `json:"id"`
	Prompt            string    `json:"prompt" gorm:"type:text"` // 截断后的 prompt
	PromptTokens      int       `json:"prompt_tokens"`
	CompletionTokens  int       `json:"completion_tokens"`
	TotalTokens       int       `json:"total_tokens"`
	Model             string    `json:"model" gorm:"index"`
	Provider          string    `json:"provider" gorm:"index"`
	UserId            int       `json:"user_id" gorm:"index"`
	TokenId           int       `json:"token_id" gorm:"index"`
	RouteReason       string    `json:"route_reason"`
	Status            string    `json:"status" gorm:"index"` // "ok" 或 "error"
	ErrorDetail       string    `json:"error_detail" gorm:"type:text"`
	UpstreamRequestId string    `json:"upstream_request_id"`
	CostUSD           float64   `json:"cost_usd"`
	LatencyMs         int64     `json:"latency_ms"`
	CreatedAt         time.Time `json:"created_at" gorm:"index;autoCreateTime"`
}

// TableName 指定表名。
func (RequestLog) TableName() string {
	return "request_logs"
}

// DashboardOverview 仪表盘概览数据。
type DashboardOverview struct {
	TodayCost    float64 `json:"today_cost"`
	MonthCost    float64 `json:"month_cost"`
	TotalCost    float64 `json:"total_cost"`
	TodayRequests int    `json:"today_requests"`
	TotalRequests int    `json:"total_requests"`
	SavedCost    float64 `json:"saved_cost"` // 节省的费用（相比全用 Claude）
}

// ModelDistribution 模型使用分布。
type ModelDistribution struct {
	Model      string  `json:"model"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
}

// CostTrendPoint 成本趋势数据点。
type CostTrendPoint struct {
	Date     string  `json:"date"`
	Cost     float64 `json:"cost"`
	Requests int     `json:"requests"`
}

// MonitorOverviewData 监控概览。
type MonitorOverviewData struct {
	TodayRequests int     `json:"today_requests"`
	ErrorCount    int     `json:"error_count"`
	TotalTokens   int     `json:"total_tokens"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
}

// ByModelData 按模型统计。
type ByModelData struct {
	Model        string  `json:"model"`
	Count        int     `json:"count"`
	TotalTokens  int     `json:"total_tokens"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	TotalCost    float64 `json:"total_cost"`
}

// InsertLog 插入请求日志。
func InsertLog(log *RequestLog) error {
	return DB.Create(log).Error
}

// DashboardOverview 查询仪表盘概览。
func DashboardOverviewQuery() (*DashboardOverview, error) {
	var overview DashboardOverview
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	// 今日费用
	DB.Model(&RequestLog{}).
		Where("created_at >= ? AND status = ?", todayStart, "ok").
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&overview.TodayCost)

	// 本月费用
	DB.Model(&RequestLog{}).
		Where("created_at >= ? AND status = ?", monthStart, "ok").
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&overview.MonthCost)

	// 总费用
	DB.Model(&RequestLog{}).
		Where("status = ?", "ok").
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&overview.TotalCost)

	// 今日请求数
	var todayCount int64
	DB.Model(&RequestLog{}).
		Where("created_at >= ?", todayStart).
		Count(&todayCount)
	overview.TodayRequests = int(todayCount)

	// 总请求数
	var totalCount int64
	DB.Model(&RequestLog{}).Count(&totalCount)
	overview.TotalRequests = int(totalCount)

	// 节省费用估算（假设全用 Claude 的成本 - 实际成本）
	// Claude Sonnet: $3/M input, $15/M output
	DB.Model(&RequestLog{}).
		Where("created_at >= ? AND status = ?", monthStart, "ok").
		Select("COALESCE(SUM(prompt_tokens * 3.0 + completion_tokens * 15.0) / 1000000 - SUM(cost_usd), 0)").
		Scan(&overview.SavedCost)

	return &overview, nil
}

// ModelDistributionQuery 查询模型分布。
func ModelDistributionQuery() ([]ModelDistribution, error) {
	var results []ModelDistribution
	var total int64

	DB.Model(&RequestLog{}).Count(&total)
	if total == 0 {
		return results, nil
	}

	rows, err := DB.Raw(`
		SELECT model, COUNT(*) as count
		FROM request_logs
		WHERE model != ''
		GROUP BY model
		ORDER BY count DESC
		LIMIT 10
	`).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var item ModelDistribution
		rows.Scan(&item.Model, &item.Count)
		item.Percentage = float64(item.Count) / float64(total) * 100
		results = append(results, item)
	}
	return results, nil
}

// CostTrendQuery 查询成本趋势。
func CostTrendQuery(days int) ([]CostTrendPoint, error) {
	var points []CostTrendPoint
	rows, err := DB.Raw(`
		SELECT DATE(created_at) as date,
		       COALESCE(SUM(cost_usd), 0) as cost,
		       COUNT(*) as requests
		FROM request_logs
		WHERE created_at >= datetime('now', ?)
		GROUP BY DATE(created_at)
		ORDER BY date ASC
	`, fmt.Sprintf("-%d days", days)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var point CostTrendPoint
		rows.Scan(&point.Date, &point.Cost, &point.Requests)
		points = append(points, point)
	}
	return points, nil
}

// MonitorOverviewQuery 查询监控概览。
func MonitorOverviewQuery() (*MonitorOverviewData, error) {
	var data MonitorOverviewData
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var todayReqCount int64
	DB.Model(&RequestLog{}).
		Where("created_at >= ?", todayStart).
		Count(&todayReqCount)
	data.TodayRequests = int(todayReqCount)

	var errorCount int64
	DB.Model(&RequestLog{}).
		Where("created_at >= ? AND status = ?", todayStart, "error").
		Count(&errorCount)
	data.ErrorCount = int(errorCount)

	DB.Model(&RequestLog{}).
		Where("created_at >= ?", todayStart).
		Select("COALESCE(SUM(total_tokens), 0)").
		Scan(&data.TotalTokens)

	DB.Model(&RequestLog{}).
		Where("created_at >= ? AND status = ?", todayStart, "ok").
		Select("COALESCE(AVG(latency_ms), 0)").
		Scan(&data.AvgLatencyMs)

	return &data, nil
}

// ByModelQuery 按模型统计。
func ByModelQuery() ([]ByModelData, error) {
	var results []ByModelData
	rows, err := DB.Raw(`
		SELECT model,
		       COUNT(*) as count,
		       COALESCE(SUM(total_tokens), 0) as total_tokens,
		       COALESCE(AVG(latency_ms), 0) as avg_latency_ms,
		       COALESCE(SUM(cost_usd), 0) as total_cost
		FROM request_logs
		WHERE model != '' AND created_at >= datetime('now', '-1 day')
		GROUP BY model
		ORDER BY count DESC
		LIMIT 20
	`).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var item ByModelData
		rows.Scan(&item.Model, &item.Count, &item.TotalTokens, &item.AvgLatencyMs, &item.TotalCost)
		results = append(results, item)
	}
	return results, nil
}
