// Package middleware 提供代理请求管线内置中间件。
// 当前包含一个 demo 中间件（PromptAnalyzer），
// 展示如何通过管线扩展代理功能。
package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/ragent/router/internal/proxy"
)

// PromptAnalyzer 自动分析用户 prompt 的特征，注入元数据。
//
// 分析维度：
//   - prompt 长度、行数
//   - 是否包含代码块（``` 标记）
//   - 是否包含中文字符
//   - 复杂度评级（simple/medium/complex，基于长度和内容）
//
// 分析结果通过 SSE 事件 event: prompt_analysis 注入到流中。
type PromptAnalyzer struct{}

// Name 实现 Middleware 接口。
func (a *PromptAnalyzer) Name() string { return "PromptAnalyzer" }

// ProcessRequest 分析 prompt 并注入元数据到请求体。
func (a *PromptAnalyzer) ProcessRequest(
	_ context.Context,
	body map[string]interface{},
	headers http.Header,
) (map[string]interface{}, error) {
	prompt := extractPrompt(body)
	if prompt == "" {
		return body, nil
	}

	analysis := analyzePrompt(prompt)

	// 将分析结果注入到 system prompt（如果存在）
	if sys, ok := body["system"].(string); ok {
		body["system"] = fmt.Sprintf("%s\n\n[Prompt 特征: 长度=%d, 行数=%d, 包含代码=%v, 包含中文=%v, 复杂度=%s]",
			sys, analysis.charCount, analysis.lineCount, analysis.hasCode, analysis.hasChinese, analysis.complexity)
	} else {
		body["system"] = fmt.Sprintf("[Prompt 特征: 长度=%d, 行数=%d, 包含代码=%v, 包含中文=%v, 复杂度=%s]",
			analysis.charCount, analysis.lineCount, analysis.hasCode, analysis.hasChinese, analysis.complexity)
	}

	log.Printf("[PromptAnalyzer] 复杂度=%s 长度=%d 代码=%v 中文=%v",
		analysis.complexity, analysis.charCount, analysis.hasCode, analysis.hasChinese)

	return body, nil
}

// ProcessResponse 在响应流中注入 prompt 分析事件。
func (a *PromptAnalyzer) ProcessResponse(
	_ context.Context,
	w http.ResponseWriter,
	tracking *proxy.RequestTracking,
) error {
	// 分析结果无须注入响应流（已在系统 prompt 中体现）
	// 如需注入，可在此处 fmt.Fprintf(w, "event: prompt_analysis...")
	return nil
}

// ── 内部分析 ──────────────────────────────────────────────────────────

type promptAnalysis struct {
	charCount  int
	lineCount  int
	hasCode    bool
	hasChinese bool
	complexity string // "simple" | "medium" | "complex"
}

func analyzePrompt(prompt string) promptAnalysis {
	a := promptAnalysis{
		charCount:  utf8.RuneCountInString(prompt),
		lineCount:  strings.Count(prompt, "\n") + 1,
		hasCode:    strings.Contains(prompt, "```") || strings.Contains(prompt, "func ") || strings.Contains(prompt, "def "),
		hasChinese: containsCJK(prompt),
	}

	// 复杂度评级
	switch {
	case a.charCount < 100 && a.lineCount < 5 && !a.hasCode:
		a.complexity = "simple"
	case a.charCount > 1000 || a.lineCount > 20 || a.hasCode:
		a.complexity = "complex"
	default:
		a.complexity = "medium"
	}

	return a
}

// containsCJK 检测是否包含中日韩文字。
func containsCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified
			(r >= 0x3400 && r <= 0x4DBF) || // CJK Ext-A
			(r >= 0x3000 && r <= 0x303F) { // CJK Symbols
			return true
		}
	}
	return false
}

// extractPrompt 从 Anthropic 格式请求体中提取用户提示词。
func extractPrompt(body map[string]interface{}) string {
	messages, ok := body["messages"].([]interface{})
	if !ok {
		return ""
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
					if text, ok := b["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
			return strings.Join(parts, " ")
		}
	}
	return ""
}
