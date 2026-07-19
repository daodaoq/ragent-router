// Package proxy 实现 Anthropic 兼容的透明代理，
// 将请求路由到不同 AI 供应商，并提供韧性引擎保护。
//
// # 协议适配
//
// AI 供应商使用不同的 API 协议：
//   - Anthropic/Claude：Messages API（SSE 流式，x-api-key 认证）
//   - OpenAI/DeepSeek：Chat Completions API（SSE 流式，Bearer Token 认证）
//
// 本包的策略：代理层统一使用 Anthropic Messages API 格式
// （因为 Claude Code 使用它），适配器负责翻译为供应商原生格式。
//
// # 适配器模式
//
// ProviderAdapter 接口抽象了协议差异。新增供应商只需实现此接口——
// 代理核心代码（handler.go）完全不需要改动。
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ────────────────────────────────────────────────────────────
// 供应商配置
// ────────────────────────────────────────────────────────────

// ProviderConfig 是一个上游 AI 供应商的完整配置。
// 可通过环境变量或 JSON 配置文件注入。
type ProviderConfig struct {
	ID      string            `json:"id"`       // 唯一标识
	Name    string            `json:"name"`     // 显示名称（如 "DeepSeek", "Claude"）
	BaseURL string            `json:"base_url"` // API 基础地址
	APIKey  string            `json:"api_key"`  // 认证密钥
	Model   string            `json:"model"`    // 默认模型名
	Headers map[string]string `json:"headers"`  // 额外的请求头
	Enabled bool              `json:"enabled"`  // 是否启用
}

// ────────────────────────────────────────────────────────────
// 适配器接口
// ────────────────────────────────────────────────────────────

// ProviderAdapter 是不同 AI 供应商协议的适配器接口。
//
// 代理层始终使用 Anthropic Messages API 格式与客户端通信。
// 适配器负责将请求翻译为供应商的原生格式。
//
// 当前支持：
//   - AnthropicAdapter：原样透传（Anthropic 原生格式）
//   - OpenAIAdapter：翻译为 OpenAI Chat Completions 格式
type ProviderAdapter interface {
	// BuildRequest 为指定供应商构建上游 HTTP 请求。
	//
	// 参数：
	//   - baseURL：供应商的 API 基础地址
	//   - headers：已预填充的通用请求头（认证、版本等）
	//   - body：Anthropic 格式的请求体（JSON 已反序列化）
	//
	// 返回值：完整的请求 URL、请求头、序列化后的请求体、可能的错误。
	BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (
		url string, reqHeaders map[string]string, reqBody []byte, err error)
}

// ────────────────────────────────────────────────────────────
// Anthropic 适配器
// ────────────────────────────────────────────────────────────

// AnthropicAdapter 原生透传，不做格式转换。
// 适用于 Anthropic 原生 API 和兼容 Anthropic 协议的服务
// （如 Bailian 的 Anthropic endpoint）。
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	reqBody, err := json.Marshal(body)
	return url, headers, reqBody, err
}

// ────────────────────────────────────────────────────────────
// OpenAI 适配器
// ────────────────────────────────────────────────────────────

// OpenAIAdapter 将 Anthropic Messages 格式翻译为 OpenAI Chat Completions 格式。
// 适用于 OpenAI、DeepSeek 等使用 Chat Completions API 的服务。
//
// 翻译要点：
//   - Anthropic content 可能是字符串或 [{"type":"text","text":"..."}] 数组
//   - OpenAI content 必须是字符串
//   - Anthropic 使用 x-api-key 头认证，OpenAI 使用 Authorization: Bearer
//   - Anthropic 需要 anthropic-version 头，OpenAI 不需要
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

	// 翻译消息格式：Anthropic → OpenAI
	messages := translateMessages(body)

	// Anthropic top-level system → OpenAI system message
	// Anthropic 格式：{ "system": "...", "messages": [...] }
	// OpenAI 格式：  { "messages": [{"role": "system", "content": "..."}, ...] }
	if sys, ok := body["system"].(string); ok && sys != "" {
		messages = append([]map[string]interface{}{
			{"role": "system", "content": sys},
		}, messages...)
	}

	openaiBody := map[string]interface{}{
		"model":       body["model"],
		"messages":    messages,
		"max_tokens":  body["max_tokens"],
		"temperature": body["temperature"],
		"stream":      true, // OpenAI 流式必须显式指定
	}

	// 转换认证头：x-api-key → Authorization: Bearer
	openaiHeaders := make(map[string]string)
	for k, v := range headers {
		openaiHeaders[k] = v
	}
	openaiHeaders["Authorization"] = "Bearer " + headers["x-api-key"]
	delete(openaiHeaders, "x-api-key")
	delete(openaiHeaders, "anthropic-version")

	reqBody, err := json.Marshal(openaiBody)
	return url, openaiHeaders, reqBody, err
}

// translateMessages 将 Anthropic 消息格式转换为 OpenAI 格式。
//
// Anthropic content 有两种可能：
//   - 字符串："Hello"
//   - 内容块数组：[{"type": "text", "text": "Hello"}, {"type": "image", ...}]
//
// OpenAI content 必须是字符串。本函数将内容块数组中的
// text 块提取并拼接为纯文本，丢弃非文本块。
func translateMessages(body map[string]interface{}) []map[string]interface{} {
	raw, ok := body["messages"]
	if !ok {
		return nil
	}
	msgs, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var result []map[string]interface{}
	for _, m := range msgs {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		content := msg["content"]
		// Anthropic 内容块数组 → 提取 text 并拼接
		if blocks, ok := content.([]interface{}); ok {
			var texts []string
			for _, block := range blocks {
				if b, ok := block.(map[string]interface{}); ok {
					if t, ok := b["text"].(string); ok {
						texts = append(texts, t)
					}
				}
			}
			content = strings.Join(texts, "\n")
		}

		result = append(result, map[string]interface{}{
			"role":    msg["role"],
			"content": content,
		})
	}
	return result
}

// ────────────────────────────────────────────────────────────
// 适配器工厂
// ────────────────────────────────────────────────────────────

// AdapterFor 根据供应商名称选择适配器。
//
// 规则：
//   - 名称含 "openai"/"deepseek"/"minimax"/"bailian" → OpenAI 适配器（Chat Completions 格式）
//   - 其他 → Anthropic 适配器（原生 Messages API 格式）
func AdapterFor(providerName string) ProviderAdapter {
	lower := strings.ToLower(providerName)
	openaiCompat := []string{"openai", "deepseek", "minimax", "bailian"}
	for _, keyword := range openaiCompat {
		if strings.Contains(lower, keyword) {
			return &OpenAIAdapter{}
		}
	}
	return &AnthropicAdapter{}
}

// AdapterFactory 是适配器的注册表，支持自定义适配器。
type AdapterFactory struct {
	adapters map[string]ProviderAdapter
}

// NewAdapterFactory 创建空的适配器工厂。
func NewAdapterFactory() *AdapterFactory {
	return &AdapterFactory{
		adapters: make(map[string]ProviderAdapter),
	}
}

// Get 返回供应商的适配器。
// 如果工厂中有注册的自定义适配器则使用，否则回退到 AdapterFor 的默认规则。
func (f *AdapterFactory) Get(providerName string) ProviderAdapter {
	if adapter, ok := f.adapters[providerName]; ok {
		return adapter
	}
	return AdapterFor(providerName)
}

// ────────────────────────────────────────────────────────────
// 供应商校验
// ────────────────────────────────────────────────────────────

// ValidateProvider 检查供应商配置是否可用。
// 规则：必须有 BaseURL、必须有 APIKey、不能代理到自己。
func ValidateProvider(cfg *ProviderConfig) error {
	return ValidateProviderWithPort(cfg, 15722)
}

// ValidateProviderWithPort 检查供应商配置是否可用（支持自定义端口）。
func ValidateProviderWithPort(cfg *ProviderConfig, selfPort int) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("provider %q: base_url is required", cfg.Name)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("provider %q: api_key is required", cfg.Name)
	}
	// 防止循环代理（代理到自己的端口）
	selfURLs := []string{
		fmt.Sprintf("http://localhost:%d", selfPort),
		fmt.Sprintf("http://127.0.0.1:%d", selfPort),
		fmt.Sprintf("http://[::1]:%d", selfPort),
	}
	for _, selfURL := range selfURLs {
		if strings.Contains(cfg.BaseURL, selfURL) {
			return fmt.Errorf("provider %q: cannot proxy to self (%s)", cfg.Name, selfURL)
		}
	}
	return nil
}

// HealthChecker 检查供应商 API 是否可达。
type HealthChecker struct {
	client *http.Client
}

// NewHealthChecker 创建健康检查器。
func NewHealthChecker(client *http.Client) *HealthChecker {
	return &HealthChecker{client: client}
}

// Check 向供应商的 /health 端点发送 GET 请求验证连通性。
// 返回 nil 表示健康，返回 error 表示不可达。
func (h *HealthChecker) Check(baseURL string) error {
	resp, err := h.client.Get(strings.TrimRight(baseURL, "/") + "/health")
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("health check: upstream returned %d", resp.StatusCode)
	}
	return nil
}
