// RAgent Router —— AI API 智能网关与容错引擎 v0.3.0
//
// 全面升级版：Gin + GORM + Redis + JWT 认证 + 渠道管理 + 计费系统
//
// 启动方式：
//
//	MOCK_MODE=true go run ./cmd/server           # Mock 演示模式
//	DEEPSEEK_API_KEY=sk-xxx go run ./cmd/server   # 生产模式
//	go build -o ragent-router ./cmd/server        # 构建
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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ragent/router/common"
	"github.com/ragent/router/internal/api"
	"github.com/ragent/router/internal/mock"
	"github.com/ragent/router/internal/orchestrator"
	"github.com/ragent/router/internal/proxy"
	proxymw "github.com/ragent/router/internal/proxy/middleware"
	"github.com/ragent/router/internal/provider"
	"github.com/ragent/router/internal/routing"
	"github.com/ragent/router/internal/semcache"
	"github.com/ragent/router/internal/store"
	"github.com/ragent/router/middleware"
	"github.com/ragent/router/model"
	"github.com/ragent/router/router"
)

func main() {
	// ── 命令行参数 ──
	port := flag.Int("port", 15722, "监听端口")
	dbPath := flag.String("db", "ragent_router.db", "SQLite 数据库路径")
	flag.Parse()

	// ── Mock 模式 ──
	mockMode := os.Getenv("MOCK_MODE") == "true"
	if mockMode {
		log.Println("╔══════════════════════════════════════════════════════╗")
		log.Println("║       RAgent Router v0.3.0 — Mock Demo 模式          ║")
		log.Println("║  无需 API Key，所有功能开箱即用                       ║")
		log.Println("║  Dashboard: http://localhost:15722                   ║")
		log.Println("║  默认用户: root / 123456                             ║")
		log.Println("╚══════════════════════════════════════════════════════╝")
	}

	// ── 初始化 GORM 数据库 ──
	if err := model.InitDB(*dbPath); err != nil {
		log.Fatalf("[启动] 数据库初始化失败: %v", err)
	}
	log.Printf("[启动] 数据库已就绪: %s", *dbPath)

	// ── 初始化 Redis（可选）──
	common.InitRedisClient()

	// ── 初始化认证 ──
	middleware.InitAuth()

	// ── 初始化 Redis 深度功能 ──
	common.InitStreamProducers() // Redis Streams 消息队列
	common.InitEventBus()        // Redis Pub/Sub 事件总线
	common.InitMetrics()         // Prometheus 指标采集
	common.InitWebSocket()       // WebSocket 实时推送

	// ── 初始化 Elasticsearch（可选）──
	common.InitElasticsearch()

	// ── 初始化链路追踪 ──
	common.InitTracing()

	// ── 初始化旧存储层（用于日志和意图，复用 GORM 的 DB 连接）──
	gormDB, err := model.DB.DB()
	if err != nil {
		log.Fatalf("[启动] 获取底层 DB 连接失败: %v", err)
	}
	logStore, err := store.NewLogStoreFromDB(gormDB)
	if err != nil {
		log.Fatalf("[启动] 旧存储层初始化失败: %v", err)
	}
	defer logStore.Close()

	// ── 加载供应商配置 ──
	var providers []proxy.ProviderConfig
	var mockEmbedder *mock.MockEmbeddingService

	if mockMode {
		upstreamAddr, emb, provs := mock.Setup(logStore)
		providers = provs
		mockEmbedder = emb
		log.Printf("[Mock] 供应商: %s (Claude) + %s (DeepSeek)", providers[0].Name, providers[1].Name)
		log.Printf("[Mock] 上游地址: %s", upstreamAddr)

		// Mock 模式下自动创建渠道
		for _, prov := range providers {
			ch := &model.Channel{
				Type:    0,
				Key:     prov.APIKey,
				Status:  model.ChannelStatusEnabled,
				Name:    prov.Name,
				BaseURL: prov.BaseURL,
				Models:  prov.Model,
				Weight:  1,
				Group:   "default",
			}
			model.CreateChannel(ch)
		}
	} else {
		providers = loadProviders()
	}

	// 构建供应商注册表
	providerMap := make(map[string]*proxy.ProviderConfig)
	for i := range providers {
		providers[i].Enabled = true
		providerMap[providers[i].Name] = &providers[i]
	}

	// ── 初始化意图存储 ──
	intentStore, err := store.NewIntentStore(logStore.DB())
	if err != nil {
		log.Fatalf("[启动] 意图存储初始化失败: %v", err)
	}
	intentStore.SeedDefaults()
	leafRecords, _ := intentStore.ListLeaves()

	providerIDToName := make(map[string]string)
	for _, prov := range providers {
		providerIDToName[prov.ID] = prov.Name
	}
	loadedIntents := intentRecordsToIntents(leafRecords, providerIDToName)
	if len(loadedIntents) == 0 {
		loadedIntents = routing.DefaultIntents()
	}

	// ── 初始化路由引擎 ──
	rules := routing.DefaultRules()
	defaultProvider := "DeepSeek"

	hybridCfg := routing.HybridConfig{
		Keywords:        rules,
		Intents:         loadedIntents,
		Providers:       providerMap,
		DefaultProvider: defaultProvider,
	}
	embeddingConfigured := false

	if mockMode && mockEmbedder != nil {
		hybridCfg.EmbeddingService = mockEmbedder
		embeddingConfigured = true
	} else if embeddingKey := os.Getenv("EMBEDDING_API_KEY"); embeddingKey != "" {
		embCfg := routing.OpenAIEmbeddingConfig{
			Endpoint: common.GetEnv("EMBEDDING_ENDPOINT", "https://api.openai.com/v1/embeddings"),
			APIKey:   embeddingKey,
			Model:    common.GetEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
		}
		hybridCfg.EmbeddingService = routing.NewOpenAIEmbeddingService(embCfg)
		embeddingConfigured = true
	}

	if classifierKey := os.Getenv("CLASSIFIER_API_KEY"); classifierKey != "" {
		clsCfg := routing.ClassifierConfig{
			Endpoint: common.GetEnv("CLASSIFIER_ENDPOINT", "https://api.deepseek.com/v1/chat/completions"),
			APIKey:   classifierKey,
			Model:    common.GetEnv("CLASSIFIER_MODEL", "deepseek-chat"),
		}
		hybridCfg.Classifier = routing.NewLLMIntentClassifier(clsCfg)
	}

	engine := routing.NewHybridRouter(hybridCfg)
	if embeddingConfigured {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		engine.Init(ctx)
		cancel()
	}

	// ── 创建代理 ──
	rc := provider.DefaultResilienceConfig()
	p := proxy.NewProxy(proxy.Config{
		Providers:             providers,
		Matcher:               engine,
		GlobalRateLimit:       rc.GlobalRateLimit,
		MaxConcurrentRequests: rc.MaxConcurrentRequests,
	})

	// ── 请求日志回调 ──
	p.OnRequestLog = func(rl proxy.RequestLog) {
		prompt := store.CompactPrompt(rl.Prompt, 500)

		// 写入旧存储（兼容）
		logStore.Insert(&store.RequestLogRecord{
			ID:                uuid.NewString(),
			Prompt:            prompt,
			PromptTokens:      rl.PromptTokens,
			CompletionTokens:  rl.CompletionTokens,
			TotalTokens:       rl.TotalTokens,
			Model:             rl.Model,
			Provider:          rl.Provider,
			RouteReason:       rl.RouteReason,
			Status:            rl.Status,
			ErrorDetail:       rl.ErrorDetail,
			UpstreamRequestID: rl.UpstreamID,
			CostUSD:           rl.CostUSD,
			LatencyMs:         rl.LatencyMs,
			CreatedAt:         rl.Timestamp,
		})

		// 写入 GORM 存储
		model.InsertLog(&model.RequestLog{
			Prompt:            prompt,
			PromptTokens:      rl.PromptTokens,
			CompletionTokens:  rl.CompletionTokens,
			TotalTokens:       rl.TotalTokens,
			Model:             rl.Model,
			Provider:          rl.Provider,
			RouteReason:       rl.RouteReason,
			Status:            rl.Status,
			ErrorDetail:       rl.ErrorDetail,
			UpstreamRequestId: rl.UpstreamID,
			CostUSD:           rl.CostUSD,
			LatencyMs:         rl.LatencyMs,
		})

		// 发布到 Redis Streams（异步消息队列）
		common.PublishLog(context.Background(), map[string]interface{}{
			"prompt":   prompt,
			"model":    rl.Model,
			"provider": rl.Provider,
			"status":   rl.Status,
			"tokens":   rl.TotalTokens,
			"cost":     rl.CostUSD,
			"latency":  rl.LatencyMs,
		})

		// 发布到 Redis Pub/Sub（实时事件广播）
		common.PublishEvent(common.ChannelRequest, common.EventTypeRequestComplete, map[string]interface{}{
			"provider": rl.Provider,
			"model":    rl.Model,
			"status":   rl.Status,
		})

		// 写入 Elasticsearch（全文检索）
		if common.ESClientInstance != nil {
			common.ESClientInstance.IndexDocument(context.Background(), uuid.NewString(), map[string]interface{}{
				"prompt":            prompt,
				"model":             rl.Model,
				"provider":          rl.Provider,
				"status":            rl.Status,
				"route_reason":      rl.RouteReason,
				"error_detail":      rl.ErrorDetail,
				"cost_usd":          rl.CostUSD,
				"latency_ms":        rl.LatencyMs,
				"prompt_tokens":     rl.PromptTokens,
				"completion_tokens": rl.CompletionTokens,
				"total_tokens":      rl.TotalTokens,
				"created_at":        rl.Timestamp.UnixMilli(),
			})
		}

		// 记录 Prometheus 指标
		common.RecordRequest("POST", rl.Status, rl.Provider, rl.Model,
			time.Duration(rl.LatencyMs)*time.Millisecond,
			rl.PromptTokens, rl.CompletionTokens, rl.CostUSD)
	}

	// ── 语义缓存 ──
	var cacheService *semcache.Service
	if embeddingConfigured {
		cacheThreshold := 0.92
		if mockMode {
			cacheThreshold = 0.85
		}
		cacheStore, err := store.NewSemanticCacheStore(logStore.DB(), cacheThreshold, 1000)
		if err == nil {
			cacheService = semcache.New(cacheStore, engine.GetEmbeddingService())
			p.Cache = cacheService
		}
	}

	// ── 多模型编排 ──
	if len(providers) >= 2 {
		caller := p.NewOrchestratorCaller()
		orchEngine := orchestrator.New(caller)
		p.Orchestrator = proxy.NewOrchestratorAdapter(orchEngine, p)
	}

	// ── 中间件管线 ──
	p.Pipeline = proxy.NewPipeline(&proxymw.PromptAnalyzer{})

	// ── 刷新渠道缓存 ──
	model.RefreshChannelCache()

	// ── 设置 Gin 路由 ──
	gin.SetMode(gin.ReleaseMode)
	if mockMode {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestID())
	r.Use(common.TraceMiddleware()) // 链路追踪
	r.Use(middleware.CORSMiddleware())
	r.Use(gin.Logger())

	// 注册路由
	router.SetRouter(r, p)

	// ── 旧 API 路由兼容（保留原有 Dashboard API）──
	mux := http.NewServeMux()
	api.RegisterRoutes(mux, api.Dependencies{
		Proxy:                p,
		LogStore:             logStore,
		IntentStore:          intentStore,
		RoutingEngine:        engine,
		Providers:            providers,
		DefaultProvider:      defaultProvider,
		EmbeddingConfigured:  embeddingConfigured,
		ClassifierConfigured: false,
		ReloadIntents: func(engine *routing.HybridRouter) {
			reloadIntents(intentStore, engine, providerIDToName)
		},
		SemanticCache: cacheService,
	})

	// 将旧 API 挂载到 Gin
	r.Any("/api/legacy/*path", func(c *gin.Context) {
		// 保留旧的 API 端点兼容性
		mux.ServeHTTP(c.Writer, c.Request)
	})

	// ── 启动服务器 ──
	addr := fmt.Sprintf(":%d", *port)

	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		log.Println("[关闭] 收到终止信号，正在优雅退出...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("[启动] RAgent Router v0.3.0 监听 http://localhost%s", addr)
	log.Printf("[启动] 已注册 %d 个供应商", len(providers))
	for _, prov := range providers {
		log.Printf("[启动]   - %s (%s)", prov.Name, prov.Model)
	}

	strategy := "关键词规则"
	if embeddingConfigured {
		strategy += " + Embedding语义匹配"
	}
	log.Printf("[启动] 路由策略: %s", strategy)
	log.Printf("[启动] 管理后台: http://localhost%s", addr)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[启动] 服务异常退出: %v", err)
	}
	log.Println("[关闭] 服务已停止")
}

// ── 供应商配置加载 ──

// providerDef 供应商定义（用于批量注册）。
type providerDef struct {
	id, name, envKey, baseURL, defaultModel string
}

// 所有支持的供应商定义。
var providerDefs = []providerDef{
	// 国际主流
	{"openai", "OpenAI", "OPENAI_API_KEY", "https://api.openai.com", "gpt-4o"},
	{"claude", "Claude", "CLAUDE_API_KEY", "https://api.anthropic.com", "claude-sonnet-4-20250514"},
	{"gemini", "Gemini", "GEMINI_API_KEY", "https://generativelanguage.googleapis.com", "gemini-pro"},
	{"deepseek", "DeepSeek", "DEEPSEEK_API_KEY", "https://api.deepseek.com", "deepseek-chat"},
	{"mistral", "Mistral", "MISTRAL_API_KEY", "https://api.mistral.ai", "mistral-large-latest"},
	{"cohere", "Cohere", "COHERE_API_KEY", "https://api.cohere.com", "command-r-plus"},
	{"perplexity", "Perplexity", "PERPLEXITY_API_KEY", "https://api.perplexity.ai", "llama-3.1-sonar-large-128k-online"},
	{"xai", "xAI", "XAI_API_KEY", "https://api.x.ai", "grok-beta"},
	{"ollama", "Ollama", "OLLAMA_API_KEY", "http://localhost:11434", "llama3"},
	{"openrouter", "OpenRouter", "OPENROUTER_API_KEY", "https://openrouter.ai/api", "anthropic/claude-3.5-sonnet"},

	// 国内主流
	{"ali", "Ali", "ALI_API_KEY", "https://dashscope.aliyuncs.com/compatible-mode", "qwen-plus"},
	{"baidu", "Baidu", "BAIDU_API_KEY", "https://aip.baidubce.com", "ernie-4.0-8k"},
	{"zhipu", "Zhipu", "ZHIPU_API_KEY", "https://open.bigmodel.cn/api/paas", "glm-4"},
	{"moonshot", "Moonshot", "MOONSHOT_API_KEY", "https://api.moonshot.cn", "moonshot-v1-128k"},
	{"minimax", "MiniMax", "MINIMAX_API_KEY", "https://api.minimax.chat", "MiniMax-M3"},
	{"tencent", "Tencent", "TENCENT_API_KEY", "https://hunyuan.tencentcloudapi.com", "hunyuan-pro"},
	{"xunfei", "Xunfei", "XUNFEI_API_KEY", "https://spark-api-open.xf-yun.com", "generalv3.5"},
	{"volcengine", "VolcEngine", "VOLCENGINE_API_KEY", "https://ark.cn-beijing.volces.com", "doubao-pro-128k"},
	{"siliconflow", "SiliconFlow", "SILICONFLOW_API_KEY", "https://api.siliconflow.cn", "deepseek-ai/DeepSeek-V2.5"},

	// 基础设施
	{"azure", "Azure", "AZURE_API_KEY", "", "gpt-4o"},
	{"bedrock", "Bedrock", "BEDROCK_API_KEY", "", "anthropic.claude-3-5-sonnet-20241022-v2:0"},
	{"vertex", "Vertex", "VERTEX_API_KEY", "", "gemini-pro"},
}

func loadProviders() []proxy.ProviderConfig {
	// 优先使用 PROVIDERS JSON 批量配置
	if providersJSON := os.Getenv("PROVIDERS"); providersJSON != "" {
		var configured []proxy.ProviderConfig
		if err := json.Unmarshal([]byte(providersJSON), &configured); err == nil && len(configured) > 0 {
			return configured
		}
		log.Printf("[配置] PROVIDERS JSON 解析失败，使用环境变量默认值")
	}

	// 从环境变量逐个加载
	var result []proxy.ProviderConfig
	for _, def := range providerDefs {
		apiKey := os.Getenv(def.envKey)
		if apiKey == "" {
			continue
		}

		baseURL := common.GetEnv(def.envKey+"_BASE_URL", def.baseURL)
		model := common.GetEnv(def.envKey+"_MODEL", def.defaultModel)

		result = append(result, proxy.ProviderConfig{
			ID:      def.id + "-default",
			Name:    def.name,
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   model,
			Enabled: true,
		})
	}

	if len(result) == 0 {
		log.Println("[警告] 未配置任何供应商——请设置环境变量或 PROVIDERS JSON")
		log.Println("[警告] 或使用 MOCK_MODE=true 启动 Demo 模式")
	}

	return result
}

// ── 意图相关 ──

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

func reloadIntents(is *store.IntentStore, engine *routing.HybridRouter, idToName map[string]string) {
	records, err := is.ListLeaves()
	if err != nil {
		return
	}
	intents := intentRecordsToIntents(records, idToName)
	if len(intents) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	engine.ReloadIntents(ctx, intents)
}
