package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

// TokenUsage holds extracted token consumption data from an SSE stream.
type TokenUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	CacheReadTokens    int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	TotalTokens        int `json:"total_tokens"`
}

// RequestTracking holds metadata collected during a streaming request.
type RequestTracking struct {
	RequestID       string     `json:"request_id"`
	UpstreamID      string     `json:"upstream_request_id"`
	Model           string     `json:"model"`
	Usage           TokenUsage `json:"usage"`
	ContentLength   int64      `json:"content_length"`
}

// TokenTracker parses SSE stream chunks to extract token usage information
// from Anthropic Messages API responses.
//
// In the Anthropic streaming protocol:
//   - message_start contains the message id
//   - message_delta contains usage (input_tokens, output_tokens)
//   - message_stop marks the end of a message
//
// The tracker wraps an io.Writer and intercepts SSE data events,
// forwarding all bytes to the underlying writer while extracting metrics.
type TokenTracker struct {
	writer io.Writer
	track  *RequestTracking
	mu     sync.Mutex
	buf    bytes.Buffer
}

// NewTokenTracker creates a tracker that writes to w and populates track.
func NewTokenTracker(w io.Writer, track *RequestTracking) *TokenTracker {
	return &TokenTracker{
		writer: w,
		track:  track,
	}
}

// Write implements io.Writer. It passes all bytes through to the
// underlying writer while scanning for SSE data events.
func (t *TokenTracker) Write(p []byte) (int, error) {
	// Forward to the underlying writer first.
	n, err := t.writer.Write(p)
	if err != nil {
		return n, err
	}

	// Scan for SSE data events and extract usage.
	t.scanForUsage(p)
	return n, nil
}

// scanForUsage parses SSE chunks for usage and message metadata.
func (t *TokenTracker) scanForUsage(chunk []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	scanner := bufio.NewScanner(bytes.NewReader(chunk))
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	var currentEvent string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if line == "" && len(dataLines) > 0 {
			// End of SSE event—process the accumulated data.
			t.processEvent(currentEvent, strings.Join(dataLines, ""))
			currentEvent = ""
			dataLines = nil
		}
	}

	// Flush remaining data.
	if len(dataLines) > 0 {
		t.processEvent(currentEvent, strings.Join(dataLines, ""))
	}
}

// processEvent extracts usage from an SSE event.
func (t *TokenTracker) processEvent(eventType string, data string) {
	if data == "" || data == "[DONE]" {
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return
	}

	switch eventType {
	case "message_start":
		if msg, ok := payload["message"].(map[string]interface{}); ok {
			if id, ok := msg["id"].(string); ok && t.track.UpstreamID == "" {
				t.track.UpstreamID = id
			}
			if model, ok := msg["model"].(string); ok {
				t.track.Model = model
			}
			// message_start may also include usage.
			t.extractUsage(msg)
		}

	case "message_delta":
		if _, ok := payload["delta"].(map[string]interface{}); ok {
			t.track.ContentLength += int64(len(data))
		}
		t.extractUsage(payload)

	case "message_stop":
		// No action needed; usage extracted from prior events.

	case "ping":
		// No action needed.

	default:
		// Try to extract usage from any event that might contain it.
		t.extractUsage(payload)
	}
}

// extractUsage pulls token counts from an event payload.
func (t *TokenTracker) extractUsage(payload map[string]interface{}) {
	raw, ok := payload["usage"]
	if !ok {
		return
	}
	usage, ok := raw.(map[string]interface{})
	if !ok {
		return
	}

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

	t.track.Usage.TotalTokens = t.track.Usage.InputTokens + t.track.Usage.OutputTokens
}
