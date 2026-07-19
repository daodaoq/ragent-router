package provider

import "time"

// ResilienceConfig 统一管理所有韧性组件参数。
// 替代散落在 handler.go、main.go 中的硬编码值。
type ResilienceConfig struct {
	// 全局限流（Token/秒）
	GlobalRateLimit float64 `json:"global_rate_limit"`

	// 舱壁——最大并发请求数
	MaxConcurrentRequests int `json:"max_concurrent_requests"`

	// 熔断器
	FailureThreshold float64       `json:"failure_threshold"` // 故障阈值（0.0-1.0）
	OpenTimeout      time.Duration `json:"open_timeout"`      // 熔断后等待时间

	// 重试
	MaxRetries int `json:"max_retries"` // 最大重试次数（不含初始请求）

	// 超时
	RequestTimeout time.Duration `json:"request_timeout"` // 请求级超时
	UpstreamTimeout time.Duration `json:"upstream_timeout"` // 上游调用级超时
}

// DefaultResilienceConfig 返回生产推荐的韧性参数。
func DefaultResilienceConfig() ResilienceConfig {
	return ResilienceConfig{
		GlobalRateLimit:        100,
		MaxConcurrentRequests:  50,
		FailureThreshold:       0.5,
		OpenTimeout:            30 * time.Second,
		MaxRetries:             2,
		RequestTimeout:         300 * time.Second,
		UpstreamTimeout:        120 * time.Second,
	}
}
