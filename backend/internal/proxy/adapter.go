// Package proxy implements an Anthropic-compatible transparent proxy
// that routes requests to different AI providers with resilience protection.
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ProviderConfig holds the configuration for an upstream AI provider.
type ProviderConfig struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	BaseURL  string            `json:"base_url"`
	APIKey   string            `json:"api_key"`
	Model    string            `json:"model"`
	Headers  map[string]string `json:"headers"`
	Enabled  bool              `json:"enabled"`
}

// ProviderAdapter normalizes protocol differences between AI providers.
// The proxy always speaks Anthropic Messages API to Claude Code;
// adapters translate to provider-specific formats when needed.
type ProviderAdapter interface {
	// BuildRequest transforms the upstream HTTP request for this provider.
	// Returns the modified URL, headers, and body.
	BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (
		url string, reqHeaders map[string]string, reqBody []byte, err error)
}

// AnthropicAdapter passes requests through unchanged (native Anthropic format).
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	reqBody, err := json.Marshal(body)
	return url, headers, reqBody, err
}

// OpenAIAdapter translates Anthropic Messages API format to OpenAI Chat Completions format.
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (string, map[string]string, []byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

	// Translate Anthropic messages to OpenAI messages.
	messages := translateMessages(body)
	openaiBody := map[string]interface{}{
		"model":       body["model"],
		"messages":    messages,
		"max_tokens":  body["max_tokens"],
		"temperature": body["temperature"],
		"stream":      true,
	}

	// Use Bearer auth header.
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

// translateMessages converts Anthropic messages format to OpenAI format.
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
		role := msg["role"]
		content := msg["content"]

		// Anthropic content can be a string or an array of blocks.
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
			"role":    role,
			"content": content,
		})
	}
	return result
}

// AdapterFor returns the appropriate adapter for a provider by name.
// Providers with "openai" or "deepseek" in their name get the OpenAI adapter;
// everyone else gets the native Anthropic adapter.
func AdapterFor(providerName string) ProviderAdapter {
	lower := strings.ToLower(providerName)
	if strings.Contains(lower, "openai") || strings.Contains(lower, "deepseek") {
		return &OpenAIAdapter{}
	}
	return &AnthropicAdapter{}
}

// AdapterFactory creates adapters based on provider configuration.
type AdapterFactory struct {
	adapters map[string]ProviderAdapter
}

// NewAdapterFactory creates a factory with registered adapters.
func NewAdapterFactory() *AdapterFactory {
	return &AdapterFactory{
		adapters: make(map[string]ProviderAdapter),
	}
}

// Get returns the adapter for a provider, falling back to Anthropic if
// no specific adapter is registered.
func (f *AdapterFactory) Get(providerName string) ProviderAdapter {
	if adapter, ok := f.adapters[providerName]; ok {
		return adapter
	}
	return AdapterFor(providerName)
}

// ValidateProvider checks that a provider config is usable.
func ValidateProvider(cfg *ProviderConfig) error {
	if cfg.BaseURL == "" {
		return fmt.Errorf("provider %q: base_url is required", cfg.Name)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("provider %q: api_key is required", cfg.Name)
	}
	if cfg.BaseURL == "http://localhost:15722" || strings.Contains(cfg.BaseURL, "127.0.0.1:15722") {
		return fmt.Errorf("provider %q: cannot proxy to self", cfg.Name)
	}
	return nil
}

// HealthChecker checks if a provider's API is reachable.
type HealthChecker struct {
	client *http.Client
}

// NewHealthChecker creates a health checker with the given HTTP client.
func NewHealthChecker(client *http.Client) *HealthChecker {
	return &HealthChecker{client: client}
}

// Check verifies that a provider's base URL is reachable.
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
