package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// executeReview 执行 review 编排：模型 A 生成 → 模型 B 审查。
//
// SSE 流格式：
//
//	event: phase_change
//	data: {"phase":"generation","provider":"DeepSeek"}
//	[生成阶段的 SSE chunks]
//	event: phase_change
//	data: {"phase":"review","provider":"Claude"}
//	[审查阶段的 SSE chunks]
func (e *Engine) executeReview(ctx context.Context, w http.ResponseWriter, req *Request) error {
	// ── 写 SSE 响应头 ──
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Ragent-Orchestration", "review")
	w.WriteHeader(http.StatusOK)

	// ════════════════════════════════════════════════════════════
	// 阶段 1：代码生成
	// ════════════════════════════════════════════════════════════
	writePhase(w, Phase{Name: "generation", Provider: req.Generator.Name})
	log.Printf("[编排] 阶段1 生成 → %s", req.Generator.Name)

	genUsage, err := e.caller.Call(ctx, w, req.Generator, req.Body)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"phase\":\"generation\",\"error\":%q}\n\n", err.Error())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return fmt.Errorf("generation phase: %w", err)
	}
	log.Printf("[编排] 阶段1 完成: input=%d output=%d cost=$%.4f",
		genUsage.InputTokens, genUsage.OutputTokens, genUsage.CostUSD)

	// ════════════════════════════════════════════════════════════
	// 阶段 2：代码审查
	// ════════════════════════════════════════════════════════════
	writePhase(w, Phase{Name: "review", Provider: req.Reviewer.Name})
	log.Printf("[编排] 阶段2 审查 → %s", req.Reviewer.Name)

	// 构建审查请求：把生成的代码嵌入审查 prompt。
	reviewBody, err := e.buildReviewBody(req.Body, req.Reviewer.Model)
	if err != nil {
		return fmt.Errorf("build review request: %w", err)
	}

	reviewUsage, err := e.caller.Call(ctx, w, req.Reviewer, reviewBody)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"phase\":\"review\",\"error\":%q}\n\n", err.Error())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return fmt.Errorf("review phase: %w", err)
	}
	log.Printf("[编排] 阶段2 完成: input=%d output=%d cost=$%.4f",
		reviewUsage.InputTokens, reviewUsage.OutputTokens, reviewUsage.CostUSD)

	// ── 汇总统计 ──
	totalCost := genUsage.CostUSD + reviewUsage.CostUSD
	summary := map[string]interface{}{
		"generation_cost": genUsage.CostUSD,
		"review_cost":     reviewUsage.CostUSD,
		"total_cost":      totalCost,
		"total_tokens":    genUsage.TotalTokens + reviewUsage.TotalTokens,
	}
	summaryJSON, _ := json.Marshal(summary)
	fmt.Fprintf(w, "event: orchestration_summary\ndata: %s\n\n", summaryJSON)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	log.Printf("[编排] 完成: 总费用=$%.4f", totalCost)

	return nil
}

// buildReviewBody 构建审查请求体。
// 将原始 prompt 与审查指令合并为新的 system prompt。
func (e *Engine) buildReviewBody(originalBody map[string]interface{}, reviewModel string) (map[string]interface{}, error) {
	// 提取原始 prompt
	messages, _ := originalBody["messages"].([]interface{})
	var userPrompt string
	if len(messages) > 0 {
		if last, ok := messages[len(messages)-1].(map[string]interface{}); ok {
			if content, ok := last["content"].(string); ok {
				userPrompt = content
			}
		}
	}

	reviewSystem := fmt.Sprintf(
		`你是一个代码审查专家。请审查上述代码的质量、安全性、性能和最佳实践。

审查维度（每项 1-10 分）：
1. 正确性：代码逻辑是否正确
2. 安全性：是否存在安全漏洞（SQL注入/XSS/密钥泄露等）
3. 性能：是否有性能瓶颈
4. 可维护性：代码是否易读、易维护
5. 最佳实践：是否符合语言/框架的惯用写法

对于每个问题，请给出具体的修复建议和修改后的代码。
原始用户需求：%s`, userPrompt,
	)

	return map[string]interface{}{
		"model":       reviewModel,
		"system":      reviewSystem,
		"messages":    messages,
		"max_tokens":  originalBody["max_tokens"],
		"temperature": 0.3,
		"stream":      true,
	}, nil
}
