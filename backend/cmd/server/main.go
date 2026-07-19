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
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/ragent/router/internal/api"
	"github.com/ragent/router/internal/mock"
	"github.com/ragent/router/internal/provider"
	"github.com/ragent/router/internal/orchestrator"
	"github.com/ragent/router/internal/proxy"
	proxymw "github.com/ragent/router/internal/proxy/middleware"
	"github.com/ragent/router/internal/routing"
	"github.com/ragent/router/internal/semcache"
	"github.com/ragent/router/internal/store"
)

func main() {
	// ── 命令行参数 ──
	port := flag.Int("port", 15722, "监听端口")
	dbPath := flag.String("db", "ragent_router.db", "SQLite 数据库路径")
	flag.Parse()

	// ── Mock 模式：无需 API Key，一键启动完整 Demo ──
	mockMode := os.Getenv("MOCK_MODE") == "true"
	if mockMode {
		log.Println("╔══════════════════════════════════════════════════════╗")
		log.Println("║       RAgent Router — Mock Demo 模式                 ║")
		log.Println("║  无需 API Key，所有功能开箱即用                       ║")
		log.Println("║  Dashboard: http://localhost:15722                   ║")
		log.Println("╚══════════════════════════════════════════════════════╝")
	}

	// ── 初始化存储层 ──
	logStore, err := store.NewLogStore(*dbPath)
	if err != nil {
		log.Fatalf("[启动] 数据库初始化失败: %v", err)
	}
	defer logStore.Close()
	log.Printf("[启动] 数据库已就绪: %s", *dbPath)

	// ── 加载供应商配置 ──
	var providers []proxy.ProviderConfig
	var mockEmbedder *mock.MockEmbeddingService

	if mockMode {
		upstreamAddr, emb, provs := mock.Setup(logStore)
		providers = provs
		mockEmbedder = emb
		log.Printf("[Mock] 供应商: %s (Claude) + %s (DeepSeek)", providers[0].Name, providers[1].Name)
		log.Printf("[Mock] 上游地址: %s", upstreamAddr)
	} else {
		providers = loadProviders()
		if len(providers) == 0 {
			log.Println("[警告] 未配置任何供应商——请设置环境变量或 PROVIDERS JSON")
			log.Println("[警告] 或使用 MOCK_MODE=true 启动 Demo 模式")
		}
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
	if mockMode && mockEmbedder != nil {
		hybridCfg.EmbeddingService = mockEmbedder
		embeddingConfigured = true
		log.Println("[Mock] Embedding 服务: 已启用（确定性的 mock 向量）")
	} else if embeddingKey := os.Getenv("EMBEDDING_API_KEY"); embeddingKey != "" {
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
	rc := provider.DefaultResilienceConfig()
	p := proxy.NewProxy(proxy.Config{
		Providers:             providers,
		Matcher:               engine,
		GlobalRateLimit:       rc.GlobalRateLimit,
		MaxConcurrentRequests: rc.MaxConcurrentRequests,
	})

	// ── 请求日志回调：代理请求完成后写入 SQLite ──
	p.OnRequestLog = func(rl proxy.RequestLog) {
		record := &store.RequestLogRecord{
			ID:                 uuid.NewString(),
			Prompt:             store.CompactPrompt(rl.Prompt, 500),
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

	// ── 初始化语义缓存 ──
	// Mock 模式下降低阈值到 0.85，使得相似但不完全相同的 prompt 也能命中（展示功能）
	var cacheService *semcache.Service
	if embeddingConfigured {
		cacheThreshold := 0.92
		if mockMode {
			cacheThreshold = 0.85 // Mock 模式下放宽阈值，提高命中率
		}
		cacheStore, err := store.NewSemanticCacheStore(logStore.DB(), cacheThreshold, 1000)
		if err != nil {
			log.Printf("[缓存] 初始化失败: %v", err)
		} else {
			cacheService = semcache.New(cacheStore, engine.GetEmbeddingService())
			p.Cache = cacheService
			log.Println("[缓存] 语义缓存已启用（阈值=0.92, 容量=1000）")
		}
	} else {
		log.Println("[缓存] Embedding 服务未配置，语义缓存已禁用")
	}

	// ── 初始化多模型编排引擎 ──
	if len(providers) >= 2 {
		caller := p.NewOrchestratorCaller()
		orchEngine := orchestrator.New(caller)
		p.Orchestrator = proxy.NewOrchestratorAdapter(orchEngine, p)
		log.Printf("[编排] 多模型编排已启用（%d 个供应商可用）", len(providers))
	} else {
		log.Println("[编排] 供应商不足 2 个，多模型编排已禁用")
	}

	// ── 初始化中间件管线 ──
	p.Pipeline = proxy.NewPipeline(
		&proxymw.PromptAnalyzer{}, // Demo: 自动分析 prompt 复杂度
	)
	log.Printf("[管线] 已注册 %d 个中间件", p.Pipeline.Len())

	// ── 构建 HTTP 路由 ──
	mux := http.NewServeMux()

	api.RegisterRoutes(mux, api.Dependencies{
		Proxy:                p,
		LogStore:             logStore,
		IntentStore:          intentStore,
		RoutingEngine:        engine,
		Providers:            providers,
		DefaultProvider:      defaultProvider,
		EmbeddingConfigured:  embeddingConfigured,
		ClassifierConfigured: classifierConfigured,
		ReloadIntents: func(engine *routing.HybridRouter) {
			reloadIntents(intentStore, engine, providerIDToName)
		},
		SemanticCache: cacheService,
	})

	// ── 启动服务器 ──
	addr := fmt.Sprintf(":%d", *port)
	server := &http.Server{
		Addr:         addr,
		// 中间件链（从外到内）：recovery → requestID → auth → CORS
		Handler: api.Recovery(api.RequestID(api.Auth(api.CORS(api.DefaultCORSOrigins())(mux)))),
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
			if err := json.Unmarshal([]byte(r.Examples), &examples); err != nil {
				log.Printf("[意图] examples 字段解析失败 (intent=%s): %v", r.IntentCode, err)
				examples = []string{}
			}
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

