// Package proxy 实现 Anthropic 兼容的透明代理，
// 将请求路由到不同 AI 供应商，并提供韧性引擎保护。
//
// # 协议适配
//
// AI 供应商使用不同的 API 协议：
//   - Anthropic/Claude：Messages API（SSE 流式，x-api-key 认证）
//   - OpenAI/DeepSeek/大部分国内厂商：Chat Completions API（SSE 流式，Bearer Token 认证）
//   - Google Gemini：GenerateContent API（JSON 或 SSE，x-goog-api-key 认证）
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
proxytypes "github.com/ragent/router/shared/proxytypes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

)

// ProviderConfig 是对 proxytypes.ProviderConfig 的类型别名，保持向后兼容。
type ProviderConfig = proxytypes.ProviderConfig

// ────────────────────────────────────────────────────────────
// 适配器接口
// ────────────────────────────────────────────────────────────

// ProviderAdapter 是不同 AI 供应商协议的适配器接口。
type ProviderAdapter interface {
	// BuildRequest 为指定供应商构建上游 HTTP 请求。
	BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (
		url string, reqHeaders map[string]string, reqBody []byte, err error)
}

// ────────────────────────────────────────────────────────────
// Anthropic 适配器（原生透传）
// ────────────────────────────────────────────────────────────

// AnthropicAdapter 原生透传，不做格式转换。
// 适用于 Anthropic 原生 API 和兼容 Anthropic 协议的服务。
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	reqBody, err := json.Marshal(body)
	return url, headers, reqBody, err
}

// ────────────────────────────────────────────────────────────
// OpenAI 适配器（Chat Completions 格式）
// ────────────────────────────────────────────────────────────

// OpenAIAdapter 将 Anthropic Messages 格式翻译为 OpenAI Chat Completions 格式。
// 适用于 OpenAI、DeepSeek、MiniMax、通义、文心、GLM、Kimi、混元、星火、豆包、
// Mistral、Cohere、Perplexity、xAI、Ollama、OpenRouter、SiliconFlow 等。
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

	messages := translateMessages(body)

	// Anthropic top-level system → OpenAI system message
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
		"stream":      true,
	}

	// 保留 top_k、top_p 等参数
	for _, key := range []string{"top_k", "top_p", "stop", "presence_penalty", "frequency_penalty"} {
		if v, ok := body[key]; ok {
			openaiBody[key] = v
		}
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

// ────────────────────────────────────────────────────────────
// Gemini 适配器（Google GenerateContent 格式）
// ────────────────────────────────────────────────────────────

// GeminiAdapter 将 Anthropic Messages 格式翻译为 Google Gemini GenerateContent 格式。
type GeminiAdapter struct{}

func (a *GeminiAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	model, _ := body["model"].(string)
	url := strings.TrimRight(baseURL, "/") + "/v1beta/models/" + model + ":streamGenerateContent"

	// 构建 Gemini contents
	contents := translateToGeminiContents(body)

	geminiBody := map[string]interface{}{
		"contents": contents,
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": body["max_tokens"],
			"temperature":     body["temperature"],
		},
	}

	// 认证：x-api-key → x-goog-api-key 或 query param
	geminiHeaders := make(map[string]string)
	for k, v := range headers {
		geminiHeaders[k] = v
	}
	geminiHeaders["x-goog-api-key"] = headers["x-api-key"]
	geminiHeaders["Content-Type"] = "application/json"
	delete(geminiHeaders, "x-api-key")
	delete(geminiHeaders, "anthropic-version")

	reqBody, err := json.Marshal(geminiBody)
	return url, geminiHeaders, reqBody, err
}

func translateToGeminiContents(body map[string]interface{}) []map[string]interface{} {
	raw, ok := body["messages"]
	if !ok {
		return nil
	}
	msgs, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var contents []map[string]interface{}
	for _, m := range msgs {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		role, _ := msg["role"].(string)
		// Gemini 使用 "user" 和 "model"（不是 "assistant"）
		if role == "assistant" {
			role = "model"
		}

		content := extractTextContent(msg["content"])
		contents = append(contents, map[string]interface{}{
			"role": role,
			"parts": []map[string]interface{}{
				{"text": content},
			},
		})
	}
	return contents
}

// ────────────────────────────────────────────────────────────
// 百度文心适配器
// ────────────────────────────────────────────────────────────

// BaiduAdapter 将 Anthropic Messages 格式翻译为百度文心 API 格式。
type BaiduAdapter struct{}

func (a *BaiduAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	model, _ := body["model"].(string)
	url := strings.TrimRight(baseURL, "/") + "/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/" + model

	messages := translateMessages(body)
	if sys, ok := body["system"].(string); ok && sys != "" {
		messages = append([]map[string]interface{}{
			{"role": "user", "content": sys},
		}, messages...)
	}

	baiduBody := map[string]interface{}{
		"messages": messages,
		"stream":   true,
	}

	// 百度使用 access_token 认证（query 参数）
	baiduHeaders := make(map[string]string)
	baiduHeaders["Content-Type"] = "application/json"

	reqBody, err := json.Marshal(baiduBody)
	return url, baiduHeaders, reqBody, err
}

// ────────────────────────────────────────────────────────────
// 通用 HTTP 适配器（自定义端点）
// ────────────────────────────────────────────────────────────

// CustomAdapter 支持自定义端点路径和认证方式。
type CustomAdapter struct {
	Endpoint     string // 自定义端点路径，如 "/v1/chat/completions"
	AuthHeader   string // 认证头名称，默认 "Authorization"
	AuthPrefix   string // 认证前缀，默认 "Bearer "
	BodyTemplate func(body map[string]interface{}) map[string]interface{} // 自定义 body 转换
}

func (a *CustomAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = "/v1/chat/completions"
	}
	url := strings.TrimRight(baseURL, "/") + endpoint

	var reqBodyMap map[string]interface{}
	if a.BodyTemplate != nil {
		reqBodyMap = a.BodyTemplate(body)
	} else {
		messages := translateMessages(body)
		if sys, ok := body["system"].(string); ok && sys != "" {
			messages = append([]map[string]interface{}{
				{"role": "system", "content": sys},
			}, messages...)
		}
		reqBodyMap = map[string]interface{}{
			"model":    body["model"],
			"messages": messages,
			"stream":   true,
		}
	}

	customHeaders := make(map[string]string)
	for k, v := range headers {
		customHeaders[k] = v
	}

	authHeader := a.AuthHeader
	if authHeader == "" {
		authHeader = "Authorization"
	}
	authPrefix := a.AuthPrefix
	if authPrefix == "" {
		authPrefix = "Bearer "
	}
	customHeaders[authHeader] = authPrefix + headers["x-api-key"]
	delete(customHeaders, "x-api-key")
	delete(customHeaders, "anthropic-version")

	reqBody, err := json.Marshal(reqBodyMap)
	return url, customHeaders, reqBody, err
}

// ────────────────────────────────────────────────────────────
// 辅助函数
// ────────────────────────────────────────────────────────────

// translateMessages 将 Anthropic 消息格式转换为 OpenAI 格式。
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
		if blocks, ok := content.([]interface{}); ok {
			content = extractTextFromBlocks(blocks)
		}

		result = append(result, map[string]interface{}{
			"role":    msg["role"],
			"content": content,
		})
	}
	return result
}

// extractTextContent 从 content 字段提取纯文本。
func extractTextContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		return extractTextFromBlocks(v)
	default:
		return fmt.Sprintf("%v", content)
	}
}

// extractTextFromBlocks 从内容块数组中提取文本。
func extractTextFromBlocks(blocks []interface{}) string {
	var texts []string
	for _, block := range blocks {
		if b, ok := block.(map[string]interface{}); ok {
			if t, ok := b["text"].(string); ok {
				texts = append(texts, t)
			}
		}
	}
	return strings.Join(texts, "\n")
}

// ────────────────────────────────────────────────────────────
// 适配器工厂
// ────────────────────────────────────────────────────────────

var (
	anthropicAdapter = &AnthropicAdapter{}
	openaiAdapter    = &OpenAIAdapter{}
	geminiAdapter    = &GeminiAdapter{}
	baiduAdapter     = &BaiduAdapter{}
)

// adapterFor 根据供应商名称选择适配器。
func adapterFor(providerName string) ProviderAdapter {
	lower := strings.ToLower(providerName)

	// Anthropic 原生
	if strings.Contains(lower, "anthropic") || strings.Contains(lower, "claude") {
		return anthropicAdapter
	}
	// Google Gemini
	if strings.Contains(lower, "gemini") || strings.Contains(lower, "google") {
		return geminiAdapter
	}
	// 百度文心
	if strings.Contains(lower, "baidu") || strings.Contains(lower, "wenxin") {
		return baiduAdapter
	}

	// 其他全部使用 OpenAI 兼容格式
	return openaiAdapter
}

// AdapterFactory 是适配器的注册表，支持自定义适配器。
type AdapterFactory struct {
	adapters map[string]ProviderAdapter
}

// NewAdapterFactory 创建适配器工厂并预注册默认适配器。
func NewAdapterFactory() *AdapterFactory {
	return &AdapterFactory{
		adapters: make(map[string]ProviderAdapter),
	}
}

// Get 返回供应商的适配器。
func (f *AdapterFactory) Get(providerName string) ProviderAdapter {
	if adapter, ok := f.adapters[providerName]; ok {
		return adapter
	}
	return adapterFor(providerName)
}

// Register 注册自定义适配器。
func (f *AdapterFactory) Register(name string, adapter ProviderAdapter) {
	f.adapters[name] = adapter
}

// ────────────────────────────────────────────────────────────
// 供应商校验
// ────────────────────────────────────────────────────────────

// ValidateProvider 检查供应商配置是否可用。
func ValidateProvider(cfg *proxytypes.ProviderConfig) error {
	return ValidateProviderWithPort(cfg, 15722)
}

// ValidateProviderWithPort 检查供应商配置是否可用（支持自定义端口）。
func ValidateProviderWithPort(cfg *proxytypes.ProviderConfig, selfPort int) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("provider %q: base_url is required", cfg.Name)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("provider %q: api_key is required", cfg.Name)
	}
	selfURLs := []string{
		fmt.Sprintf("http://localhost:%d", selfPort),
		fmt.Sprintf("http://127.0.0.1:%d", selfPort),
	}
	for _, selfURL := range selfURLs {
		if strings.Contains(cfg.BaseURL, selfURL) {
			return fmt.Errorf("provider %q: cannot proxy to self", cfg.Name)
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

// Check 向供应商发送健康检查请求。
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
