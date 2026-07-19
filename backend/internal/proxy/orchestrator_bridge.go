package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ragent/router/internal/orchestrator"
	"github.com/ragent/router/internal/resilience/retry"
	"github.com/ragent/router/internal/resilience/timeout"
)

// NewOrchestratorCaller 创建编排引擎的上游调用器。
// 将 proxy 的内部能力（适配器、HTTP 客户端、韧性组件）
// 暴露给编排引擎，同时保持包间解耦。
func (p *Proxy) NewOrchestratorCaller() orchestrator.UpstreamCaller {
	return &orchestratorCaller{proxy: p}
}

type orchestratorCaller struct {
	proxy *Proxy
}

// Call 调用上游 API 并以 SSE 流式写回。
// 实现了 orchestrator.UpstreamCaller 接口。
func (c *orchestratorCaller) Call(
	ctx context.Context,
	w http.ResponseWriter,
	prov *orchestrator.Provider,
	body map[string]interface{},
) (*orchestrator.Usage, error) {
	// ── 获取适配器 ──
	adapter := c.proxy.adapters.Get(prov.Name)

	// ── 构建请求头 ──
	headers := map[string]string{
		"x-api-key":         prov.APIKey,
		"anthropic-version": "2023-06-01",
		"content-type":      "application/json",
	}
	for k, v := range prov.Headers {
		headers[k] = v
	}

	url, reqHeaders, reqBody, err := adapter.BuildRequest(prov.BaseURL, headers, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	// ── 发送请求（带重试）──
	var resp *http.Response
	retryCfg := retry.DefaultConfig()
	retryCfg.MaxAttempts = 2
	backoff := retry.NewExponentialBackoff(retryCfg)

	err = retry.Do(ctx, backoff, backoff.MaxAttempts(), func() error {
		upstreamCtx, cancel := timeout.Cascading(ctx, c.proxy.resilience.UpstreamTimeout)
		defer cancel()

		bodyReader := bytes.NewReader(reqBody)
		upstreamReq, reqErr := http.NewRequestWithContext(upstreamCtx, http.MethodPost, url, bodyReader)
		if reqErr != nil {
			return fmt.Errorf("create request: %w", reqErr)
		}
		for k, v := range reqHeaders {
			upstreamReq.Header.Set(k, v)
		}
		upstreamReq.ContentLength = int64(len(reqBody))

		resp2, doErr := c.proxy.client.Do(upstreamReq)
		if doErr != nil {
			return fmt.Errorf("upstream request: %w", doErr)
		}
		if resp2.StatusCode >= 500 {
			errBody, _ := io.ReadAll(io.LimitReader(resp2.Body, 4096))
			resp2.Body.Close()
			return fmt.Errorf("upstream HTTP %d: %s", resp2.StatusCode, string(errBody))
		}
		resp = resp2
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// ── 流式复制 + Token 追踪 ──
	tracking := &RequestTracking{}
	tracker := NewTokenTracker(w, tracking)
	_, err = io.Copy(tracker, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("stream copy: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// ── 计算费用 ──
	model, _ := body["model"].(string)
	cost := estimateCost(prov.Name, model, tracking.Usage.InputTokens, tracking.Usage.OutputTokens)

	return &orchestrator.Usage{
		InputTokens:  tracking.Usage.InputTokens,
		OutputTokens: tracking.Usage.OutputTokens,
		TotalTokens:  tracking.Usage.TotalTokens,
		CostUSD:      cost,
	}, nil
}

// OrchestratorAdapter 将 orchestrator.Engine 适配为 proxy.Orchestrator 接口。
// 解决包间依赖：proxy 不知道 orchestrator.Engine 的具体类型。
type OrchestratorAdapter struct {
	engine interface {
		Execute(ctx context.Context, w http.ResponseWriter, req *orchestrator.Request) error
	}
	proxy *Proxy // 用于解析供应商名 → Provider 信息
}

// NewOrchestratorAdapter 创建编排适配器。
func NewOrchestratorAdapter(engine interface {
	Execute(ctx context.Context, w http.ResponseWriter, req *orchestrator.Request) error
}, p *Proxy) *OrchestratorAdapter {
	return &OrchestratorAdapter{engine: engine, proxy: p}
}

// Execute 实现 proxy.Orchestrator 接口。
func (a *OrchestratorAdapter) Execute(ctx context.Context, w http.ResponseWriter, req *OrchestrateRequest) error {
	genProv := a.lookupProvider(req.GeneratorName)
	revProv := a.lookupProvider(req.ReviewerName)
	if genProv == nil || revProv == nil {
		return fmt.Errorf("provider not found: gen=%s rev=%s", req.GeneratorName, req.ReviewerName)
	}

	orchReq := &orchestrator.Request{
		Generator: genProv,
		Reviewer:  revProv,
		Body:      req.Body,
		Strategy:  orchestrator.StrategyReview,
	}
	return a.engine.Execute(ctx, w, orchReq)
}

func (a *OrchestratorAdapter) lookupProvider(name string) *orchestrator.Provider {
	for _, pv := range a.proxy.ListProviders() {
		if strings.EqualFold(pv.Name, name) {
			return &orchestrator.Provider{
				Name:    pv.Name,
				BaseURL: pv.BaseURL,
				APIKey:  pv.APIKey,
				Model:   pv.Model,
				Headers: pv.Headers,
			}
		}
	}
	// 回退：大小写不敏感子串匹配
	nameLower := strings.ToLower(name)
	for _, pv := range a.proxy.ListProviders() {
		if strings.Contains(strings.ToLower(pv.Name), nameLower) {
			return &orchestrator.Provider{
				Name:    pv.Name,
				BaseURL: pv.BaseURL,
				APIKey:  pv.APIKey,
				Model:   pv.Model,
				Headers: pv.Headers,
			}
		}
	}
	return nil
}

// extractPrompt 从请求体中提取用户提示词（用于日志）。
func extractPromptFromBody(body map[string]interface{}) string {
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
