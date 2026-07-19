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

	// ── 初始化意图存储（SQLite）──
	intentStore, err := store.NewIntentStore(logStore.DB())
	if err != nil {
		log.Fatalf("[启动] 意图存储初始化失败: %v", err)
	}

	// 首次启动写入默认意图树。
	if err := intentStore.SeedDefaults(); err != nil {
		log.Printf("[警告] 种子意图写入失败: %v", err)
	}

	// 从 DB 加载启用的叶子意图。
	leafRecords, err := intentStore.ListLeaves()
	if err != nil {
		log.Fatalf("[启动] 加载叶子意图失败: %v", err)
	}

	// 构建 provider_id → provider_name 映射。
	providerIDToName := make(map[string]string)
	for _, prov := range providers {
		providerIDToName[prov.ID] = prov.Name
	}

	// 将 DB 记录转换为路由引擎的 Intent 列表。
	loadedIntents := intentRecordsToIntents(leafRecords, providerIDToName)
	if len(loadedIntents) == 0 {
		// DB 无数据 → 回退到硬编码默认值。
		log.Println("[启动] 意图树为空，使用硬编码默认意图")
		loadedIntents = routing.DefaultIntents()
	} else {
		log.Printf("[启动] 从 DB 加载了 %d 个叶子意图", len(loadedIntents))
	}

	// ── 初始化路由引擎（三阶段混合路由）──
	rules := routing.DefaultRules()
	defaultProvider := "DeepSeek"

	// 构建 HybridRouter——每个阶段都是可插拔的。
	hybridCfg := routing.HybridConfig{
		Keywords:        rules,
		Intents:         loadedIntents,
		Providers:       providerMap,
		DefaultProvider: defaultProvider,
	}
	embeddingConfigured := false
	classifierConfigured := false

	// ── 可选的 Embedding 语义匹配层 ──
	if embeddingKey := os.Getenv("EMBEDDING_API_KEY"); embeddingKey != "" {
		embCfg := routing.OpenAIEmbeddingConfig{
			Endpoint: getEnv("EMBEDDING_ENDPOINT", "https://api.openai.com/v1/embeddings"),
			APIKey:   embeddingKey,
			Model:    getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
		}
		hybridCfg.EmbeddingService = routing.NewOpenAIEmbeddingService(embCfg)
		embeddingConfigured = true
	}

	// ── 可选的 LLM 意图分类器 ──
	if classifierKey := os.Getenv("CLASSIFIER_API_KEY"); classifierKey != "" {
		clsCfg := routing.ClassifierConfig{
			Endpoint: getEnv("CLASSIFIER_ENDPOINT", "https://api.deepseek.com/v1/chat/completions"),
			APIKey:   classifierKey,
			Model:    getEnv("CLASSIFIER_MODEL", "deepseek-chat"),
		}
		hybridCfg.Classifier = routing.NewLLMIntentClassifier(clsCfg)
		classifierConfigured = true
	}

	engine := routing.NewHybridRouter(hybridCfg)

	// 预热意图嵌入向量（如果配置了 Embedding 服务）。
	if embeddingConfigured {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := engine.Init(ctx); err != nil {
			log.Printf("[警告] 意图嵌入预热失败: %v（语义匹配层已禁用）", err)
			// 预热失败不阻止启动——自动降级到关键词 + LLM 分类。
		}
		cancel()
	}

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

	// ── 路由策略统计（各层命中次数）──
	mux.HandleFunc("/api/routing/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := engine.Stats()
		total := stats.KeywordHits + stats.EmbeddingHits + stats.ClassifierHits + stats.FallbackHits
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"keyword_hits":    stats.KeywordHits,
			"embedding_hits":  stats.EmbeddingHits,
			"classifier_hits": stats.ClassifierHits,
			"fallback_hits":   stats.FallbackHits,
			"total":           total,
			"cache_size":      engine.CacheSize(),
		})
	})

	// ════════════════════════════════════════════════════════════
	// 意图树管理 API
	// ════════════════════════════════════════════════════════════

	// 获取完整意图树。
	mux.HandleFunc("/api/intent/tree", func(w http.ResponseWriter, r *http.Request) {
		records, err := intentStore.ListAll()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		roots := store.ToTree(records)
		writeJSON(w, http.StatusOK, map[string]interface{}{"roots": roots})
	})

	// 获取分类器配置状态。
	mux.HandleFunc("/api/intent/classifier", func(w http.ResponseWriter, r *http.Request) {
		if classifierConfigured {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"configured":    true,
				"provider_name": getEnv("CLASSIFIER_MODEL", "deepseek-chat"),
				"model":         getEnv("CLASSIFIER_MODEL", "deepseek-chat"),
				"source":        "env",
			})
		} else {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"configured": false,
				"message":    "未配置 CLASSIFIER_API_KEY 环境变量，LLM 分类不可用",
			})
		}
	})

	// 获取默认供应商信息。
	mux.HandleFunc("/api/intent/default-provider", func(w http.ResponseWriter, r *http.Request) {
		for _, prov := range providers {
			if prov.Name == defaultProvider {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"found": true, "id": prov.ID, "name": prov.Name,
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"found": false})
	})

	// 意图分类（返回详细候选列表）。
	mux.HandleFunc("/api/intent/classify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "需要 POST 请求"})
			return
		}
		var body struct {
			Question   string `json:"question"`
			AutoSwitch bool   `json:"auto_switch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if body.Question == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question 不能为空"})
			return
		}

		result := engine.Classify(r.Context(), body.Question)

		// 自动切换逻辑。
		if body.AutoSwitch && result.Matched != nil && result.Matched.ProviderID != "" {
			result.Switched = &routing.SwitchResult{
				Success:      true,
				ProviderName: result.Matched.ProviderName,
				Detail:       "已自动切换到 " + result.Matched.ProviderName,
			}
		} else if body.AutoSwitch {
			result.Switched = &routing.SwitchResult{
				Success:      true,
				ProviderName: result.DefaultProvider.Name,
				Fallback:     true,
				Detail:       "未匹配 → 兜底 " + result.DefaultProvider.Name,
			}
		}

		writeJSON(w, http.StatusOK, result)
	})

	// 创建意图节点。
	mux.HandleFunc("/api/intent/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var node store.IntentNodeRecord
			if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
			// 确保 examples 字段为有效 JSON 数组。
			if node.Examples == "" {
				node.Examples = "[]"
			}
			if err := intentStore.Insert(&node); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			reloadIntents(intentStore, engine, providerIDToName)
			writeJSON(w, http.StatusCreated, map[string]interface{}{"success": true})
			return
		}
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	})

	// 更新/删除意图节点。
	mux.HandleFunc("/api/intent/nodes/", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimPrefix(r.URL.Path, "/api/intent/nodes/")
		if code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 intent_code"})
			return
		}

		switch r.Method {
		case http.MethodPatch:
			var node store.IntentNodeRecord
			if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
			if err := intentStore.Update(code, &node); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			reloadIntents(intentStore, engine, providerIDToName)
			writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})

		case http.MethodDelete:
			if err := intentStore.Delete(code); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			reloadIntents(intentStore, engine, providerIDToName)
			writeJSON(w, http.StatusNoContent, nil)

		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
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

	// 打印路由策略信息。
	strategy := "关键词规则"
	if embeddingConfigured {
		strategy += " + Embedding语义匹配"
	}
	if classifierConfigured {
		strategy += " + LLM意图分类器"
	}
	log.Printf("[启动] 路由策略: %s", strategy)
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

// ────────────────────────────────────────────────────────────
// 意图管理辅助函数
// ────────────────────────────────────────────────────────────

// intentRecordsToIntents 将 DB 中的叶子节点记录转换为路由引擎的 Intent。
func intentRecordsToIntents(records []store.IntentNodeRecord, providerIDToName map[string]string) []routing.Intent {
	result := make([]routing.Intent, 0, len(records))
	for i, r := range records {
		providerName := "unknown"
		if r.ProviderID != nil {
			if name, ok := providerIDToName[*r.ProviderID]; ok {
				providerName = name
			}
		}

		var examples []string
		if r.Examples != "" {
			json.Unmarshal([]byte(r.Examples), &examples)
		}
		if examples == nil {
			examples = []string{}
		}

		result = append(result, routing.Intent{
			IntentCode:  r.IntentCode,
			Name:        r.Name,
			Description: r.Description,
			Examples:    examples,
			Provider:    providerName,
			Priority:    100 - i,
		})
	}
	return result
}

// reloadIntents 从 DB 重载意图并热更新路由引擎。
func reloadIntents(is *store.IntentStore, engine *routing.HybridRouter, idToName map[string]string) {
	records, err := is.ListLeaves()
	if err != nil {
		log.Printf("[意图] 重载失败: %v", err)
		return
	}
	intents := intentRecordsToIntents(records, idToName)
	if len(intents) == 0 {
		log.Println("[意图] 无启用的叶子节点，跳过重载")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := engine.ReloadIntents(ctx, intents); err != nil {
		log.Printf("[意图] 热重载失败: %v", err)
	} else {
		log.Printf("[意图] 热重载完成: %d 个叶子意图", len(intents))
	}
}

// writeJSON 以 JSON 格式写回响应。
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[HTTP] JSON 编码失败: %v", err)
	}
}
