package logic

import (
	"context"
	"io"
	"net/http"

	"github.com/ragent/router/rpc/proxy"
	"github.com/ragent/router/rpc/router"
	"github.com/ragent/router/services/api/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
)

type MessagesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMessagesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MessagesLogic {
	return &MessagesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// Handle 处理消息请求。
func (l *MessagesLogic) Handle(w http.ResponseWriter, r *http.Request) {
	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// ── 调用 Router RPC ──
	classifyResp, err := l.svcCtx.RouterRPC.Classify(l.ctx, &router.ClassifyReq{
		Prompt: string(bodyBytes),
		Model:  r.Header.Get("X-Model"),
	})
	if err != nil {
		l.Errorf("[消息] 路由失败: %v", err)
		http.Error(w, `{"error":"routing failed"}`, http.StatusInternalServerError)
		return
	}

	l.Infof("[消息] 路由到 %s (策略=%s)", classifyResp.ProviderName, classifyResp.Strategy)

	// ── 调用 Proxy RPC ──
	forwardResp, err := l.svcCtx.ProxyRPC.Forward(l.ctx, &proxy.ForwardReq{
		ProviderName: classifyResp.ProviderName,
		Model:        r.Header.Get("X-Model"),
		RequestBody:  bodyBytes,
	})
	if err != nil {
		l.Errorf("[消息] 转发失败: %v", err)
		http.Error(w, `{"error":"forward failed"}`, http.StatusBadGateway)
		return
	}

	// ── 返回 ──
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Ragent-Provider", classifyResp.ProviderName)
	w.WriteHeader(int(forwardResp.StatusCode))
	w.Write(forwardResp.ResponseBody)
}
