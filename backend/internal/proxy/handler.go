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

// Proxy is the main HTTP handler for the AI API gateway.
// It routes Anthropic-compatible requests to upstream providers,
// protected by circuit breakers, rate limiters, retry, and bulkheads.
type Proxy struct {
	// Provider registry
	providers map[string]*ProviderConfig

	// Resilience per provider
	breakers   map[string]*circuitbreaker.CircuitBreaker
	limiter    *ratelimit.TokenBucket
	bulkhead   *bulkhead.Bulkhead

	// Routing
	matcher RouteMatcher

	// Adapters
	adapters *AdapterFactory

	// HTTP client for upstream calls
	client *http.Client

	// Callback for request logging
	OnRequestLog func(log RequestLog)
}

// RouteMatcher is the interface for selecting a provider based on request content.
type RouteMatcher interface {
	Match(prompt string, model string) *ProviderConfig
}

// RequestLog is emitted after each proxied request completes.
type RequestLog struct {
	RequestID       string
	Provider        string
	Model           string
	RouteReason     string
	PromptTokens    int
	CompletionTokens int
	TotalTokens     int
	CostUSD         float64
	LatencyMs       int64
	Status          string
	ErrorDetail     string
	UpstreamID      string
	Timestamp       time.Time
}

// NewProxy creates a new AI API proxy.
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

	// Register providers and create per-provider circuit breakers.
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

// Config configures the proxy.
type Config struct {
	Providers             []ProviderConfig
	Matcher               RouteMatcher
	HTTPClient            *http.Client
	GlobalRateLimit       float64 // tokens/second globally
	MaxConcurrentRequests int
}

// ServeHTTP implements http.Handler for the Anthropic Messages API endpoint.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only POST is supported.
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// ---- Resilience: global rate limit ----
	if !p.limiter.Allow() {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// ---- Resilience: bulkhead ----
	reqCtx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()

	err := p.bulkhead.Execute(reqCtx, func() error {
		return p.handleRequest(w, r)
	})

	if err == bulkhead.ErrBulkheadFull {
		http.Error(w, `{"error":"server too busy"}`, http.StatusServiceUnavailable)
	}
}

// handleRequest processes a single proxy request: parse body → route → forward → stream.
func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) error {
	startTime := time.Now()

	// Parse the request body.
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

	// Extract user prompt for routing.
	prompt := extractPrompt(bodyJSON)
	modelName, _ := bodyJSON["model"].(string)

	// Route to a provider.
	provider := p.matcher.Match(prompt, modelName)
	if provider == nil {
		// Find any enabled provider as fallback.
		for _, pv := range p.providers {
			provider = pv
			break
		}
	}
	if provider == nil {
		http.Error(w, `{"error":"no provider configured"}`, http.StatusBadGateway)
		return nil
	}
	log.Printf("[PROXY] routed → %s (model=%s)", provider.Name, modelName)

	// ---- Resilience: per-provider circuit breaker ----
	breaker := p.breakers[provider.Name]
	if breaker == nil {
		cbCfg := circuitbreaker.DefaultConfig()
		breaker = circuitbreaker.New(cbCfg)
		p.breakers[provider.Name] = breaker
	}

	// Set up tracking.
	tracking := &RequestTracking{
		RequestID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	// Execute with circuit breaker + retry.
	retryCfg := retry.DefaultConfig()
	retryCfg.MaxAttempts = 2
	backoff := retry.NewExponentialBackoff(retryCfg)

	var proxyErr error
	err = breaker.Call(func() error {
		return retry.Do(r.Context(), backoff, backoff.MaxAttempts(), func() error {
			return p.doUpstreamRequest(w, r, provider, bodyJSON, tracking)
		})
	})

	latency := time.Since(startTime).Milliseconds()

	// Log the request.
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

// doUpstreamRequest forwards the request to the upstream provider and streams
// the response back to the client in SSE format.
func (p *Proxy) doUpstreamRequest(
	w http.ResponseWriter,
	r *http.Request,
	provider *ProviderConfig,
	body map[string]interface{},
	tracking *RequestTracking,
) error {
	// Build the upstream request using the provider's adapter.
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

	// Create the upstream request with a timeout.
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

	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	// Handle upstream errors.
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// Set SSE response headers for the client.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Ragent-Provider", provider.Name)
	w.Header().Set("X-Ragent-Model", tracking.Model)
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	// Stream the response, tracking token usage.
	tracker := NewTokenTracker(w, tracking)
	_, err = io.Copy(tracker, resp.Body)
	if err != nil {
		return fmt.Errorf("stream copy: %w", err)
	}
	flusher.Flush()

	return nil
}

// extractPrompt pulls the last user message from an Anthropic-compatible request body.
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

// estimateCost computes a rough cost estimate based on provider rates.
func estimateCost(provider, model string, inputTokens, outputTokens int) float64 {
	// Approximate rates per million tokens (USD).
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
			return float64(int(cost*10000)) / 10000 // round to 4 decimal places
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

// AddProvider registers a new provider at runtime.
func (p *Proxy) AddProvider(cfg ProviderConfig) {
	cfg.Enabled = true
	p.providers[cfg.Name] = &cfg
	cbCfg := circuitbreaker.DefaultConfig()
	p.breakers[cfg.Name] = circuitbreaker.New(cbCfg)
}

// RemoveProvider removes a provider by name.
func (p *Proxy) RemoveProvider(name string) {
	delete(p.providers, name)
	delete(p.breakers, name)
}

// GetProvider returns a provider by name.
func (p *Proxy) GetProvider(name string) *ProviderConfig {
	return p.providers[name]
}

// ListProviders returns all registered providers.
func (p *Proxy) ListProviders() []*ProviderConfig {
	result := make([]*ProviderConfig, 0, len(p.providers))
	for _, pv := range p.providers {
		result = append(result, pv)
	}
	return result
}

// BreakerStats returns circuit breaker stats for a provider.
func (p *Proxy) BreakerStats(name string) *circuitbreaker.Stats {
	if cb, ok := p.breakers[name]; ok {
		s := cb.Stats()
		return &s
	}
	return nil
}
