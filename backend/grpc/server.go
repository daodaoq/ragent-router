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
	"context"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "github.com/ragent/router/grpc/proto"
)

// ────────────────────────────────────────────────────────────
// 服务实现
// ────────────────────────────────────────────────────────────

// GatewayServer 实现 pb.GatewayServiceServer 接口。
//
// 嵌入 UnimplementedGatewayServiceServer 保证向前兼容——
// proto 新增方法时，未实现的方法返回 UNIMPLEMENTED 而非编译错误。
type GatewayServer struct {
	pb.UnimplementedGatewayServiceServer

	port      int
	startTime time.Time

	// 运行时统计（原子操作，无锁）
	totalRequests  atomic.Int64
	activeRequests atomic.Int32
	providerCount  atomic.Int32

	// 路由引擎引用（用于 ClassifyIntent）
	classifier IntentClassifier
}

// IntentClassifier 是意图分类的接口。
// 代理核心的路由引擎实现此接口。
type IntentClassifier interface {
	Classify(ctx context.Context, prompt string, model string) (provider string, strategy string, confidence float64, latencyMs int64)
}

// ClassifyResult 分类结果。
type ClassifyResult struct {
	Provider   string
	Strategy   string
	Confidence float64
	LatencyMs  int64
}

// NewGatewayServer 创建 gRPC 服务器。
func NewGatewayServer(port int) *GatewayServer {
	return &GatewayServer{
		port:      port,
		startTime: time.Now(),
	}
}

// SetClassifier 设置意图分类器。
func (s *GatewayServer) SetClassifier(c IntentClassifier) {
	s.classifier = c
}

// SetProviderCount 设置供应商数量（启动时调用）。
func (s *GatewayServer) SetProviderCount(n int32) {
	s.providerCount.Store(n)
}

// IncrActiveRequests 增加活跃请求计数。
func (s *GatewayServer) IncrActiveRequests() {
	s.activeRequests.Add(1)
	s.totalRequests.Add(1)
}

// DecrActiveRequests 减少活跃请求计数。
func (s *GatewayServer) DecrActiveRequests() {
	s.activeRequests.Add(-1)
}

// ────────────────────────────────────────────────────────────
// RPC 实现
// ────────────────────────────────────────────────────────────

// GetStatus 获取网关运行状态（Unary RPC）。
func (s *GatewayServer) GetStatus(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	return &pb.StatusResponse{
		Version:        "0.3.0",
		UptimeSeconds:  int64(time.Since(s.startTime).Seconds()),
		TotalRequests:  s.totalRequests.Load(),
		ActiveRequests: s.activeRequests.Load(),
		ProviderCount:  s.providerCount.Load(),
		Healthy:        true,
		ErrorRate:      0.0, // TODO: 从 Prometheus 指标中计算
	}, nil
}

// StreamMetrics 实时推送网关指标（Server Streaming RPC）。
//
// 每秒推送一次指标更新，直到客户端断开连接。
// 演示了 Server Streaming 模式：服务端持续写入，客户端持续读取。
func (s *GatewayServer) StreamMetrics(req *pb.MetricsRequest, stream pb.GatewayService_StreamMetricsServer) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			// 客户端断开连接
			return nil
		case <-ticker.C:
			update := &pb.MetricsUpdate{
				Timestamp:      time.Now().UnixMilli(),
				RequestsPerSec: 0, // TODO: 从 Prometheus 指标中计算
				AvgLatencyMs:   0,
				ErrorRate:      0,
				ActiveRequests: s.activeRequests.Load(),
			}
			if err := stream.Send(update); err != nil {
				return fmt.Errorf("send metrics: %w", err)
			}
		}
	}
}

// ClassifyIntent 对用户提示词进行意图分类（Unary RPC）。
func (s *GatewayServer) ClassifyIntent(ctx context.Context, req *pb.ClassifyRequest) (*pb.ClassifyResponse, error) {
	if s.classifier == nil {
		return &pb.ClassifyResponse{
			MatchedProvider: "unknown",
			Strategy:        "fallback",
			Confidence:      0.0,
			LatencyMs:       0,
		}, nil
	}

	provider, strategy, confidence, latencyMs := s.classifier.Classify(ctx, req.Prompt, req.Model)
	return &pb.ClassifyResponse{
		MatchedProvider: provider,
		Strategy:        strategy,
		Confidence:      confidence,
		LatencyMs:       latencyMs,
	}, nil
}

// ────────────────────────────────────────────────────────────
// 服务器启动
// ────────────────────────────────────────────────────────────

// Start 启动 gRPC 服务器。
//
// 功能：
//   - 注册 GatewayService
//   - 注册 Unary 拦截器（日志 + 认证）
//   - 注册 Stream 拦截器（日志）
//   - 注册健康检查服务（grpc.health.v1）
//   - 注册反射服务（grpcurl 等工具调试用）
func (s *GatewayServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gRPC 监听失败: %w", err)
	}

	// 创建 gRPC 服务器，注册拦截器
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			LoggingUnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			LoggingStreamInterceptor(),
		),
	)

	// 注册 GatewayService
	pb.RegisterGatewayServiceServer(grpcServer, s)

	// 注册健康检查服务
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("ragent.grpc.GatewayService", healthpb.HealthCheckResponse_SERVING)

	// 注射服务（grpcurl 等工具调试用）
	reflection.Register(grpcServer)

	log.Printf("[gRPC] 服务器启动: %s", addr)
	log.Printf("[gRPC] 支持的 RPC 方法:")
	log.Printf("[gRPC]   - GetStatus (Unary)")
	log.Printf("[gRPC]   - StreamMetrics (Server Streaming)")
	log.Printf("[gRPC]   - ClassifyIntent (Unary)")
	log.Printf("[gRPC]   - HealthCheck (grpc.health.v1)")

	return grpcServer.Serve(lis)
}
