// Package store provides persistent storage for request logs and analytics.
package store

import (
	"time"
)

// RequestLogRecord represents a single proxied API call.
type RequestLogRecord struct {
	ID                 string    `json:"id" db:"id"`
	Prompt             string    `json:"prompt" db:"prompt"`
	PromptTokens       int       `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens   int       `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens        int       `json:"total_tokens" db:"total_tokens"`
	CacheReadTokens    int       `json:"cache_read_tokens" db:"cache_read_tokens"`
	CacheCreationTokens int      `json:"cache_creation_tokens" db:"cache_creation_tokens"`
	Model              string    `json:"model" db:"model"`
	Provider           string    `json:"provider" db:"provider"`
	UpstreamURL        string    `json:"upstream_url" db:"upstream_url"`
	RouteReason        string    `json:"route_reason" db:"route_reason"`
	Status             string    `json:"status" db:"status"`
	ErrorDetail        string    `json:"error_detail" db:"error_detail"`
	UpstreamRequestID  string    `json:"upstream_request_id" db:"upstream_request_id"`
	CostUSD            float64   `json:"cost_usd" db:"cost_usd"`
	LatencyMs          int64     `json:"latency_ms" db:"latency_ms"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
}

// DashboardOverview holds aggregate dashboard statistics.
type DashboardOverview struct {
	TodayCost     float64 `json:"today_cost"`
	MonthCost     float64 `json:"month_cost"`
	SavedAmount   float64 `json:"saved_amount"`
	SavingRate    float64 `json:"saving_rate"`
	TotalRequests int64   `json:"total_requests"`
}

// ModelDistribution holds per-model usage stats.
type ModelDistribution struct {
	Model      string  `json:"model"`
	Count      int64   `json:"count"`
	Percentage float64 `json:"percentage"`
}

// RecentRoute is a lightweight log entry for the dashboard.
type RecentRoute struct {
	ID          string  `json:"id"`
	Prompt      string  `json:"prompt"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	RouteReason string  `json:"route_reason"`
	CostUSD     float64 `json:"cost_usd"`
	LatencyMs   int64   `json:"latency_ms"`
	CreatedAt   string  `json:"created_at"`
}

// CostTrendPoint is a time-series data point for cost charts.
type CostTrendPoint struct {
	Date     string  `json:"date"`
	Cost     float64 `json:"cost"`
	Requests int64   `json:"requests"`
}

// ProxyHealth holds the status of the proxy and its dependencies.
type ProxyHealth struct {
	CCSwitchDBAvailable   bool     `json:"ccswitch_db_available"`
	StateFileOK           bool     `json:"state_file_ok"`
	ActiveProviderID      string   `json:"active_provider_id"`
	ActiveProviderValid   bool     `json:"active_provider_valid"`
	Warnings              []string `json:"warnings"`
	ProxyReady            bool     `json:"proxy_ready"`
}
