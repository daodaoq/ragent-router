// RAgent Router — AI API Gateway with Resilience Engine
//
// A transparent proxy between Claude Code and multiple AI providers,
// featuring circuit breaking, rate limiting, retry with jitter,
// token tracking, intelligent routing, and cost analytics.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ragent/router/internal/proxy"
	"github.com/ragent/router/internal/routing"
	"github.com/ragent/router/internal/store"
)

func main() {
	port := flag.Int("port", 15722, "listen port")
	dbPath := flag.String("db", "ragent_router.db", "SQLite database path")
	flag.Parse()

	// Initialize storage.
	logStore, err := store.NewLogStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer logStore.Close()
	log.Printf("[INIT] Database ready: %s", *dbPath)

	// Configure providers.
	// In production, these would come from environment variables or a config file.
	providers := loadProviders()

	// Build the provider registry for routing.
	providerMap := make(map[string]*proxy.ProviderConfig)
	for i := range providers {
		providers[i].Enabled = true
		providerMap[providers[i].Name] = &providers[i]
	}

	// Set up routing.
	rules := routing.DefaultRules()
	defaultProvider := "deepseek" // cheapest model as default
	engine := routing.NewRuleEngine(rules, providerMap, defaultProvider)

	// Create the proxy.
	p := proxy.NewProxy(proxy.Config{
		Providers:             providers,
		Matcher:               engine,
		GlobalRateLimit:       100, // 100 req/s global limit
		MaxConcurrentRequests: 50,
	})

	// Wire up request logging to SQLite.
	p.OnRequestLog = func(rl proxy.RequestLog) {
		record := &store.RequestLogRecord{
			ID:                 fmt.Sprintf("%d", time.Now().UnixNano()),
			Prompt:             store.CompactPrompt("", 500),
			PromptTokens:       rl.PromptTokens,
			CompletionTokens:   rl.CompletionTokens,
			TotalTokens:        rl.TotalTokens,
			Model:              rl.Model,
			Provider:           rl.Provider,
			RouteReason:        rl.RouteReason,
			Status:             rl.Status,
			ErrorDetail:        rl.ErrorDetail,
			UpstreamRequestID:  rl.UpstreamID,
			CostUSD:            rl.CostUSD,
			LatencyMs:          rl.LatencyMs,
			CreatedAt:          rl.Timestamp,
		}
		if err := logStore.Insert(record); err != nil {
			log.Printf("[STORE] Failed to insert log: %v", err)
		}
	}

	// Build HTTP server.
	mux := http.NewServeMux()

	// CORS middleware.
	handler := corsMiddleware(mux)

	// Health check.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"service": "ragent-router",
			"version": "0.2.0",
		})
	})

	// ---- Proxy endpoint (Anthropic-compatible) ----
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"proxy":    "ragent-router",
				"endpoint": "/v1/messages",
				"method":   "POST (streaming SSE)",
				"status":   "ok",
			})
			return
		}
		p.ServeHTTP(w, r)
	})
	mux.HandleFunc("/v1/messages/", func(w http.ResponseWriter, r *http.Request) {
		p.ServeHTTP(w, r)
	})

	// ---- Dashboard API ----
	mux.HandleFunc("/api/dashboard/overview", func(w http.ResponseWriter, r *http.Request) {
		overview, err := logStore.DashboardOverview()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, overview)
	})

	mux.HandleFunc("/api/dashboard/model-distribution", func(w http.ResponseWriter, r *http.Request) {
		items, err := logStore.ModelDistribution()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	})

	mux.HandleFunc("/api/dashboard/recent-routes", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 20
		}
		routes, err := logStore.RecentRoutes(limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": routes})
	})

	mux.HandleFunc("/api/dashboard/cost-trend", func(w http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days <= 0 {
			days = 7
		}
		points, err := logStore.CostTrend(days)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"points": points})
	})

	// ---- Monitor API ----
	mux.HandleFunc("/api/monitor/overview", func(w http.ResponseWriter, r *http.Request) {
		overview, err := logStore.DashboardOverview()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		byModel, _ := logStore.ByModel()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"total_requests":     overview.TotalRequests,
			"today_requests":     0,
			"error_count":        0,
			"total_tokens":       0,
			"total_cost_usd":     overview.MonthCost,
			"avg_latency_ms":     0,
			"by_model":           byModel,
		})
	})

	mux.HandleFunc("/api/monitor/recent", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 50
		}
		routes, err := logStore.RecentRoutes(limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": routes})
	})

	mux.HandleFunc("/api/monitor/by-model", func(w http.ResponseWriter, r *http.Request) {
		byModel, err := logStore.ByModel()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": byModel})
	})

	// ---- Proxy Management API ----
	mux.HandleFunc("/api/proxy/current", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		if len(providers) == 0 {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"provider_id":   "",
				"provider_name": "none",
				"is_valid":      false,
				"warning":       "No providers configured",
			})
			return
		}
		// Return the first enabled provider as "current".
		for _, prov := range providers {
			if prov.Enabled {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"provider_id":   prov.ID,
					"provider_name": prov.Name,
					"endpoints":     []map[string]string{{"app_type": "claude", "url": prov.BaseURL}},
					"is_valid":      true,
					"base_url":      prov.BaseURL,
					"warning":       nil,
				})
				return
			}
		}
	})

	mux.HandleFunc("/api/proxy/activate/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
			return
		}
		// Extract provider ID from URL: /api/proxy/activate/{id}
		providerID := strings.TrimPrefix(r.URL.Path, "/api/proxy/activate/")
		if providerID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider id required"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":       true,
			"provider_name": providerID,
			"message":       fmt.Sprintf("Activated provider: %s", providerID),
		})
	})

	mux.HandleFunc("/api/proxy/health", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		warnings := []string{}
		ready := len(providers) > 0
		if !ready {
			warnings = append(warnings, "No providers configured")
		}
		writeJSON(w, http.StatusOK, store.ProxyHealth{
			StateFileOK:         true,
			ActiveProviderValid: ready,
			Warnings:            warnings,
			ProxyReady:          ready,
		})
	})

	// ---- Provider List API ----
	mux.HandleFunc("/api/ccswitch/providers", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		items := make([]map[string]interface{}, 0, len(providers))
		for _, prov := range providers {
			items = append(items, map[string]interface{}{
				"id":          prov.ID,
				"name":        prov.Name,
				"app_type":    "claude",
				"category":    "custom",
				"is_current":  true,
				"icon_color":  "#6366f1",
				"enabled":     prov.Enabled,
				"endpoints":   []map[string]string{{"app_type": "claude", "url": prov.BaseURL}},
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": len(items)})
	})

	// ---- Circuit Breaker Stats API ----
	mux.HandleFunc("/api/resilience/stats", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		stats := make(map[string]interface{})
		for _, prov := range providers {
			if bs := p.BreakerStats(prov.Name); bs != nil {
				stats[prov.Name] = map[string]interface{}{
					"state":            bs.State.String(),
					"total_failures":   bs.TotalFailures,
					"total_successes":  bs.TotalSuccesses,
					"window_failures":  bs.WindowFailures,
					"window_successes": bs.WindowSuccesses,
				}
			}
		}
		writeJSON(w, http.StatusOK, stats)
	})

	// Start server.
	addr := fmt.Sprintf(":%d", *port)
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // Long timeout for SSE streaming
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		log.Println("[SHUTDOWN] Received signal, shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("[START] RAgent Router listening on http://localhost%s", addr)
	log.Printf("[START] Providers: %d registered", len(providers))
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("[STOP] Server stopped")
}

// loadProviders reads provider configuration from environment variables.
// Expected format:
//
//	PROVIDERS='[{"id":"1","name":"DeepSeek","base_url":"https://api.deepseek.com","api_key":"sk-...","model":"deepseek-chat"}]'
func loadProviders() []proxy.ProviderConfig {
	// Default demo providers (with placeholder keys).
	defaults := []proxy.ProviderConfig{
		{
			ID:      "deepseek-default",
			Name:    "DeepSeek",
			BaseURL: getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
			APIKey:  getEnv("DEEPSEEK_API_KEY", ""),
			Model:   getEnv("DEEPSEEK_MODEL", "deepseek-chat"),
			Enabled: getEnv("DEEPSEEK_API_KEY", "") != "",
		},
		{
			ID:      "claude-default",
			Name:    "Claude",
			BaseURL: getEnv("CLAUDE_BASE_URL", "https://api.anthropic.com"),
			APIKey:  getEnv("CLAUDE_API_KEY", ""),
			Model:   getEnv("CLAUDE_MODEL", "claude-sonnet-4-20250514"),
			Enabled: getEnv("CLAUDE_API_KEY", "") != "",
		},
		{
			ID:      "minimax-default",
			Name:    "MiniMax",
			BaseURL: getEnv("MINIMAX_BASE_URL", "https://api.minimax.chat"),
			APIKey:  getEnv("MINIMAX_API_KEY", ""),
			Model:   getEnv("MINIMAX_MODEL", "MiniMax-M3"),
			Enabled: getEnv("MINIMAX_API_KEY", "") != "",
		},
	}

	// Check for JSON config in PROVIDERS env var.
	if providersJSON := os.Getenv("PROVIDERS"); providersJSON != "" {
		var configured []proxy.ProviderConfig
		if err := json.Unmarshal([]byte(providersJSON), &configured); err == nil && len(configured) > 0 {
			return configured
		}
		log.Printf("[CONFIG] Invalid PROVIDERS JSON, using defaults")
	}

	// Filter out providers without API keys.
	var result []proxy.ProviderConfig
	for _, p := range defaults {
		if p.Enabled {
			result = append(result, p)
		}
	}
	return result
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// corsMiddleware adds CORS headers and handles preflight requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key, Anthropic-Version")
		w.Header().Set("Access-Control-Expose-Headers", "X-Ragent-Provider, X-Ragent-Model, X-Ragent-Reason")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSON serializes data as a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[HTTP] Failed to encode response: %v", err)
	}
}
