// Package mock 提供 Mock 模式的全部基础设施——
// 无需真实 API Key 即可启动完整 Demo，展示所有功能。
//
// Mock 上游服务器模拟 AI API 的 SSE 流式响应，
// 返回确定性的、可预测的数据，方便面试 Demo 展示。
package mock

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// StartUpstreamServer 启动 mock AI API 服务器，返回其地址。
// 支持 Anthropic Messages API 和 OpenAI Chat Completions API 两种格式。
func StartUpstreamServer() (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(fmt.Sprintf("mock upstream: %v", err))
	}

	var requestCount int64
	addr := fmt.Sprintf("http://%s", listener.Addr().String())

	mux := http.NewServeMux()

	// ── Anthropic Messages API 兼容端点 ──
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		prompt := extractPrompt(body)
		model, _ := body["model"].(string)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		// 模拟错误：每 8 次请求有 1 次返回 500（展示熔断器）
		if count%8 == 0 {
			fmt.Fprintf(w, `event: error\ndata: {"type":"error","error":{"type":"overloaded","message":"Mock 服务器过载模拟"}}\n\n`)
			return
		}

		msgID := fmt.Sprintf("msg_mock_%04d", count)
		respText := generateResponse(prompt, model)

		// message_start 事件
		startEvent := map[string]interface{}{
			"message": map[string]interface{}{
				"id":    msgID,
				"model": model,
				"usage": map[string]interface{}{
					"input_tokens": len(prompt) / 4,
				},
			},
		}
		startJSON, _ := json.Marshal(startEvent)
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", startJSON)

		// content_block_start
		fmt.Fprintf(w, `event: content_block_start\ndata: {"index":0,"content_block":{"type":"text","text":""}}\n\n`)

		// 分块输出文本
		words := strings.Fields(respText)
		chunkSize := 5
		for i := 0; i < len(words); i += chunkSize {
			end := i + chunkSize
			if end > len(words) {
				end = len(words)
			}
			chunk := strings.Join(words[i:end], " ") + " "
			deltaEvent := map[string]interface{}{
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": chunk,
				},
			}
			deltaJSON, _ := json.Marshal(deltaEvent)
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", deltaJSON)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(30 * time.Millisecond) // 模拟流式延迟
		}

		// content_block_stop + message_delta
		fmt.Fprintf(w, `event: content_block_stop\ndata: {"index":0}\n\n`)
		deltaEvent := map[string]interface{}{
			"delta": map[string]interface{}{"stop_reason": "end_turn"},
			"usage": map[string]interface{}{
				"output_tokens": len(respText) / 4,
			},
		}
		deltaJSON, _ := json.Marshal(deltaEvent)
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", deltaJSON)
		fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")
	})

	// ── OpenAI Chat Completions 兼容端点 ──
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		prompt := extractPrompt(body)
		model, _ := body["model"].(string)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		respText := generateResponse(prompt, model)

		words := strings.Fields(respText)
		chunkSize := 3
		for i := 0; i < len(words); i += chunkSize {
			end := i + chunkSize
			if end > len(words) {
				end = len(words)
			}
			chunk := strings.Join(words[i:end], " ") + " "
			chunkData := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-mock-%04d", count),
				"object":  "chat.completion.chunk",
				"model":   model,
				"choices": []map[string]interface{}{{"delta": map[string]interface{}{"content": chunk}}},
			}
			chunkJSON, _ := json.Marshal(chunkData)
			fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}

		// 最后一个 chunk 带 usage
		finalData := map[string]interface{}{
			"id":      fmt.Sprintf("chatcmpl-mock-%04d", count),
			"object":  "chat.completion.chunk",
			"model":   model,
			"usage": map[string]interface{}{
				"prompt_tokens":     len(prompt) / 4,
				"completion_tokens": len(respText) / 4,
				"total_tokens":      (len(prompt) + len(respText)) / 4,
			},
		}
		finalJSON, _ := json.Marshal(finalData)
		fmt.Fprintf(w, "data: %s\n\n", finalJSON)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)

	return addr, func() { srv.Close() }
}

// ── 响应生成 ──────────────────────────────────────────────────────────

func generateResponse(prompt, model string) string {
	lower := strings.ToLower(prompt)

	// 根据 prompt 内容生成确定性响应
	switch {
	case strings.Contains(lower, "架构") || strings.Contains(lower, "architecture"):
		return "基于你的需求，我建议采用分层架构：表示层使用 React，业务逻辑层使用 Go 微服务，数据层使用 PostgreSQL。关键设计点包括：1) API Gateway 统一入口 2) gRPC 服务间通信 3) Redis 缓存热点数据 4) Kafka 异步消息解耦。"
	case strings.Contains(lower, "bug") || strings.Contains(lower, "debug") || strings.Contains(lower, "error"):
		return "我分析了你的代码，发现问题在第 42 行：变量未初始化就使用了。修复方法：在函数开头添加 `var result []string` 初始化空切片。另外建议添加空值检查以提高健壮性。"
	case strings.Contains(lower, "代码") || strings.Contains(lower, "code") || strings.Contains(lower, "写"):
		return "以下是实现代码：\n\n```go\nfunc ProcessData(items []string) ([]string, error) {\n    if len(items) == 0 {\n        return nil, fmt.Errorf(\"items is empty\")\n    }\n    result := make([]string, 0, len(items))\n    for _, item := range items {\n        result = append(result, strings.TrimSpace(item))\n    }\n    return result, nil\n}\n```\n\n这个实现包含了输入校验、预分配容量和错误处理。"
	case strings.Contains(lower, "hello") || strings.Contains(lower, "你好"):
		return "你好！我是 " + model + " 模型（Mock 模式）。我可以帮你解答编程问题、架构设计、代码审查等各种开发相关的问题。有什么我可以帮你的？"
	default:
		return "这是一个很好的问题。基于 " + model + " 模型的分析，我给出以下建议：首先理解问题的核心，然后分步骤解决，最后验证结果。在实现过程中要注意边界条件和错误处理。如果你需要更具体的代码示例，请告诉我更多细节。"
	}
}

func extractPrompt(body map[string]interface{}) string {
	if sys, ok := body["system"].(string); ok && sys != "" {
		return sys
	}
	messages, ok := body["messages"].([]interface{})
	if !ok {
		return "hello"
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		content := msg["content"]
		switch v := content.(type) {
		case string:
			return v
		case []interface{}:
			var parts []string
			for _, block := range v {
				if b, ok := block.(map[string]interface{}); ok {
					if t, ok := b["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
			return strings.Join(parts, " ")
		}
	}
	return "hello"
}
