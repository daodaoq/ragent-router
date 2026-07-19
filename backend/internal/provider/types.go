// Package provider 定义供应商相关的核心类型与接口。
// proxy 和 routing 包都依赖此包，避免双向耦合。
package provider

// Config 是一个上游 AI 供应商的完整配置。
// 可通过环境变量或 JSON 配置文件注入。
type Config struct {
	ID      string            `json:"id"`       // 唯一标识
	Name    string            `json:"name"`     // 显示名称（如 "DeepSeek", "Claude"）
	BaseURL string            `json:"base_url"` // API 基础地址
	APIKey  string            `json:"api_key"`  // 认证密钥
	Model   string            `json:"model"`    // 默认模型名
	Headers map[string]string `json:"headers"`  // 额外的请求头
	Enabled bool              `json:"enabled"`  // 是否启用
}
