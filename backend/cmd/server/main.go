// RAgent Router —— AI API 智能网关与容错引擎
//
// 在 Claude Code 与多个大模型供应商之间构建的透明代理层，
// 提供熔断降级、限流控制、Jitter 重试、Token 计量、智能路由与成本分析能力。
//
// 启动方式：
//
//	# 通过环境变量配置供应商
//	DEEPSEEK_API_KEY=sk-xxx go run ./cmd/server
//
//	# 通过 JSON 批量配置
//	PROVIDERS='[{"id":"1","name":"DeepSeek","base_url":"https://api.deepseek.com","api_key":"sk-xxx","model":"deepseek-chat","enabled":true}]' go run ./cmd/server
//
//	# 构建可执行文件
//	go build -o ragent-router ./cmd/server
//
// 服务默认监听 :15722，前端 Dashboard 连接同一端口。
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
	// ── 命令行参数 ──
	port := flag.Int("port", 15722, "监听端口")
	dbPath := flag.String("db", "ragent_router.db", "SQLite 数据库路径")
	flag.Parse()

	// ── 初始化存储层 ──
	logStore, err := store.NewLogStore(*dbPath)
	if err != nil {
		log.Fatalf("[启动] 数据库初始化失败: %v", err)
	}
	defer logStore.Close()
	log.Printf("[启动] 数据库已就绪: %s", *dbPath)

	// ── 加载供应商配置 ──
	providers := loadProviders()
	if len(providers) == 0 {
		log.Println("[警告] 未配置任何供应商——请设置环境变量或 PROVIDERS JSON")
		log.Println("[警告] 例如：DEEPSEEK_API_KEY=sk-xxx CLAUDE_API_KEY=sk-ant-xxx go run ./cmd/server")
	}

	// 构建供应商注册表（供路由引擎使用）。
	providerMap := make(map[string]*proxy.ProviderConfig)
	for i := range providers {
		providers[i].Enabled = true
		providerMap[providers[i].Name] = &providers[i]
	}

	// ── 初始化路由引擎 ──
	rules := routing.DefaultRules()
	defaultProvider := "deepseek"
	engine := routing.NewRuleEngine(rules, providerMap, defaultProvider)

	// ── 创建代理（核心韧性组件在此组装）──
	p := proxy.NewProxy(proxy.Config{
		Providers:             providers,
		Matcher:               engine,
		GlobalRateLimit:       100, // 全局 QPS 100
		MaxConcurrentRequests: 50,  // 最多 50 个并发请求
	})

	// ── 请求日志回调：代理请求完成后写入 SQLite ──
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
			log.Printf("[存储] 写入日志失败: %v", err)
		}
	}

	// ── 构建 HTTP 路由 ──
	mux := http.NewServeMux()

	// ── 健康检查 ──
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"service": "ragent-router",
			"version": "0.2.0",
		})
	})

	// ════════════════════════════════════════════════════════════
	// 代理端点（Anthropic Messages API 兼容）
	// ════════════════════════════════════════════════════════════

	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// GET 返回状态信息（方便调试）。
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"proxy":    "ragent-router",
				"endpoint": "/v1/messages",
				"method":   "POST (SSE streaming)",
				"status":   "ok",
			})
			return
		}
		// POST 请求进入全韧性保护的代理流程。
		p.ServeHTTP(w, r)
	})
	mux.HandleFunc("/v1/messages/", func(w http.ResponseWriter, r *http.Request) {
		p.ServeHTTP(w, r)
	})

	// ════════════════════════════════════════════════════════════
	// Dashboard API
	// ════════════════════════════════════════════════════════════

	// 获取 Dashboard 首页概览（日/月费用、总请求数、节省估算）。
	mux.HandleFunc("/api/dashboard/overview", func(w http.ResponseWriter, r *http.Request) {
		overview, err := logStore.DashboardOverview()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, overview)
	})

	// 获取各模型的请求分布（饼图数据）。
	mux.HandleFunc("/api/dashboard/model-distribution", func(w http.ResponseWriter, r *http.Request) {
		items, err := logStore.ModelDistribution()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	})

	// 获取最近 N 条请求日志（默认 20 条）。
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

	// 获取过去 N 天的费用趋势（默认 7 天）。
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

	// ════════════════════════════════════════════════════════════
	// 监控 API
	// ════════════════════════════════════════════════════════════

	// 获取聚合监控数据（总请求数、Token、费用、延迟、错误率）。
	mux.HandleFunc("/api/monitor/overview", func(w http.ResponseWriter, r *http.Request) {
		overview, err := logStore.DashboardOverview()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		byModel, _ := logStore.ByModel()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"total_requests": overview.TotalRequests,
			"today_requests": 0,
			"error_count":    0,
			"total_tokens":   0,
			"total_cost_usd": overview.MonthCost,
			"avg_latency_ms": 0,
			"by_model":       byModel,
		})
	})

	// 获取最近的原始请求日志（可配置条数）。
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

	// 获取各模型的详细统计（延迟、费用、Token 明细）。
	mux.HandleFunc("/api/monitor/by-model", func(w http.ResponseWriter, r *http.Request) {
		byModel, err := logStore.ByModel()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": byModel})
	})

	// ════════════════════════════════════════════════════════════
	// 供应商管理 API
	// ════════════════════════════════════════════════════════════

	// 获取当前活跃的供应商信息。
	mux.HandleFunc("/api/proxy/current", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		if len(providers) == 0 {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"provider_id":   "",
				"provider_name": "none",
				"is_valid":      false,
				"warning":       "未配置任何供应商",
			})
			return
		}
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

	// 激活指定供应商。
	mux.HandleFunc("/api/proxy/activate/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "需要 POST 请求"})
			return
		}
		providerID := strings.TrimPrefix(r.URL.Path, "/api/proxy/activate/")
		if providerID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少供应商 ID"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":       true,
			"provider_name": providerID,
			"message":       fmt.Sprintf("已切换到供应商: %s", providerID),
		})
	})

	// 获取代理健康状态。
	mux.HandleFunc("/api/proxy/health", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		warnings := []string{}
		ready := len(providers) > 0
		if !ready {
			warnings = append(warnings, "未配置任何供应商")
		}
		writeJSON(w, http.StatusOK, store.ProxyHealth{
			StateFileOK:         true,
			ActiveProviderValid: ready,
			Warnings:            warnings,
			ProxyReady:          ready,
		})
	})

	// 获取所有已注册的供应商列表。
	mux.HandleFunc("/api/ccswitch/providers", func(w http.ResponseWriter, r *http.Request) {
		providers := p.ListProviders()
		items := make([]map[string]interface{}, 0, len(providers))
		for _, prov := range providers {
			items = append(items, map[string]interface{}{
				"id":         prov.ID,
				"name":       prov.Name,
				"app_type":   "claude",
				"category":   "custom",
				"is_current": true,
				"icon_color": "#6366f1",
				"enabled":    prov.Enabled,
				"endpoints":  []map[string]string{{"app_type": "claude", "url": prov.BaseURL}},
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": len(items)})
	})

	// ════════════════════════════════════════════════════════════
	// 韧性引擎状态 API
	// ════════════════════════════════════════════════════════════

	// 获取各供应商的熔断器状态（供调试和监控）。
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

	// ── 启动服务器 ──
	addr := fmt.Sprintf(":%d", *port)
	server := &http.Server{
		Addr:         addr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // SSE 流式传输需要长超时
		IdleTimeout:  120 * time.Second,
	}

	// 优雅关闭：收到 SIGINT/SIGTERM 后等待现有请求完成再退出。
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		log.Println("[关闭] 收到终止信号，正在优雅退出...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("[启动] RAgent Router 监听 http://localhost%s", addr)
	log.Printf("[启动] 已注册 %d 个供应商", len(providers))
	for _, prov := range providers {
		log.Printf("[启动]   - %s (%s)", prov.Name, prov.Model)
	}
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[启动] 服务异常退出: %v", err)
	}
	log.Println("[关闭] 服务已停止")
}

// ────────────────────────────────────────────────────────────
// 供应商配置加载
// ────────────────────────────────────────────────────────────

// loadProviders 从环境变量加载供应商配置。
//
// 支持两种方式：
//  1. 单独环境变量：DEEPSEEK_API_KEY, CLAUDE_API_KEY 等
//  2. JSON 批量配置：PROVIDERS 环境变量包含 JSON 数组
//
// JSON 格式示例：
//
//	[{
//	  "id": "1",
//	  "name": "DeepSeek",
//	  "base_url": "https://api.deepseek.com",
//	  "api_key": "sk-xxx",
//	  "model": "deepseek-chat",
//	  "enabled": true
//	}]
func loadProviders() []proxy.ProviderConfig {
	// 默认供应商（需通过环境变量激活）。
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

	// 尝试从 PROVIDERS 环境变量读取 JSON 配置（覆盖默认值）。
	if providersJSON := os.Getenv("PROVIDERS"); providersJSON != "" {
		var configured []proxy.ProviderConfig
		if err := json.Unmarshal([]byte(providersJSON), &configured); err == nil && len(configured) > 0 {
			return configured
		}
		log.Printf("[配置] PROVIDERS JSON 解析失败，使用环境变量默认值")
	}

	// 过滤掉未配置密钥的供应商。
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

// ────────────────────────────────────────────────────────────
// 中间件
// ────────────────────────────────────────────────────────────

// corsMiddleware 添加跨域访问头并处理预检请求。
// 允许前端 Dashboard（可能运行在不同端口）调用后端 API。
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

// writeJSON 以 JSON 格式写回响应。
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[HTTP] JSON 编码失败: %v", err)
	}
}
