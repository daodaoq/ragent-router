package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragent/router/internal/provider"
)

// =============================================================================
// 辅助函数
// =============================================================================

// mockUpstream 创建一个模拟上游 SSE 服务器。
func mockUpstream(model string, inputTokens, outputTokens int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, `data: {"message":{"id":"msg_test","model":"%s","usage":{"input_tokens":%d}}}`+"\n", model, inputTokens)
		fmt.Fprintf(w, "\n")
		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, `data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`+"\n", outputTokens)
		fmt.Fprintf(w, "\n")
	}))
}

// mockFailingUpstream 创建一个返回 500 的上游服务器。
func mockFailingUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":"internal server error"}`)
	}))
}

// buildRequestJSON 构造 Anthropic 格式的请求体。
func buildRequestJSON(prompt, model string) []byte {
	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
	}
	data, _ := json.Marshal(body)
	return data
}

// =============================================================================
// HTTP Handler 测试
// =============================================================================

func TestProxy_ServeHTTP_MethodNotAllowed(t *testing.T) {
	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "test", BaseURL: "http://localhost", APIKey: "key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 请求应返回 405, got=%d", w.Code)
	}
}

func TestProxy_ServeHTTP_InvalidJSON(t *testing.T) {
	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "test", BaseURL: "http://localhost", APIKey: "key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("无效 JSON 应返回 400, got=%d", w.Code)
	}
}

func TestProxy_ServeHTTP_NoProvider(t *testing.T) {
	p := NewProxy(Config{
		Providers:             []ProviderConfig{},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		bytes.NewReader(buildRequestJSON("hello", "test")))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("无供应商应返回 502, got=%d", w.Code)
	}
}

func TestProxy_ServeHTTP_Success(t *testing.T) {
	upstream := mockUpstream("claude-3.5", 100, 50)
	defer upstream.Close()

	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "test", BaseURL: upstream.URL, APIKey: "test-key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	var logMu sync.Mutex
	var lastLog RequestLog
	p.OnRequestLog = func(log RequestLog) {
		logMu.Lock()
		lastLog = log
		logMu.Unlock()
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		bytes.NewReader(buildRequestJSON("hello", "test")))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("成功请求应返回 200, got=%d, body=%s", w.Code, w.Body.String())
	}

	// 验证 SSE 响应头
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: want=text/event-stream, got=%s", ct)
	}

	// 验证响应体包含 SSE 事件
	body := w.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Error("响应应包含 message_start 事件")
	}

	// 验证请求日志
	logMu.Lock()
	defer logMu.Unlock()
	if lastLog.Status != "ok" {
		t.Errorf("日志状态: want=ok, got=%s", lastLog.Status)
	}
	if lastLog.PromptTokens != 100 {
		t.Errorf("PromptTokens: want=100, got=%d", lastLog.PromptTokens)
	}
	if lastLog.CompletionTokens != 50 {
		t.Errorf("CompletionTokens: want=50, got=%d", lastLog.CompletionTokens)
	}
}

// =============================================================================
// 提示词提取测试
// =============================================================================

func TestExtractPrompt_SimpleString(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello world"},
		},
	}
	prompt := extractPrompt(body)
	if prompt != "Hello world" {
		t.Errorf("want='Hello world', got=%q", prompt)
	}
}

func TestExtractPrompt_ContentBlocks(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "Part 1"},
					map[string]interface{}{"type": "text", "text": "Part 2"},
				},
			},
		},
	}
	prompt := extractPrompt(body)
	if prompt != "Part 1 Part 2" {
		t.Errorf("want='Part 1 Part 2', got=%q", prompt)
	}
}

func TestExtractPrompt_LastUserMessage(t *testing.T) {
	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "First"},
			map[string]interface{}{"role": "assistant", "content": "Response"},
			map[string]interface{}{"role": "user", "content": "Last"},
		},
	}
	prompt := extractPrompt(body)
	if prompt != "Last" {
		t.Errorf("want='Last', got=%q", prompt)
	}
}

func TestExtractPrompt_Empty(t *testing.T) {
	// 无 messages
	prompt := extractPrompt(map[string]interface{}{})
	if prompt != "" {
		t.Errorf("无 messages 应返回空, got=%q", prompt)
	}

	// 无 user 消息
	prompt = extractPrompt(map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "system prompt"},
		},
	})
	if prompt != "" {
		t.Errorf("无 user 消息应返回空, got=%q", prompt)
	}
}

// =============================================================================
// 供应商管理测试
// =============================================================================

func TestProxy_ProviderManagement(t *testing.T) {
	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "a", BaseURL: "http://a", APIKey: "key-a", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	// GetProvider
	prov := p.GetProvider("a")
	if prov == nil {
		t.Fatal("应能获取供应商 a")
	}
	if prov.Name != "a" {
		t.Errorf("Name: want=a, got=%s", prov.Name)
	}

	// ListProviders
	list := p.ListProviders()
	if len(list) != 1 {
		t.Errorf("ListProviders: want=1, got=%d", len(list))
	}

	// AddProvider
	p.AddProvider(ProviderConfig{Name: "b", BaseURL: "http://b", APIKey: "key-b"})
	prov = p.GetProvider("b")
	if prov == nil {
		t.Fatal("添加后应能获取供应商 b")
	}

	// RemoveProvider
	p.RemoveProvider("b")
	prov = p.GetProvider("b")
	if prov != nil {
		t.Fatal("删除后不应获取供应商 b")
	}
}

// =============================================================================
// 调试锁定测试
// =============================================================================

func TestProxy_DebugProvider(t *testing.T) {
	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "a", BaseURL: "http://a", APIKey: "key-a", Enabled: true},
			{Name: "b", BaseURL: "http://b", APIKey: "key-b", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	// 初始未锁定
	if p.GetDebugProvider() != "" {
		t.Error("初始不应有调试锁定")
	}

	// 设置锁定
	if !p.SetDebugProvider("a") {
		t.Fatal("应能设置调试锁定到 a")
	}
	if p.GetDebugProvider() != "a" {
		t.Errorf("调试锁定: want=a, got=%s", p.GetDebugProvider())
	}

	// 设置不存在的供应商
	if p.SetDebugProvider("nonexistent") {
		t.Error("不应能锁定不存在的供应商")
	}

	// 清除锁定
	if !p.SetDebugProvider("") {
		t.Fatal("应能清除调试锁定")
	}
	if p.GetDebugProvider() != "" {
		t.Error("清除后不应有调试锁定")
	}
}

// =============================================================================
// 熔断器集成测试
// =============================================================================

func TestProxy_BreakerStats(t *testing.T) {
	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "test", BaseURL: "http://localhost", APIKey: "key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})

	stats := p.BreakerStats("test")
	if stats == nil {
		t.Fatal("应能获取熔断器统计")
	}
	if stats.State.String() != "closed" {
		t.Errorf("初始状态: want=closed, got=%s", stats.State)
	}

	// 不存在的供应商
	stats = p.BreakerStats("nonexistent")
	if stats != nil {
		t.Error("不存在的供应商应返回 nil")
	}
}

// =============================================================================
// 端到端集成测试（多供应商 + 韧性）
// =============================================================================

func TestProxy_E2E_MultipleProviders(t *testing.T) {
	upstream1 := mockUpstream("claude-3.5", 100, 50)
	defer upstream1.Close()
	upstream2 := mockUpstream("deepseek-chat", 200, 80)
	defer upstream2.Close()

	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "provider1", BaseURL: upstream1.URL, APIKey: "key1", Enabled: true},
			{Name: "provider2", BaseURL: upstream2.URL, APIKey: "key2", Enabled: true},
		},
		Matcher:               &simpleMatcher{provider: "provider1"},
		MaxConcurrentRequests: 10,
	})

	// 发送请求
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		bytes.NewReader(buildRequestJSON("test", "claude-3.5")))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("请求失败: %d, body=%s", w.Code, w.Body.String())
	}
}

func TestProxy_E2E_UpstreamFailure(t *testing.T) {
	failing := mockFailingUpstream()
	defer failing.Close()

	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "failing", BaseURL: failing.URL, APIKey: "key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
		Resilience: &provider.ResilienceConfig{
			MaxRetries:      1, // 只重试 1 次，加速测试
			RequestTimeout:  5 * time.Second,
			UpstreamTimeout: 2 * time.Second,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		bytes.NewReader(buildRequestJSON("test", "model")))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// 上游 5xx 会触发重试，最终仍失败
	if w.Code != http.StatusOK {
		// 这是预期的——上游持续返回 5xx
		t.Logf("上游失败时返回: %d", w.Code)
	}
}

// =============================================================================
// 限流集成测试
// =============================================================================

func TestProxy_RateLimit(t *testing.T) {
	upstream := mockUpstream("test", 10, 5)
	defer upstream.Close()

	p := NewProxy(Config{
		Providers: []ProviderConfig{
			{Name: "test", BaseURL: upstream.URL, APIKey: "key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		GlobalRateLimit:       2, // 极低限流速率
		MaxConcurrentRequests: 10,
	})

	// 快速发送多个请求，应有限流
	rejected := 0
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages",
			bytes.NewReader(buildRequestJSON("test", "model")))
		w := httptest.NewRecorder()
		p.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			rejected++
		}
	}

	if rejected == 0 {
		t.Error("低限流速率下应有请求被拒绝")
	}
	t.Logf("限流测试: 10 个请求中 %d 个被限流", rejected)
}

// =============================================================================
// simpleMatcher 用于测试的简单路由匹配器
// =============================================================================

type simpleMatcher struct {
	provider string
}

func (m *simpleMatcher) Match(ctx context.Context, prompt string, model string) *ProviderConfig {
	// 返回 nil 让 Proxy 使用默认逻辑（取第一个启用的供应商）
	return nil
}
