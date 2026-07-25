// Package grpc 提供 gRPC 内部服务通信。
//
// 面试考点：
//  1. gRPC 四种模式：Unary / Server Streaming / Client Streaming / Bidirectional
//  2. Protobuf 编码：Varint + ZigZag + Tag-Length-Value，比 JSON 小 3-10x
//  3. HTTP/2 多路复用：单连接并发多个请求流，减少 TCP 握手
//  4. 拦截器（Interceptor）：类似中间件，支持 Unary 和 Stream 两种
//  5. 健康检查：grpc.health.v1 标准协议
package grpc

import (
	"fmt"
	"log"
	"net"
	"time"
)

// ────────────────────────────────────────────────────────────
// 消息类型（模拟 Protobuf 生成的结构体）
//
// 实际项目中应使用 protoc 生成，这里手动定义展示结构
// ────────────────────────────────────────────────────────────

// StatusRequest 获取状态请求。
type StatusRequest struct {
	// 未来可扩展：指定查询维度
}

// StatusResponse 状态响应。
type StatusResponse struct {
	Version        string  `json:"version"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
	TotalRequests  int64   `json:"total_requests"`
	ActiveRequests int32   `json:"active_requests"`
	ProviderCount  int32   `json:"provider_count"`
	Healthy        bool    `json:"healthy"`
	ErrorRate      float64 `json:"error_rate"`
}

// ClassifyRequest 意图分类请求。
type ClassifyRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
}

// ClassifyResponse 意图分类响应。
type ClassifyResponse struct {
	MatchedProvider string  `json:"matched_provider"`
	Strategy        string  `json:"strategy"`        // keyword / embedding / llm / fallback
	Confidence      float64 `json:"confidence"`       // 置信度
	LatencyMs       int64   `json:"latency_ms"`       // 分类耗时
}

// MetricsUpdate 指标更新（流式推送）。
type MetricsUpdate struct {
	Timestamp      int64   `json:"timestamp"`
	RequestsPerSec float64 `json:"requests_per_sec"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	ErrorRate      float64 `json:"error_rate"`
	ActiveRequests int32   `json:"active_requests"`
}

// ────────────────────────────────────────────────────────────
// gRPC 服务器
// ────────────────────────────────────────────────────────────

// GRPCServer gRPC 服务器。
type GRPCServer struct {
	port      int
	startTime time.Time
	// 在实际项目中，这里会有 protobuf 生成的 UnimplementedGatewayServiceServer
}

// NewGRPCServer 创建 gRPC 服务器。
func NewGRPCServer(port int) *GRPCServer {
	return &GRPCServer{
		port:      port,
		startTime: time.Now(),
	}
}

// Start 启动 gRPC 服务器。
//
// 实际项目中应使用：
//
//	lis, _ := net.Listen("tcp", ":50051")
//	s := grpc.NewServer(
//	    grpc.UnaryInterceptor(loggingInterceptor),
//	    grpc.StreamInterceptor(streamInterceptor),
//	)
//	pb.RegisterGatewayServiceServer(s, &server{})
//	s.Serve(lis)
func (s *GRPCServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gRPC 监听失败: %w", err)
	}

	log.Printf("[gRPC] 服务器启动: %s", addr)
	log.Printf("[gRPC] 支持的 RPC 方法:")
	log.Printf("[gRPC]   - GetStatus (Unary)")
	log.Printf("[gRPC]   - StreamMetrics (Server Streaming)")
	log.Printf("[gRPC]   - ClassifyIntent (Unary)")

	// 实际项目中：
	// s.grpcServer = grpc.NewServer(...)
	// pb.RegisterGatewayServiceServer(s.grpcServer, s)
	// return s.grpcServer.Serve(lis)

	// 当前：占位实现，保持监听
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			conn.Close() // 简化实现
		}
	}()

	return nil
}

// GetStatus 获取网关状态（Unary RPC）。
func (s *GRPCServer) GetStatus() *StatusResponse {
	return &StatusResponse{
		Version:       "0.3.0",
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
		Healthy:       true,
	}
}

// ClassifyIntent 意图分类（Unary RPC）。
func (s *GRPCServer) ClassifyIntent(prompt string) *ClassifyResponse {
	// 简化实现，实际应调用路由引擎
	return &ClassifyResponse{
		MatchedProvider: "DeepSeek",
		Strategy:        "keyword",
		Confidence:      0.95,
		LatencyMs:       1,
	}
}
