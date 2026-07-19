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
//
// # 跨 chunk 解析
//
// bufio.Scanner 在 TokenTracker 生命周期内复用，配合 bytes.Buffer 缓冲
// 不完整的行，解决 SSE 事件被 TCP chunk 边界切断的问题。
type TokenTracker struct {
	writer  io.Writer         // 底层 Writer（HTTP ResponseWriter）
	track   *RequestTracking  // 收集到的元数据（修改此对象）
	mu      sync.Mutex        // 保护 track 的并发写入
	buf     bytes.Buffer      // 跨 chunk 缓冲（保存上次未完成的行）
	scanner *bufio.Scanner    // 跨 chunk 复用的扫描器
}

// NewTokenTracker 创建解析器。
// w 是数据实际写入的目标（通常是 HTTP ResponseWriter），
// track 中的字段会在流式传输过程中被逐步填充。
func NewTokenTracker(w io.Writer, track *RequestTracking) *TokenTracker {
	t := &TokenTracker{
		writer: w,
		track:  track,
	}
	t.scanner = bufio.NewScanner(&t.buf)
	t.scanner.Buffer(make([]byte, 64*1024), 64*1024)
	return t
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
// 跨 chunk 处理：上一个 chunk 末尾的不完整行会被保留在 t.buf 中，
// 与当前 chunk 拼接后再扫描，解决 TCP 分包切断 SSE 事件的问题。
func (t *TokenTracker) scanForUsage(chunk []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 将当前 chunk 追加到跨 chunk 缓冲区。
	t.buf.Write(chunk)

	var currentEvent string
	var dataLines []string

	for t.scanner.Scan() {
		line := t.scanner.Text()

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

	// scanner.Scan() 失败时，缓冲区中剩余的数据是不完整的行
	// → 保留在 t.buf 中等待下一个 chunk。
	// 重置 buffer：保留未处理完的数据 + 清空已消费的数据。
	// 注意：bufio.Scanner 在 Scan() 返回 false 后，buffer 中
	// 保留的是未完成的行，需要保留这些数据。
	if t.scanner.Err() != nil {
		// Scanner buffer 太小或其他错误 → 清空缓冲区避免数据错位。
		t.buf.Reset()
	} else {
		// 正常情况：scanner 消费了所有完整行。
		// 将 buffer 中的数据替换为剩余未扫描的数据。
		// 由于 bufio.Scanner 已经读走了完整行，buf 中剩余的是不完整行。
		remaining := t.buf.Bytes()
		if len(remaining) > 0 {
			newBuf := bytes.NewBuffer(make([]byte, 0, len(remaining)+4096))
			newBuf.Write(remaining)
			t.buf = *newBuf
			t.scanner = bufio.NewScanner(&t.buf)
			t.scanner.Buffer(make([]byte, 64*1024), 64*1024)
		}
	}

	// 处理可能在 data 后面没有空行的情况。
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
//
// 兼容两种字段名：
//   - Anthropic: input_tokens / output_tokens
//   - OpenAI:    prompt_tokens / completion_tokens / total_tokens
func (t *TokenTracker) extractUsage(payload map[string]interface{}) {
	raw, ok := payload["usage"]
	if !ok {
		return
	}
	usage, ok := raw.(map[string]interface{})
	if !ok {
		return
	}

	// 输入 Token：优先 Anthropic 的 input_tokens，回退到 OpenAI 的 prompt_tokens
	inputTokens := 0
	if v, ok := usage["input_tokens"].(float64); ok {
		inputTokens = int(v)
	} else if v, ok := usage["prompt_tokens"].(float64); ok {
		inputTokens = int(v)
	}
	t.track.Usage.InputTokens = max(t.track.Usage.InputTokens, inputTokens)

	// 输出 Token：优先 Anthropic 的 output_tokens，回退到 OpenAI 的 completion_tokens
	outputTokens := 0
	if v, ok := usage["output_tokens"].(float64); ok {
		outputTokens = int(v)
	} else if v, ok := usage["completion_tokens"].(float64); ok {
		outputTokens = int(v)
	}
	t.track.Usage.OutputTokens = max(t.track.Usage.OutputTokens, outputTokens)

	// 缓存相关 Token（仅 Anthropic 支持）
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		t.track.Usage.CacheReadTokens = max(t.track.Usage.CacheReadTokens, int(v))
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		t.track.Usage.CacheCreationTokens = max(t.track.Usage.CacheCreationTokens, int(v))
	}

	// OpenAI 的 total_tokens（如果直接提供了）
	if t.track.Usage.TotalTokens == 0 {
		if v, ok := usage["total_tokens"].(float64); ok {
			t.track.Usage.TotalTokens = int(v)
		}
	}

	// 如果 total_tokens 未直接提供，用 input+output 计算
	if t.track.Usage.TotalTokens == 0 {
		t.track.Usage.TotalTokens = t.track.Usage.InputTokens + t.track.Usage.OutputTokens
	}
}
