package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

// ────────────────────────────────────────────────────────────
// 数据结构
// ────────────────────────────────────────────────────────────

// TokenUsage 是从 SSE 流中提取的 Token 消耗量。
// 所有字段取流式事件中的最大值（因为 usage 可能跨多个 chunk 更新）。
type TokenUsage struct {
	InputTokens        int `json:"input_tokens"`         // 提示词 Token 数
	OutputTokens       int `json:"output_tokens"`        // 生成 Token 数
	CacheReadTokens    int `json:"cache_read_tokens"`    // 缓存读取 Token 数
	CacheCreationTokens int `json:"cache_creation_tokens"` // 缓存创建 Token 数
	TotalTokens        int `json:"total_tokens"`         // 总 Token 数（Input + Output）
}

// RequestTracking 是流式请求过程中收集的元数据。
type RequestTracking struct {
	RequestID     string     `json:"request_id"`      // 本网关生成的请求 ID
	UpstreamID    string     `json:"upstream_request_id"` // 上游返回的消息 ID
	Model         string     `json:"model"`            // 实际使用的模型名
	Usage         TokenUsage `json:"usage"`            // Token 消耗量
	ContentLength int64      `json:"content_length"`   // 响应内容长度
}

// ────────────────────────────────────────────────────────────
// SSE Token 解析器
// ────────────────────────────────────────────────────────────

// TokenTracker 解析 SSE（Server-Sent Events）流式数据，
// 从中提取 Token 用量信息。
//
// # Anthropic SSE 协议关键事件
//
//	event: message_start
//	data: {"message": {"id": "msg_xxx", "model": "claude-..."}}
//	→ 提取 message.id 和 model
//
//	event: message_delta
//	data: {"delta": {...}, "usage": {"output_tokens": 150}}
//	→ 提取 usage 字段
//
//	event: message_stop
//	data: {}
//	→ 流结束，不需要特殊处理
//
// # 实现方式
//
// TokenTracker 包装了一个 io.Writer（实际就是 HTTP ResponseWriter）。
// 所有数据正常透传给底层 Writer，同时按行扫描 SSE 事件。
// 这种"拦截 + 透传"的方式对下游完全透明。
type TokenTracker struct {
	writer io.Writer         // 底层 Writer（HTTP ResponseWriter）
	track  *RequestTracking  // 收集到的元数据（修改此对象）
	mu     sync.Mutex        // 保护 track 的并发写入
	buf    bytes.Buffer      // 内部缓冲区（当前未使用，预留）
}

// NewTokenTracker 创建解析器。
// w 是数据实际写入的目标（通常是 HTTP ResponseWriter），
// track 中的字段会在流式传输过程中被逐步填充。
func NewTokenTracker(w io.Writer, track *RequestTracking) *TokenTracker {
	return &TokenTracker{
		writer: w,
		track:  track,
	}
}

// Write 实现 io.Writer。
// 数据先完整写入底层 Writer（保证客户端正常收到），
// 然后扫描其中包含的 SSE 事件来提取 usage。
func (t *TokenTracker) Write(p []byte) (int, error) {
	// 1. 透传给底层 Writer——客户端先收到数据。
	n, err := t.writer.Write(p)
	if err != nil {
		return n, err
	}

	// 2. 扫描 SSE 事件，提取 usage（不影响客户端）。
	t.scanForUsage(p)
	return n, nil
}

// scanForUsage 解析 SSE 数据块，提取事件类型和 payload。
//
// SSE 格式：
//
//	event: <事件类型>\n
//	data: <JSON payload>\n
//	\n                        ← 空行表示事件结束
//
// 注意：一个 TCP chunk 可能包含多个 SSE 事件，
// 也可能包含不完整的事件（被 buffer 边界切断）。
// 当前实现简单处理——按行扫描，不处理跨 chunk 的事件。
// 生产环境中应使用 bufio.Scanner + 跨 chunk 缓存。
func (t *TokenTracker) scanForUsage(chunk []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	scanner := bufio.NewScanner(bytes.NewReader(chunk))
	scanner.Buffer(make([]byte, 64*1024), 64*1024) // 最大 64KB 的行

	var currentEvent string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if line == "" && len(dataLines) > 0 {
			// 空行 = 一个 SSE 事件结束。
			t.processEvent(currentEvent, strings.Join(dataLines, ""))
			currentEvent = ""
			dataLines = nil
		}
	}

	// 处理 chunk 末尾可能在 data 后面没有空行的情况。
	if len(dataLines) > 0 {
		t.processEvent(currentEvent, strings.Join(dataLines, ""))
	}
}

// processEvent 根据事件类型提取数据。
func (t *TokenTracker) processEvent(eventType string, data string) {
	if data == "" || data == "[DONE]" {
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return // JSON 解析失败，静默跳过
	}

	switch eventType {
	case "message_start":
		// 提取 message.id（上游请求 ID）和 model
		if msg, ok := payload["message"].(map[string]interface{}); ok {
			if id, ok := msg["id"].(string); ok && t.track.UpstreamID == "" {
				t.track.UpstreamID = id
			}
			if model, ok := msg["model"].(string); ok {
				t.track.Model = model
			}
			// message_start 中可能也包含 usage（预填充）
			t.extractUsage(msg)
		}

	case "message_delta":
		// 记录 content delta 的长度
		if _, ok := payload["delta"].(map[string]interface{}); ok {
			t.track.ContentLength += int64(len(data))
		}
		t.extractUsage(payload)

	case "message_stop":
		// 流结束，usage 已在前面的 message_delta 中提取完毕。

	case "ping":
		// 心跳包，忽略。

	default:
		// 未知事件类型——尝试从中提取 usage（乐观处理）。
		t.extractUsage(payload)
	}
}

// extractUsage 从事件 payload 中提取 Token 用量。
//
// Token 用量可能出现在多个事件中的不同位置
// （message_start.usage, message_delta.usage, message.usage）。
// 使用 max() 取最大值——因为同一个请求的 usage
// 可能跨多个 chunk 逐步更新。
func (t *TokenTracker) extractUsage(payload map[string]interface{}) {
	raw, ok := payload["usage"]
	if !ok {
		return
	}
	usage, ok := raw.(map[string]interface{})
	if !ok {
		return
	}

	// 逐个提取并用 max() 防止后续事件中的低值覆盖高值。
	if v, ok := usage["input_tokens"].(float64); ok {
		t.track.Usage.InputTokens = max(t.track.Usage.InputTokens, int(v))
	}
	if v, ok := usage["output_tokens"].(float64); ok {
		t.track.Usage.OutputTokens = max(t.track.Usage.OutputTokens, int(v))
	}
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		t.track.Usage.CacheReadTokens = max(t.track.Usage.CacheReadTokens, int(v))
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		t.track.Usage.CacheCreationTokens = max(t.track.Usage.CacheCreationTokens, int(v))
	}

	// 总 Token = 输入 + 输出（不含缓存 Token）
	t.track.Usage.TotalTokens = t.track.Usage.InputTokens + t.track.Usage.OutputTokens
}
