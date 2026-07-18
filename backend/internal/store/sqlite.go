package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// LogStore persists proxy request logs to SQLite.
type LogStore struct {
	db *sql.DB
}

// NewLogStore opens (or creates) the SQLite database and initializes the schema.
func NewLogStore(path string) (*LogStore, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Connection pool: SQLite is single-writer, so limit connections.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &LogStore{db: db}, nil
}

// Close closes the database connection.
func (s *LogStore) Close() error {
	return s.db.Close()
}

// Insert saves a request log record.
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

// DashboardOverview returns aggregate dashboard statistics.
func (s *LogStore) DashboardOverview() (*DashboardOverview, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var overview DashboardOverview

	// Today's cost.
	err := s.db.QueryRow(
		"SELECT COALESCE(SUM(cost_usd), 0) FROM request_logs WHERE created_at >= ?",
		todayStart,
	).Scan(&overview.TodayCost)
	if err != nil {
		return nil, err
	}

	// Month's cost.
	err = s.db.QueryRow(
		"SELECT COALESCE(SUM(cost_usd), 0) FROM request_logs WHERE created_at >= ?",
		monthStart,
	).Scan(&overview.MonthCost)
	if err != nil {
		return nil, err
	}

	// Total requests.
	err = s.db.QueryRow("SELECT COUNT(1) FROM request_logs").Scan(&overview.TotalRequests)
	if err != nil {
		return nil, err
	}

	// Estimated savings (placeholder: assume 20% savings from routing to cheaper models).
	overview.SavedAmount = overview.MonthCost * 0.25
	overview.SavingRate = 25.0

	return &overview, nil
}

// ModelDistribution returns request distribution by model.
func (s *LogStore) ModelDistribution() ([]ModelDistribution, error) {
	rows, err := s.db.Query(`
		SELECT model, COUNT(1) as cnt
		FROM request_logs
		GROUP BY model
		ORDER BY cnt DESC
	`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		items = append(items, i)
		total += i.Count
	}

	var result []ModelDistribution
	for _, it := range items {
		pct := float64(0)
		if total > 0 {
			pct = float64(it.Count) / float64(total) * 100
		}
		result = append(result, ModelDistribution{
			Model:      it.Model,
			Count:      it.Count,
			Percentage: float64(int(pct*10)) / 10,
		})
	}
	return result, nil
}

// RecentRoutes returns the most recent request logs.
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
		return nil, err
	}
	defer rows.Close()

	var result []RecentRoute
	for rows.Next() {
		var r RecentRoute
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.Prompt, &r.Model, &r.Provider,
			&r.RouteReason, &r.CostUSD, &r.LatencyMs, &createdAt); err != nil {
			return nil, err
		}
		if len(r.Prompt) > 200 {
			r.Prompt = r.Prompt[:200]
		}
		r.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		result = append(result, r)
	}
	return result, nil
}

// CostTrend returns daily cost and request counts for the past N days.
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
		return nil, err
	}
	defer rows.Close()

	var result []CostTrendPoint
	for rows.Next() {
		var p CostTrendPoint
		if err := rows.Scan(&p.Date, &p.Cost, &p.Requests); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}

// ByModel returns detailed per-model statistics.
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
		return nil, err
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var model, provider string
		var requests, inputTokens, outputTokens, cacheTokens, minLat, maxLat int64
		var cost, avgLat float64
		if err := rows.Scan(&model, &provider, &requests, &inputTokens, &outputTokens,
			&cacheTokens, &cost, &avgLat, &minLat, &maxLat); err != nil {
			return nil, err
		}
		result = append(result, map[string]interface{}{
			"model":              model,
			"provider":           provider,
			"requests":           requests,
			"input_tokens":       inputTokens,
			"output_tokens":      outputTokens,
			"cache_read_tokens":  cacheTokens,
			"cost_usd":           float64(int(cost*10000)) / 10000,
			"avg_latency_ms":     int(avgLat),
			"min_latency_ms":     minLat,
			"max_latency_ms":     maxLat,
		})
	}
	return result, nil
}

// migrate creates the database schema if it doesn't exist.
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

// CompactPrompt truncates a prompt for display.
func CompactPrompt(prompt string, maxLen int) string {
	if len(prompt) <= maxLen {
		return prompt
	}
	return strings.TrimSpace(prompt[:maxLen]) + "..."
}
