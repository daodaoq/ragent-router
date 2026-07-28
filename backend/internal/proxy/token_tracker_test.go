package proxy

import (
	"bytes"
	"strings"
	"testing"
)

// =============================================================================
// TokenTracker SSE 事件解析测试
// =============================================================================

func TestTokenTracker_AnthropicUsage(t *testing.T) {
	var buf bytes.Buffer
	tracking := &RequestTracking{}
	tracker := NewTokenTracker(&buf, tracking)

	// 模拟 Anthropic SSE 流（message_start + message_delta）
	sseStream := strings.Join([]string{
		"event: message_start",
		`data: {"message":{"id":"msg_001","model":"claude-sonnet-4-20250514","usage":{"input_tokens":1500}}}`,
		"",
		"event: content_block_delta",
		`data: {"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: message_delta",
		`data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":45}}`,
		"",
	}, "\n") + "\n"

	tracker.Write([]byte(sseStream))

	if tracking.UpstreamID != "msg_001" {
		t.Errorf("UpstreamID: want=msg_001, got=%s", tracking.UpstreamID)
	}
	if tracking.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model: want=claude-sonnet-4-20250514, got=%s", tracking.Model)
	}
	if tracking.Usage.InputTokens != 1500 {
		t.Errorf("InputTokens: want=1500, got=%d", tracking.Usage.InputTokens)
	}
	if tracking.Usage.OutputTokens != 45 {
		t.Errorf("OutputTokens: want=45, got=%d", tracking.Usage.OutputTokens)
	}
	if tracking.Usage.TotalTokens != 1545 {
		t.Errorf("TotalTokens: want=1545, got=%d", tracking.Usage.TotalTokens)
	}
}

func TestTokenTracker_OpenAIUsage(t *testing.T) {
	var buf bytes.Buffer
	tracking := &RequestTracking{}
	tracker := NewTokenTracker(&buf, tracking)

	// 模拟 OpenAI/DeepSeek SSE 流（最后一个 chunk 带 usage）
	sseStream := strings.Join([]string{
		"data: [DONE]",
		"",
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","model":"deepseek-chat","usage":{"prompt_tokens":500,"completion_tokens":200,"total_tokens":700}}`,
		"",
	}, "\n") + "\n"

	tracker.Write([]byte(sseStream))

	if tracking.Usage.InputTokens != 500 {
		t.Errorf("InputTokens (prompt_tokens): want=500, got=%d", tracking.Usage.InputTokens)
	}
	if tracking.Usage.OutputTokens != 200 {
		t.Errorf("OutputTokens (completion_tokens): want=200, got=%d", tracking.Usage.OutputTokens)
	}
	if tracking.Usage.TotalTokens != 700 {
		t.Errorf("TotalTokens: want=700, got=%d", tracking.Usage.TotalTokens)
	}
}

// TestTokenTracker_CrossChunkSSE 验证跨 TCP chunk 的 SSE 事件解析。
//
// 真实场景中，SSE 事件可能被 TCP 分包切断：
//
//	Chunk 1: "event: message_start\ndata: {\"message\":{\"id\":\"msg"
//	Chunk 2: "_001\",\"model\":\"claude-3.5\",\"usage\":{\"input_t"
//	Chunk 3: "okens\":1000}}}\n\nevent: message_delta\ndata: {\"usage\":{\"output_tokens\":50}}\n\n"
//
// TokenTracker 的 accumulator 缓冲区应正确拼接不完整的行。
func TestTokenTracker_CrossChunkSSE(t *testing.T) {
	var buf bytes.Buffer
	tracking := &RequestTracking{}
	tracker := NewTokenTracker(&buf, tracking)

	// 将一个完整的 SSE 流拆成 3 个 chunk，模拟 TCP 分包
	chunks := []string{
		// Chunk 1: message_start 事件的前半部分（event 行完整，data 行被切断）
		"event: message_start\n" +
			`data: {"message":{"id":"msg`,
		// Chunk 2: data 行的中间部分
		`_001","model":"claude-3.5","usage":{"input_t`,
		// Chunk 3: data 行结束 + message_delta 事件完整
		`okens":1000}}}` + "\n\n" +
			"event: message_delta\n" +
			`data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}` + "\n\n",
	}

	for i, chunk := range chunks {
		n, err := tracker.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("chunk %d Write 错误: %v", i, err)
		}
		if n != len(chunk) {
			t.Fatalf("chunk %d 写入字节数: want=%d, got=%d", i, len(chunk), n)
		}
	}

	// 验证跨 chunk 拼接后正确解析
	if tracking.UpstreamID != "msg_001" {
		t.Errorf("UpstreamID: want=msg_001, got=%s", tracking.UpstreamID)
	}
	if tracking.Model != "claude-3.5" {
		t.Errorf("Model: want=claude-3.5, got=%s", tracking.Model)
	}
	if tracking.Usage.InputTokens != 1000 {
		t.Errorf("InputTokens: want=1000, got=%d", tracking.Usage.InputTokens)
	}
	if tracking.Usage.OutputTokens != 50 {
		t.Errorf("OutputTokens: want=50, got=%d", tracking.Usage.OutputTokens)
	}
	if tracking.Usage.TotalTokens != 1050 {
		t.Errorf("TotalTokens: want=1050, got=%d", tracking.Usage.TotalTokens)
	}
}

func TestTokenTracker_TeeWriter(t *testing.T) {
	// 验证 TokenTracker 正确透传数据到下游 writer
	var buf bytes.Buffer
	tracking := &RequestTracking{}
	tracker := NewTokenTracker(&buf, tracking)

	testData := []byte("hello world\n")
	n, err := tracker.Write(testData)
	if err != nil {
		t.Fatalf("Write 错误: %v", err)
	}
	if n != len(testData) {
		t.Errorf("写入字节数: want=%d, got=%d", len(testData), n)
	}
	if buf.String() != string(testData) {
		t.Errorf("透传数据不匹配: %q vs %q", buf.String(), string(testData))
	}
}

// =============================================================================
// Benchmark：TokenTracker 写入 + 解析开销
// =============================================================================

func BenchmarkTokenTracker_Write(b *testing.B) {
	data := []byte("event: message_delta\ndata: {\"usage\":{\"output_tokens\":100}}\n\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		tracking := &RequestTracking{}
		tracker := NewTokenTracker(&buf, tracking)
		tracker.Write(data)
	}
}
