package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ragent/router/internal/proxy"
	"github.com/ragent/router/internal/routing"
	"github.com/ragent/router/internal/semcache"
	"github.com/ragent/router/internal/store"
)

// Dependencies 是 API 层所需的所有依赖。
type Dependencies struct {
	Proxy           *proxy.Proxy
	LogStore        *store.LogStore
	IntentStore     *store.IntentStore
	RoutingEngine   *routing.HybridRouter
	Providers       []proxy.ProviderConfig
	DefaultProvider string
	SemanticCache   *semcache.Service // 可选：语义缓存服务

	// 可选配置状态
	EmbeddingConfigured  bool
	ClassifierConfigured bool

	// 意图热重载器
	ReloadIntents func(engine *routing.HybridRouter)
}

// RegisterRoutes 将所有 API 路由注册到 mux。
func RegisterRoutes(mux *http.ServeMux, deps Dependencies) {
	r := &router{deps}

	// ── 健康检查 ──
	mux.HandleFunc("/healthz", r.healthz)
	mux.HandleFunc("/health", r.health)
	mux.HandleFunc("/readyz", r.readyz)

	// ── 代理端点 ──
	mux.HandleFunc("/v1/messages", r.proxyMessages)
	mux.HandleFunc("/v1/messages/", r.proxyMessages)

	// ── Dashboard ──
	mux.HandleFunc("/api/dashboard/overview", r.dashboardOverview)
	mux.HandleFunc("/api/dashboard/model-distribution", r.dashboardModelDist)
	mux.HandleFunc("/api/dashboard/recent-routes", r.dashboardRecentRoutes)
	mux.HandleFunc("/api/dashboard/cost-trend", r.dashboardCostTrend)

	// ── Analytics ──
	mux.HandleFunc("/api/analytics/model-performance", r.analyticsModelPerf)

	// ── Monitor ──
	mux.HandleFunc("/api/monitor/overview", r.monitorOverview)
	mux.HandleFunc("/api/monitor/recent", r.monitorRecent)
	mux.HandleFunc("/api/monitor/by-model", r.monitorByModel)

	// ── Proxy / Suppliers ──
	mux.HandleFunc("/api/proxy/current", r.proxyCurrent)
	mux.HandleFunc("/api/proxy/activate/", r.proxyActivate)
	mux.HandleFunc("/api/proxy/health", r.proxyHealth)
	mux.HandleFunc("/api/ccswitch/providers", r.ccswitchProviders)

	// ── Cache ──
	mux.HandleFunc("/api/cache/stats", r.cacheStats)
	mux.HandleFunc("/api/cache/clear", r.cacheClear)

	// ── Resilience ──
	mux.HandleFunc("/api/resilience/stats", r.resilienceStats)
	mux.HandleFunc("/api/routing/stats", r.routingStats)

	// ── Intent ──
	mux.HandleFunc("/api/intent/tree", r.intentTree)
	mux.HandleFunc("/api/intent/classifier", r.intentClassifier)
	mux.HandleFunc("/api/intent/default-provider", r.intentDefaultProvider)
	mux.HandleFunc("/api/intent/classify", r.intentClassify)
	mux.HandleFunc("/api/intent/nodes", r.intentNodes)
	mux.HandleFunc("/api/intent/nodes/", r.intentNodeByCode)
}

type router struct {
	Dependencies
}

// ── Health ────────────────────────────────────────────────────────────

func (r *router) healthz(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "ragent-router",
	})
}

func (r *router) health(w http.ResponseWriter, _ *http.Request) {
	dbStatus := "ok"
	if err := r.LogStore.DB().Ping(); err != nil {
		dbStatus = "error: " + err.Error()
	}
	n := 0
	for _, p := range r.Providers {
		if p.Enabled {
			n++
		}
	}
	status := "ok"
	if dbStatus != "ok" {
		status = "degraded"
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":          status,
		"service":         "ragent-router",
		"version":         "0.2.0",
		"database":        dbStatus,
		"providers_count": n,
	})
}

func (r *router) readyz(w http.ResponseWriter, _ *http.Request) {
	dbStatus := "ok"
	if err := r.LogStore.DB().Ping(); err != nil {
		dbStatus = "error: " + err.Error()
	}
	status := http.StatusOK
	body := map[string]interface{}{"status": "ok", "database": dbStatus}
	if dbStatus != "ok" {
		status = http.StatusServiceUnavailable
		body["status"] = "not ready"
	}
	WriteJSON(w, status, body)
}

// ── Proxy Messages ────────────────────────────────────────────────────

func (r *router) proxyMessages(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"proxy":    "ragent-router",
			"endpoint": "/v1/messages",
			"method":   "POST (SSE streaming)",
			"status":   "ok",
		})
		return
	}
	r.Proxy.ServeHTTP(w, req)
}

// ── Dashboard ─────────────────────────────────────────────────────────

func (r *router) dashboardOverview(w http.ResponseWriter, _ *http.Request) {
	overview, err := r.LogStore.DashboardOverview()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, overview)
}

func (r *router) dashboardModelDist(w http.ResponseWriter, _ *http.Request) {
	items, err := r.LogStore.ModelDistribution()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (r *router) dashboardRecentRoutes(w http.ResponseWriter, req *http.Request) {
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	routes, err := r.LogStore.RecentRoutes(limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"items": routes})
}

func (r *router) dashboardCostTrend(w http.ResponseWriter, req *http.Request) {
	days, _ := strconv.Atoi(req.URL.Query().Get("days"))
	if days <= 0 {
		days = 7
	}
	points, err := r.LogStore.CostTrend(days)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"points": points})
}

// ── Analytics ─────────────────────────────────────────────────────────

func (r *router) analyticsModelPerf(w http.ResponseWriter, _ *http.Request) {
	items, err := r.LogStore.ModelPerformance()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

// ── Monitor ───────────────────────────────────────────────────────────

func (r *router) monitorOverview(w http.ResponseWriter, _ *http.Request) {
	overview, err := r.LogStore.DashboardOverview()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	monitor, err := r.LogStore.MonitorOverview()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byModel, err := r.LogStore.ByModel()
	if err != nil {
		log.Printf("[监控] ByModel 查询失败: %v", err)
		byModel = nil
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"total_requests": overview.TotalRequests,
		"today_requests": monitor.TodayRequests,
		"error_count":    monitor.ErrorCount,
		"total_tokens":   monitor.TotalTokens,
		"total_cost_usd": overview.MonthCost,
		"avg_latency_ms": monitor.AvgLatencyMs,
		"by_model":       byModel,
	})
}

func (r *router) monitorRecent(w http.ResponseWriter, req *http.Request) {
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	routes, err := r.LogStore.RecentRoutes(limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"items": routes})
}

func (r *router) monitorByModel(w http.ResponseWriter, _ *http.Request) {
	byModel, err := r.LogStore.ByModel()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"items": byModel})
}

// ── Proxy / CC Switch ─────────────────────────────────────────────────

func (r *router) proxyCurrent(w http.ResponseWriter, _ *http.Request) {
	providers := r.Proxy.ListProviders()
	if len(providers) == 0 {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"provider_id":   "",
			"provider_name": "none",
			"is_valid":      false,
			"warning":       "未配置任何供应商",
		})
		return
	}
	for _, prov := range providers {
		if prov.Enabled {
			WriteJSON(w, http.StatusOK, map[string]interface{}{
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
	prov := providers[0]
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"provider_id":   prov.ID,
		"provider_name": prov.Name,
		"endpoints":     []map[string]string{{"app_type": "claude", "url": prov.BaseURL}},
		"is_valid":      false,
		"base_url":      prov.BaseURL,
		"warning":       "所有供应商均已禁用",
	})
}

func (r *router) proxyActivate(w http.ResponseWriter, req *http.Request) {
	providerID := strings.TrimPrefix(req.URL.Path, "/api/proxy/activate/")
	if providerID == "" {
		WriteError(w, http.StatusBadRequest, "缺少供应商 ID")
		return
	}

	switch req.Method {
	case http.MethodPost:
		providerName := ""
		for _, prov := range r.Providers {
			if prov.ID == providerID || prov.Name == providerID {
				providerName = prov.Name
				break
			}
		}
		if providerName == "" {
			WriteError(w, http.StatusNotFound, "供应商不存在: "+providerID)
			return
		}
		if ok := r.Proxy.SetDebugProvider(providerName); !ok {
			WriteError(w, http.StatusBadRequest, "无法激活供应商: "+providerName)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":       true,
			"provider_name": providerName,
			"message":       fmt.Sprintf("调试锁定模式：所有请求将路由到 %s", providerName),
		})
	case http.MethodDelete:
		r.Proxy.SetDebugProvider("")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "调试锁定已清除，恢复正常路由引擎",
		})
	default:
		WriteError(w, http.StatusMethodNotAllowed, "需要 POST 或 DELETE 请求")
	}
}

func (r *router) proxyHealth(w http.ResponseWriter, _ *http.Request) {
	providers := r.Proxy.ListProviders()
	warnings := []string{}
	ready := len(providers) > 0
	if !ready {
		warnings = append(warnings, "未配置任何供应商")
	}
	dbOK := r.LogStore.DB().Ping() == nil
	WriteJSON(w, http.StatusOK, store.ProxyHealth{
		DBOK:                dbOK,
		StateFileOK:         true,
		ActiveProviderValid: ready,
		Warnings:            warnings,
		ProxyReady:          ready && dbOK,
	})
}

func (r *router) ccswitchProviders(w http.ResponseWriter, _ *http.Request) {
	providers := r.Proxy.ListProviders()
	debugProvider := r.Proxy.GetDebugProvider()
	items := make([]map[string]interface{}, 0, len(providers))
	for _, prov := range providers {
		isCurrent := prov.Name == debugProvider
		if debugProvider == "" {
			isCurrent = len(items) == 0
		}
		items = append(items, map[string]interface{}{
			"id":         prov.ID,
			"name":       prov.Name,
			"app_type":   "claude",
			"category":   "custom",
			"is_current": isCurrent,
			"icon_color": "#6366f1",
			"enabled":    prov.Enabled,
			"endpoints":  []map[string]string{{"app_type": "claude", "url": prov.BaseURL}},
		})
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": len(items)})
}

// ── Cache ──────────────────────────────────────────────────────────────

func (r *router) cacheStats(w http.ResponseWriter, _ *http.Request) {
	if r.SemanticCache == nil {
		WriteJSON(w, http.StatusOK, map[string]interface{}{"configured": false})
		return
	}
	stats, err := r.SemanticCache.Stats()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	count, _ := r.SemanticCache.Count()
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"configured":     true,
		"total_entries":  stats.TotalEntries,
		"hits_today":     stats.HitsToday,
		"misses_today":   stats.MissesToday,
		"hit_rate":       stats.HitRate,
		"current_entries": count,
	})
}

func (r *router) cacheClear(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "需要 POST 请求")
		return
	}
	if r.SemanticCache == nil {
		WriteError(w, http.StatusServiceUnavailable, "缓存服务未配置")
		return
	}
	if err := r.SemanticCache.Clear(); err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "缓存已清空"})
}

// ── Resilience ────────────────────────────────────────────────────────

func (r *router) resilienceStats(w http.ResponseWriter, _ *http.Request) {
	providers := r.Proxy.ListProviders()
	stats := make(map[string]interface{})
	for _, prov := range providers {
		if bs := r.Proxy.BreakerStats(prov.Name); bs != nil {
			stats[prov.Name] = map[string]interface{}{
				"state":            bs.State.String(),
				"total_failures":   bs.TotalFailures,
				"total_successes":  bs.TotalSuccesses,
				"window_failures":  bs.WindowFailures,
				"window_successes": bs.WindowSuccesses,
			}
		}
	}
	WriteJSON(w, http.StatusOK, stats)
}

func (r *router) routingStats(w http.ResponseWriter, _ *http.Request) {
	stats := r.RoutingEngine.Stats()
	total := stats.KeywordHits + stats.EmbeddingHits + stats.ClassifierHits + stats.FallbackHits
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"keyword_hits":    stats.KeywordHits,
		"embedding_hits":  stats.EmbeddingHits,
		"classifier_hits": stats.ClassifierHits,
		"fallback_hits":   stats.FallbackHits,
		"total":           total,
		"cache_size":      r.RoutingEngine.CacheSize(),
	})
}

// ── Intent ────────────────────────────────────────────────────────────

func (r *router) intentTree(w http.ResponseWriter, _ *http.Request) {
	records, err := r.IntentStore.ListAll()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	roots := store.ToTree(records)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"roots": roots})
}

func (r *router) intentClassifier(w http.ResponseWriter, _ *http.Request) {
	if r.ClassifierConfigured {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"configured":    true,
			"provider_name": getEnvDefault("CLASSIFIER_MODEL", "deepseek-chat"),
			"model":         getEnvDefault("CLASSIFIER_MODEL", "deepseek-chat"),
			"source":        "env",
		})
	} else {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"configured": false,
			"message":    "未配置 CLASSIFIER_API_KEY 环境变量，LLM 分类不可用",
		})
	}
}

func (r *router) intentDefaultProvider(w http.ResponseWriter, _ *http.Request) {
	for _, prov := range r.Providers {
		if prov.Name == r.DefaultProvider {
			WriteJSON(w, http.StatusOK, map[string]interface{}{
				"found": true, "id": prov.ID, "name": prov.Name,
			})
			return
		}
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"found": false})
}

func (r *router) intentClassify(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "需要 POST 请求")
		return
	}
	var body struct {
		Question   string `json:"question"`
		AutoSwitch bool   `json:"auto_switch"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Question == "" {
		WriteError(w, http.StatusBadRequest, "question 不能为空")
		return
	}

	result := r.RoutingEngine.Classify(req.Context(), body.Question)

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

	WriteJSON(w, http.StatusOK, result)
}

func (r *router) intentNodes(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		var node store.IntentNodeRecord
		if err := json.NewDecoder(req.Body).Decode(&node); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if node.Examples == "" {
			node.Examples = "[]"
		}
		if err := r.IntentStore.Insert(&node); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		r.ReloadIntents(r.RoutingEngine)
		WriteJSON(w, http.StatusCreated, map[string]interface{}{"success": true})
		return
	}
	WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (r *router) intentNodeByCode(w http.ResponseWriter, req *http.Request) {
	code := strings.TrimPrefix(req.URL.Path, "/api/intent/nodes/")
	if code == "" {
		WriteError(w, http.StatusBadRequest, "缺少 intent_code")
		return
	}

	switch req.Method {
	case http.MethodPatch:
		var node store.IntentNodeRecord
		if err := json.NewDecoder(req.Body).Decode(&node); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := r.IntentStore.Update(code, &node); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		r.ReloadIntents(r.RoutingEngine)
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true})
	case http.MethodDelete:
		if err := r.IntentStore.Delete(code); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		r.ReloadIntents(r.RoutingEngine)
		WriteJSON(w, http.StatusNoContent, nil)
	default:
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
