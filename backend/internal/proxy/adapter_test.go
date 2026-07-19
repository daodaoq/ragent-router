package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// =============================================================================
// 表驱动测试：协议翻译正确性（Anthropic ↔ OpenAI/DeepSeek 格式转换）
// =============================================================================

func TestOpenAIAdapter_BuildRequest(t *testing.T) {
	adapter := &OpenAIAdapter{}
	headers := map[string]string{
		"x-api-key":         "sk-test-key",
		"anthropic-version": "2023-06-01",
		"content-type":      "application/json",
	}

	cases := []struct {
		name       string
		body       map[string]interface{}
		wantModel  string
		wantTokens float64
		wantStream bool
		wantAuth   string
		checkBody  func(t *testing.T, body map[string]interface{})
	}{
		{
			name: "基础聊天请求",
			body: map[string]interface{}{
				"model":    "deepseek-chat",
				"messages": []interface{}{map[string]interface{}{"role": "user", "content": "Hello"}},
			},
			wantModel:  "deepseek-chat",
			wantStream: true,
			wantAuth:   "Bearer sk-test-key",
		},
		{
			name: "带 max_tokens 和 temperature",
			body: map[string]interface{}{
				"model":       "deepseek-chat",
				"max_tokens":  float64(4096),
				"temperature": 0.7,
				"messages":    []interface{}{map[string]interface{}{"role": "user", "content": "Explain Go interfaces"}},
			},
			wantModel:  "deepseek-chat",
			wantTokens: 4096,
			wantStream: true,
		},
		{
			name: "Anthropic system 字段翻译为 OpenAI system 消息",
			body: map[string]interface{}{
				"model":    "deepseek-chat",
				"system":   "You are a helpful coding assistant.",
				"messages": []interface{}{map[string]interface{}{"role": "user", "content": "Write a function"}},
			},
			wantModel: "deepseek-chat",
			checkBody: func(t *testing.T, body map[string]interface{}) {
				msgs, ok := body["messages"].([]interface{})
				if !ok {
					t.Fatal("messages 字段缺失或类型错误")
				}
				if len(msgs) == 0 {
					t.Fatal("messages 为空")
				}
				first := msgs[0].(map[string]interface{})
				if first["role"] != "system" {
					t.Errorf("第一条消息 role 应为 system，实际为 %v", first["role"])
				}
				if first["content"] != "You are a helpful coding assistant." {
					t.Errorf("system content 不匹配: %v", first["content"])
				}
			},
		},
		{
			name: "认证头正确转换（x-api-key → Bearer）",
			body: map[string]interface{}{
				"model":    "openai-gpt-4",
				"messages": []interface{}{map[string]interface{}{"role": "user", "content": "Test"}},
			},
			wantAuth: "Bearer sk-test-key",
		},
		{
			name: "anthropic-version 头被移除",
			body: map[string]interface{}{
				"model":    "openai-gpt-4",
				"messages": []interface{}{map[string]interface{}{"role": "user", "content": "Test"}},
			},
			checkBody: func(t *testing.T, body map[string]interface{}) {
				// 不做额外检查，由 wantAuth 验证认证头
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, reqHeaders, reqBody, err := adapter.BuildRequest(
				"https://api.deepseek.com", headers, c.body,
			)

			if err != nil {
				t.Fatalf("BuildRequest 返回错误: %v", err)
			}

			// 验证 URL
			if !strings.Contains(url, "/v1/chat/completions") {
				t.Errorf("URL 应包含 /v1/chat/completions，实际: %s", url)
			}

			// 验证认证头
			if c.wantAuth != "" {
				if auth := reqHeaders["Authorization"]; auth != c.wantAuth {
					t.Errorf("Authorization 头: want=%q, got=%q", c.wantAuth, auth)
				}
			}

			// 验证 x-api-key 已被删除
			if _, ok := reqHeaders["x-api-key"]; ok {
				t.Error("x-api-key 头应被删除")
			}

			// 验证 anthropic-version 已被删除
			if _, ok := reqHeaders["anthropic-version"]; ok {
				t.Error("anthropic-version 头应被删除")
			}

			// 解析请求体到 map
			var bodyMap map[string]interface{}
			if err := json.Unmarshal(reqBody, &bodyMap); err != nil {
				t.Fatalf("反序列化请求体失败: %v", err)
			}

			// 验证 model
			if c.wantModel != "" {
				if model, _ := bodyMap["model"].(string); model != c.wantModel {
					t.Errorf("model: want=%q, got=%q", c.wantModel, model)
				}
			}

			// 验证 stream 为 true
			if c.wantStream {
				if stream, _ := bodyMap["stream"].(bool); !stream {
					t.Error("stream 应为 true")
				}
			}

			// 自定义验证
			if c.checkBody != nil {
				c.checkBody(t, bodyMap)
			}
		})
	}
}

func TestAnthropicAdapter_BuildRequest(t *testing.T) {
	adapter := &AnthropicAdapter{}
	headers := map[string]string{
		"x-api-key":         "sk-ant-test",
		"anthropic-version": "2023-06-01",
	}

	body := map[string]interface{}{
		"model":    "claude-sonnet-4-20250514",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "Hello"}},
	}

	url, reqHeaders, reqBody, err := adapter.BuildRequest(
		"https://api.anthropic.com", headers, body,
	)

	if err != nil {
		t.Fatalf("BuildRequest 返回错误: %v", err)
	}

	// Anthropic 适配器原样透传
	if !strings.Contains(url, "/v1/messages") {
		t.Errorf("URL 应包含 /v1/messages，实际: %s", url)
	}
	if reqHeaders["x-api-key"] != "sk-ant-test" {
		t.Errorf("x-api-key 应当保留")
	}
	if reqHeaders["anthropic-version"] != "2023-06-01" {
		t.Errorf("anthropic-version 应当保留")
	}

	var bodyMap map[string]interface{}
	json.Unmarshal(reqBody, &bodyMap)
	if model, _ := bodyMap["model"].(string); model != "claude-sonnet-4-20250514" {
		t.Errorf("model 不匹配: %s", model)
	}
}

// =============================================================================
// 适配器选择测试
// =============================================================================

func TestAdapterFactory_Get(t *testing.T) {
	factory := NewAdapterFactory()

	cases := []struct {
		providerName string
		wantType     string
	}{
		{"DeepSeek", "OpenAI"},
		{"deepseek", "OpenAI"},
		{"OpenAI", "OpenAI"},
		{"MiniMax", "OpenAI"},
		{"BaiLian", "OpenAI"},
		{"Claude", "Anthropic"},
		{"Anthropic", "Anthropic"},
		{"unknown-provider", "Anthropic"},
	}

	for _, c := range cases {
		t.Run(c.providerName, func(t *testing.T) {
			adapter := factory.Get(c.providerName)
			switch c.wantType {
			case "OpenAI":
				if _, ok := adapter.(*OpenAIAdapter); !ok {
					t.Errorf("%s 应返回 OpenAIAdapter", c.providerName)
				}
			case "Anthropic":
				if _, ok := adapter.(*AnthropicAdapter); !ok {
					t.Errorf("%s 应返回 AnthropicAdapter", c.providerName)
				}
			}
		})
	}
}

// =============================================================================
// Benchmark：适配器创建开销
// =============================================================================

func BenchmarkAdapterFactory_Get(b *testing.B) {
	factory := NewAdapterFactory()
	names := []string{"DeepSeek", "Claude", "MiniMax", "Bailian", "OpenAI"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		factory.Get(names[i%len(names)])
	}
}
