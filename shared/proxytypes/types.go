package proxytypes

import "time"

// ProviderConfig 供应商配置。
type ProviderConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url"`
	APIKey    string            `json:"api_key"`
	Model     string            `json:"model"`
	Enabled   bool              `json:"enabled"`
	Weight    int               `json:"weight"`
	Headers   map[string]string `json:"headers,omitempty"`
	Group     string            `json:"group"`
	Priority  int64             `json:"priority"`
}

// RequestLog 请求日志。
type RequestLog struct {
	RequestID        string
	Prompt           string
	Provider         string
	Model            string
	RouteReason      string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	LatencyMs        int64
	Status           string
	ErrorDetail      string
	UpstreamID       string
	Timestamp        time.Time
}
