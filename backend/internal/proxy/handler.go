package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ragent/router/internal/resilience/bulkhead"
	"github.com/ragent/router/internal/resilience/circuitbreaker"
	"github.com/ragent/router/internal/resilience/ratelimit"
	"github.com/ragent/router/internal/resilience/retry"
	"github.com/ragent/router/internal/resilience/timeout"
)

// ────────────────────────────────────────────────────────────
// Proxy 核心结构
// ────────────────────────────────────────────────────────────

// Proxy 是 AI API 网关的主处理器。
//
// 职责：
//   1. 接收 Anthropic 兼容的 POST /v1/messages 请求
//   2. 解析请求体中的用户提示词，通过路由引擎选择目标供应商
//   3. 在韧性引擎的保护下（限流→熔断→重试→舱壁→超时），
//      将请求转发到上游并以 SSE 流式返回
//   4. 在流式传输过程中实时解析 Token 用量
//   5. 请求完成后记录日志（Token 用量、费用、延迟、状态）
//
// 韧性引擎的执行顺序：
//
//	ServeHTTP ─→ 全局限流 ─→ 舱壁 ─→ 单供应商熔断 ─→ 重试 ─→ 超时 ─→ HTTP 转发
//
// 这个顺序是精心设计的：
//   - 限流最先：避免无意义的请求进入后续流程
//   - 舱壁其次：防止慢请求堆积
//   - 熔断针对单供应商：一个供应商挂了不影响其他
//   - 重试在熔断之后：熔断打开时直接拒绝，不会浪费时间重试
type Proxy struct {
	// 供应商注册表：name → config
	providers map[string]*ProviderConfig

	// 每个供应商一个独立的熔断器——故障隔离
	breakers map[string]*circuitbreaker.CircuitBreaker

	// 全局限流器——所有供应商共享
	limiter *ratelimit.TokenBucket

	// 舱壁——限制总并发数
	bulkhead *bulkhead.Bulkhead

	// 路由引擎——根据提示词选择供应商
	matcher RouteMatcher

	// 协议适配器工厂
	adapters *AdapterFactory

	// HTTP 客户端（连接池复用）
	client *http.Client

	// 请求日志回调——每次请求完成后调用
	OnRequestLog func(log RequestLog)
}

// RouteMatcher 是路由选择的接口。
// 根据用户提示词和请求模型名，决定使用哪个供应商。
// ctx 用于 Embedding API 调用和 LLM 分类器的超时控制。
type RouteMatcher interface {
	Match(ctx context.Context, prompt string, model string) *ProviderConfig
}

// RequestLog 是一次代理请求完成后的日志记录。
type RequestLog struct {
	RequestID        string
	Provider         string
	Model            string
	RouteReason      string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
	LatencyMs        int64
	Status           string // "ok" 或 "error"
	ErrorDetail      string
	UpstreamID       string
	Timestamp        time.Time
}

// ────────────────────────────────────────────────────────────
// 配置与构造函数
// ────────────────────────────────────────────────────────────

// Config 是创建 Proxy 的配置参数。
type Config struct {
	Providers             []ProviderConfig // 供应商列表
	Matcher               RouteMatcher     // 路由引擎
	HTTPClient            *http.Client     // HTTP 客户端（nil 则使用默认值）
	GlobalRateLimit       float64          // 全局限流速率（Token/秒）
	MaxConcurrentRequests int              // 最大并发请求数（舱壁容量）
}

// NewProxy 创建一个新的 AI API 网关实例。
//
// 对每个已启用的供应商，自动创建一个独立的熔断器。
// HTTPClient 为 nil 时使用合理的默认值：
//   - 超时 300s（适配长文本生成）
//   - 100 个空闲连接，每 host 20 个
//   - 90s 空闲连接超时
func NewProxy(cfg Config) *Proxy {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 300 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}

	p := &Proxy{
		providers: make(map[string]*ProviderConfig),
		breakers:  make(map[string]*circuitbreaker.CircuitBreaker),
		limiter:   ratelimit.NewTokenBucket(cfg.GlobalRateLimit, uint64(cfg.GlobalRateLimit)),
		bulkhead:  bulkhead.New(cfg.MaxConcurrentRequests),
		matcher:   cfg.Matcher,
		adapters:  NewAdapterFactory(),
		client:    cfg.HTTPClient,
	}

	// 注册供应商，为每个创建独立的熔断器。
	for i := range cfg.Providers {
		prov := &cfg.Providers[i]
		if prov.Enabled {
			p.providers[prov.Name] = prov
			cbCfg := circuitbreaker.DefaultConfig()
			cbCfg.FailureThreshold = 0.5
			cbCfg.OpenTimeout = 30 * time.Second
			p.breakers[prov.Name] = circuitbreaker.New(cbCfg)
		}
	}

	return p
}

// ────────────────────────────────────────────────────────────
// HTTP Handler
// ────────────────────────────────────────────────────────────

// ServeHTTP 实现 http.Handler 接口。
//
// 仅接受 POST 方法。执行流程：
//
//	1. 全局限流检查
//	2. 舱壁并发检查
//	3. 解析请求体 + 路由 + 韧性保护 + 流式转发
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// ═══ 韧性层 1：全局限流 ═══
	if !p.limiter.Allow() {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// ═══ 韧性层 2：舱壁隔离 ═══
	reqCtx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()

	err := p.bulkhead.Execute(reqCtx, func() error {
		return p.handleRequest(w, r)
	})

	if err == bulkhead.ErrBulkheadFull {
		http.Error(w, `{"error":"server too busy"}`, http.StatusServiceUnavailable)
	}
}

// ────────────────────────────────────────────────────────────
// 请求处理
// ────────────────────────────────────────────────────────────

// handleRequest 处理单个代理请求的完整流程：
//
//	解析 → 路由 → 韧性保护（熔断+重试+超时） → 上游转发 → 日志记录
func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) error {
	startTime := time.Now()

	// ── 步骤 1：解析请求体 ──
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return nil
	}
	defer r.Body.Close()

	var bodyJSON map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &bodyJSON); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return nil
	}

	// ── 步骤 2：提取提示词 + 路由 ──
	prompt := extractPrompt(bodyJSON)
	modelName, _ := bodyJSON["model"].(string)

	provider := p.matcher.Match(r.Context(), prompt, modelName)
	if provider == nil {
		// 降级：取第一个启用的供应商
		for _, pv := range p.providers {
			provider = pv
			break
		}
	}
	if provider == nil {
		http.Error(w, `{"error":"no provider configured"}`, http.StatusBadGateway)
		return nil
	}
	log.Printf("[路由] → %s (model=%s)", provider.Name, modelName)

	// ── 步骤 3：获取或创建供应商熔断器 ──
	breaker := p.breakers[provider.Name]
	if breaker == nil {
		cbCfg := circuitbreaker.DefaultConfig()
		breaker = circuitbreaker.New(cbCfg)
		p.breakers[provider.Name] = breaker
	}

	// ── 步骤 4：初始化追踪 ──
	tracking := &RequestTracking{
		RequestID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	// ── 步骤 5：韧性保护 + 上游转发 ──
	// 执行顺序：熔断器.Call → 重试.Do → 实际请求
	// 注意：重试次数设为 2（共 3 次尝试：1 初始 + 2 重试）
	retryCfg := retry.DefaultConfig()
	retryCfg.MaxAttempts = 2
	backoff := retry.NewExponentialBackoff(retryCfg)

	var proxyErr error
	err = breaker.Call(func() error {
		return retry.Do(r.Context(), backoff, backoff.MaxAttempts(), func() error {
			return p.doUpstreamRequest(w, r, provider, bodyJSON, tracking)
		})
	})

	// ── 步骤 6：记录日志 ──
	latency := time.Since(startTime).Milliseconds()
	status := "ok"
	errorDetail := ""
	if proxyErr != nil || err != nil {
		status = "error"
		if proxyErr != nil {
			errorDetail = proxyErr.Error()
		} else if err != nil {
			errorDetail = err.Error()
		}
	}

	if p.OnRequestLog != nil {
		p.OnRequestLog(RequestLog{
			RequestID:        tracking.RequestID,
			Provider:         provider.Name,
			Model:            modelName,
			RouteReason:      fmt.Sprintf("prompt_len=%d", len(prompt)),
			PromptTokens:     tracking.Usage.InputTokens,
			CompletionTokens: tracking.Usage.OutputTokens,
			TotalTokens:      tracking.Usage.TotalTokens,
			CostUSD:          estimateCost(provider.Name, modelName, tracking.Usage.InputTokens, tracking.Usage.OutputTokens),
			LatencyMs:        latency,
			Status:           status,
			ErrorDetail:      errorDetail,
			UpstreamID:       tracking.UpstreamID,
			Timestamp:        startTime,
		})
	}

	return nil
}

// ────────────────────────────────────────────────────────────
// 上游请求
// ────────────────────────────────────────────────────────────

// doUpstreamRequest 向供应商发送 HTTP 请求并以 SSE 流式传回。
//
// 流程：
//  1. 通过适配器构建供应商原生格式的请求
//  2. 创建 HTTP 请求（带级联超时）
//  3. 发送请求，检查响应状态
//  4. 设置 SSE 响应头
//  5. 通过 TokenTracker 流式复制响应（同时提取 Token 用量）
func (p *Proxy) doUpstreamRequest(
	w http.ResponseWriter,
	r *http.Request,
	provider *ProviderConfig,
	body map[string]interface{},
	tracking *RequestTracking,
) error {
	// ── 构建供应商原生请求 ──
	adapter := p.adapters.Get(provider.Name)

	headers := map[string]string{
		"x-api-key":         provider.APIKey,
		"anthropic-version": "2023-06-01",
		"content-type":      "application/json",
	}
	for k, v := range provider.Headers {
		headers[k] = v
	}

	url, reqHeaders, reqBody, err := adapter.BuildRequest(provider.BaseURL, headers, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	// ── 创建上游请求（级联超时）──
	// 总超时 120s，但如果 ctx 有更紧的 deadline 则受 ctx 约束。
	upstreamCtx, cancel := timeout.Cascading(r.Context(), 120*time.Second)
	defer cancel()

	bodyReader := bytes.NewReader(reqBody)
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range reqHeaders {
		upstreamReq.Header.Set(k, v)
	}
	upstreamReq.ContentLength = int64(len(reqBody))

	// ── 发送上游请求 ──
	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	// 上游返回错误 → 记录错误信息并返回。
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// ── 设置 SSE 响应头（告诉客户端准备接收流式数据）──
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Ragent-Provider", provider.Name)
	w.Header().Set("X-Ragent-Model", tracking.Model)
	w.WriteHeader(http.StatusOK)

	// ── 流式复制 + Token 追踪 ──
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	tracker := NewTokenTracker(w, tracking)
	_, err = io.Copy(tracker, resp.Body)
	if err != nil {
		return fmt.Errorf("stream copy: %w", err)
	}
	flusher.Flush()

	return nil
}

// ────────────────────────────────────────────────────────────
// 辅助函数
// ────────────────────────────────────────────────────────────

// extractPrompt 从 Anthropic 格式的请求体中提取最后一条用户消息。
//
// Anthropic messages 是一个数组，格式为：
//
//	[{"role": "system", "content": "..."},
//	 {"role": "user", "content": "Hello"},
//	 {"role": "assistant", "content": "Hi!"},
//	 {"role": "user", "content": "Explain Redis"}]   ← 提取这条
//
// content 可能是字符串或 [{"type":"text","text":"..."}] 的内容块数组。
func extractPrompt(body map[string]interface{}) string {
	messages, ok := body["messages"].([]interface{})
	if !ok {
		return ""
	}

	// 从后往前找最后一条 user 消息。
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
			// 内容块数组 → 提取所有 text 块并拼接。
			var parts []string
			for _, block := range v {
				if b, ok := block.(map[string]interface{}); ok {
					if text, ok := b["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
			return joinStrings(parts, " ")
		}
	}

	return ""
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}

// ────────────────────────────────────────────────────────────
// 成本估算
// ────────────────────────────────────────────────────────────

// estimateCost 根据供应商和模型名称估算 API 调用费用。
//
// 使用硬编码的费率表（美元/百万 Token）：
//
//	Claude:  $3.00/M input,  $15.00/M output
//	DeepSeek: $0.27/M input, $1.10/M output
//	OpenAI:   $2.50/M input, $10.00/M output
//
// 注意：实际费率会变化，生产环境应改为数据库配置或 API 查询。
func estimateCost(provider, model string, inputTokens, outputTokens int) float64 {
	type rate struct{ input, output float64 }
	rates := map[string]rate{
		"deepseek": {0.27, 1.10},
		"claude":   {3.00, 15.00},
		"openai":   {2.50, 10.00},
		"minimax":  {0.30, 1.20},
		"bailian":  {0.40, 1.20},
	}

	for keyword, r := range rates {
		if contains(provider, keyword) || contains(model, keyword) {
			cost := (float64(inputTokens)*r.input + float64(outputTokens)*r.output) / 1_000_000
			return float64(int(cost*10000)) / 10000 // 保留 4 位小数
		}
	}
	return 0.0
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────
// 供应商管理（运行时动态修改）
// ────────────────────────────────────────────────────────────

// AddProvider 运行时注册新供应商，同时创建对应的熔断器。
func (p *Proxy) AddProvider(cfg ProviderConfig) {
	cfg.Enabled = true
	p.providers[cfg.Name] = &cfg
	cbCfg := circuitbreaker.DefaultConfig()
	p.breakers[cfg.Name] = circuitbreaker.New(cbCfg)
}

// RemoveProvider 移除供应商及其熔断器。
func (p *Proxy) RemoveProvider(name string) {
	delete(p.providers, name)
	delete(p.breakers, name)
}

// GetProvider 按名称获取供应商配置。
func (p *Proxy) GetProvider(name string) *ProviderConfig {
	return p.providers[name]
}

// ListProviders 返回所有已注册的供应商列表。
func (p *Proxy) ListProviders() []*ProviderConfig {
	result := make([]*ProviderConfig, 0, len(p.providers))
	for _, pv := range p.providers {
		result = append(result, pv)
	}
	return result
}

// BreakerStats 返回指定供应商的熔断器状态。
func (p *Proxy) BreakerStats(name string) *circuitbreaker.Stats {
	if cb, ok := p.breakers[name]; ok {
		s := cb.Stats()
		return &s
	}
	return nil
}
