package proxy

import (
	"context"
	"testing"

	"github.com/ragent/router/shared/proxytypes"
)

func TestAdapterRegistry(t *testing.T) {
	factory := NewAdapterFactory()
	names := []string{"Anthropic", "OpenAI", "Gemini", "DeepSeek", "Custom"}
	for _, name := range names {
		adapter := factory.Get(name)
		if adapter == nil {
			t.Errorf("适配器 %s 未注册", name)
		}
	}
	adapter := factory.Get("Unknown")
	if adapter == nil {
		t.Error("未知适配器应返回默认适配器")
	}
}

func TestOpenAIAdapter_BuildRequest(t *testing.T) {
	adapter := &OpenAIAdapter{}
	body := map[string]interface{}{
		"model": "gpt-4",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	url, headers, reqBody, err := adapter.BuildRequest(
		"https://api.openai.com",
		map[string]string{"x-api-key": "test-key"},
		body,
	)
	if err != nil {
		t.Fatalf("BuildRequest 错误: %v", err)
	}
	if url != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("URL: got=%s", url)
	}
	if headers["Authorization"] != "Bearer test-key" {
		t.Errorf("Authorization: got=%s", headers["Authorization"])
	}
	if reqBody == nil {
		t.Error("reqBody 不应为 nil")
	}
}

func TestTokenTracker_ParseAnthropicUsage(t *testing.T) {
	var buf mockWriter
	track := &RequestTracking{}
	tracker := NewTokenTracker(&buf, track)
	sse := "event: message_start\ndata: {\"message\":{\"id\":\"msg_001\",\"model\":\"claude-3.5\",\"usage\":{\"input_tokens\":100}}}\n\nevent: message_delta\ndata: {\"usage\":{\"output_tokens\":50}}\n\n"
	tracker.Write([]byte(sse))
	if track.UpstreamID != "msg_001" {
		t.Errorf("UpstreamID: want=msg_001, got=%s", track.UpstreamID)
	}
	if track.Usage.InputTokens != 100 {
		t.Errorf("InputTokens: want=100, got=%d", track.Usage.InputTokens)
	}
}

func TestTokenTracker_CrossChunk(t *testing.T) {
	var buf mockWriter
	track := &RequestTracking{}
	tracker := NewTokenTracker(&buf, track)
	tracker.Write([]byte("event: message_start\ndata: {\"message\":{\"id\":\"msg"))
	tracker.Write([]byte("_001\",\"model\":\"claude-3.5\",\"usage\":{\"input_t"))
	tracker.Write([]byte("okens\":1000}}}\n\nevent: message_delta\ndata: {\"usage\":{\"output_tokens\":50}}\n\n"))
	tracker.Flush()
	if track.Usage.InputTokens != 1000 {
		t.Errorf("InputTokens: want=1000, got=%d", track.Usage.InputTokens)
	}
}

func TestProxy_ConcurrentProviderAccess(t *testing.T) {
	p := NewProxy(Config{
		Providers: []proxytypes.ProviderConfig{
			{Name: "test1", BaseURL: "http://localhost", APIKey: "key", Enabled: true},
		},
		Matcher:               &simpleMatcher{},
		MaxConcurrentRequests: 10,
	})
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func() {
			_ = p.ListProviders()
			_ = p.GetProvider("test1")
			_ = p.GetDebugProvider()
			done <- true
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}

type mockWriter struct{ data []byte }
func (w *mockWriter) Write(p []byte) (int, error) { w.data = append(w.data, p...); return len(p), nil }

type simpleMatcher struct{}
func (m *simpleMatcher) Match(ctx context.Context, prompt string, modelName string) *proxytypes.ProviderConfig { return nil }
