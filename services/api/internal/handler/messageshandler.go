package handler

import (
	"io"
	"net/http"

	"github.com/ragent/router/rpc/proxy"
	"github.com/ragent/router/rpc/router"
	"github.com/ragent/router/services/api/internal/logic"
	"github.com/ragent/router/services/api/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

// MessagesHandler 处理 /v1/messages 请求。
//
// 完整流程：
//
//	客户端 → API 网关（限流+认证）→ Router RPC（路由分类）
//	→ Proxy RPC（韧性转发）→ 上游 LLM → SSE 流式返回
//
// 高并发方案 5：go-zero P2C 负载均衡自动选择下游实例。
type MessagesHandler struct {
	svcCtx *svc.ServiceContext
}

func NewMessagesHandler(svcCtx *svc.ServiceContext) *MessagesHandler {
	return &MessagesHandler{svcCtx: svcCtx}
}

func (h *MessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	logger := logx.WithContext(ctx)

	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// ── 步骤 1: 调用 Router RPC 获取路由 ──
	classifyResp, err := h.svcCtx.RouterRPC.Classify(ctx, &router.ClassifyReq{
		Prompt: string(bodyBytes),
		Model:  r.Header.Get("X-Model"),
	})
	if err != nil {
		logger.Errorf("[网关] 路由分类失败: %v", err)
		http.Error(w, `{"error":"routing failed"}`, http.StatusInternalServerError)
		return
	}
	logger.Infof("[网关] 路由: %s (策略=%s, 置信度=%.2f, 耗时=%dms)",
		classifyResp.ProviderName, classifyResp.Strategy,
		classifyResp.Confidence, classifyResp.LatencyMs)

	// ── 步骤 2: 调用 Proxy RPC 转发请求 ──
	// go-zero P2C 自动选择负载最低的 proxy 实例
	forwardResp, err := h.svcCtx.ProxyRPC.Forward(ctx, &proxy.ForwardReq{
		ProviderName: classifyResp.ProviderName,
		Model:        r.Header.Get("X-Model"),
		RequestBody:  bodyBytes,
		Headers:      extractHeaders(r),
	})
	if err != nil {
		logger.Errorf("[网关] 转发失败: %v", err)
		http.Error(w, `{"error":"proxy failed"}`, http.StatusBadGateway)
		return
	}

	// ── 步骤 3: 返回响应 ──
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Ragent-Provider", classifyResp.ProviderName)
	w.Header().Set("X-Ragent-Strategy", classifyResp.Strategy)
	w.WriteHeader(int(forwardResp.StatusCode))
	w.Write(forwardResp.ResponseBody)
}

func extractHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string)
	for _, key := range []string{"Authorization", "X-Api-Key", "Content-Type"} {
		if v := r.Header.Get(key); v != "" {
			headers[key] = v
		}
	}
	return headers
}

// MessagesHandlerWithLogic 使用 logic 层的完整实现。
func MessagesHandlerWithLogic(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 调用 logic 层
		l := logic.NewMessagesLogic(r.Context(), svcCtx)
		l.Handle(w, r)
	}
}
