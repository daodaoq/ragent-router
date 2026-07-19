package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Engine 是多模型编排的执行引擎。
// 通过 UpstreamCaller 接口解耦对上游 API 的依赖，便于单元测试。
type Engine struct {
	caller UpstreamCaller
}

// New 创建编排引擎。
func New(caller UpstreamCaller) *Engine {
	return &Engine{caller: caller}
}

// Execute 执行编排请求并以 SSE 流式写回。
func (e *Engine) Execute(ctx context.Context, w http.ResponseWriter, req *Request) error {
	switch req.Strategy {
	case StrategyReview:
		return e.executeReview(ctx, w, req)
	default:
		return fmt.Errorf("unknown strategy: %s", req.Strategy)
	}
}

// writePhase 写入 phase_change SSE 事件。
func writePhase(w io.Writer, phase Phase) {
	data, _ := json.Marshal(phase)
	fmt.Fprintf(w, "event: phase_change\ndata: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// estimateProviderCost 估算单次调用的费用。
func estimateProviderCost(providerName string, inputTokens, outputTokens int) float64 {
	rates := map[string]struct{ input, output float64 }{
		"deepseek": {0.27, 1.10},
		"claude":   {3.00, 15.00},
		"openai":   {2.50, 10.00},
		"minimax":  {0.30, 1.20},
		"bailian":  {0.40, 1.20},
	}
	name := strings.ToLower(providerName)
	for keyword, r := range rates {
		if strings.Contains(name, keyword) {
			return (float64(inputTokens)*r.input + float64(outputTokens)*r.output) / 1_000_000
		}
	}
	return 0
}
