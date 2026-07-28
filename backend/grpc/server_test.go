package grpc

import (
	"context"
	"testing"
	"time"

	pb "github.com/ragent/router/grpc/proto"
	"google.golang.org/grpc"
)

// =============================================================================
// GatewayServer 单元测试
// =============================================================================

func TestGatewayServer_GetStatus(t *testing.T) {
	srv := NewGatewayServer(50051)
	srv.SetProviderCount(3)

	resp, err := srv.GetStatus(context.Background(), &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus 错误: %v", err)
	}

	if resp.Version != "0.3.0" {
		t.Errorf("Version: want=0.3.0, got=%s", resp.Version)
	}
	if resp.ProviderCount != 3 {
		t.Errorf("ProviderCount: want=3, got=%d", resp.ProviderCount)
	}
	if !resp.Healthy {
		t.Error("Healthy 应为 true")
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("UptimeSeconds 应 >= 0, got=%d", resp.UptimeSeconds)
	}
}

func TestGatewayServer_GetStatus_ActiveRequests(t *testing.T) {
	srv := NewGatewayServer(50051)

	// 初始无活跃请求
	resp, _ := srv.GetStatus(context.Background(), &pb.StatusRequest{})
	if resp.ActiveRequests != 0 {
		t.Errorf("初始 ActiveRequests: want=0, got=%d", resp.ActiveRequests)
	}
	if resp.TotalRequests != 0 {
		t.Errorf("初始 TotalRequests: want=0, got=%d", resp.TotalRequests)
	}

	// 模拟请求
	srv.IncrActiveRequests()
	srv.IncrActiveRequests()

	resp, _ = srv.GetStatus(context.Background(), &pb.StatusRequest{})
	if resp.ActiveRequests != 2 {
		t.Errorf("ActiveRequests: want=2, got=%d", resp.ActiveRequests)
	}
	if resp.TotalRequests != 2 {
		t.Errorf("TotalRequests: want=2, got=%d", resp.TotalRequests)
	}

	// 请求完成
	srv.DecrActiveRequests()
	resp, _ = srv.GetStatus(context.Background(), &pb.StatusRequest{})
	if resp.ActiveRequests != 1 {
		t.Errorf("ActiveRequests: want=1, got=%d", resp.ActiveRequests)
	}
}

func TestGatewayServer_ClassifyIntent_NoClassifier(t *testing.T) {
	srv := NewGatewayServer(50051)

	resp, err := srv.ClassifyIntent(context.Background(), &pb.ClassifyRequest{
		Prompt: "hello",
		Model:  "test",
	})
	if err != nil {
		t.Fatalf("ClassifyIntent 错误: %v", err)
	}

	// 无分类器时应返回 fallback
	if resp.Strategy != "fallback" {
		t.Errorf("Strategy: want=fallback, got=%s", resp.Strategy)
	}
	if resp.MatchedProvider != "unknown" {
		t.Errorf("MatchedProvider: want=unknown, got=%s", resp.MatchedProvider)
	}
}

// mockClassifier 用于测试的 mock 分类器。
type mockClassifier struct {
	provider   string
	strategy   string
	confidence float64
	latencyMs  int64
}

func (m *mockClassifier) Classify(ctx context.Context, prompt string, model string) (string, string, float64, int64) {
	return m.provider, m.strategy, m.confidence, m.latencyMs
}

func TestGatewayServer_ClassifyIntent_WithClassifier(t *testing.T) {
	srv := NewGatewayServer(50051)
	srv.SetClassifier(&mockClassifier{
		provider:   "DeepSeek",
		strategy:   "keyword",
		confidence: 0.95,
		latencyMs:  5,
	})

	resp, err := srv.ClassifyIntent(context.Background(), &pb.ClassifyRequest{
		Prompt: "帮我写代码",
		Model:  "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("ClassifyIntent 错误: %v", err)
	}

	if resp.MatchedProvider != "DeepSeek" {
		t.Errorf("MatchedProvider: want=DeepSeek, got=%s", resp.MatchedProvider)
	}
	if resp.Strategy != "keyword" {
		t.Errorf("Strategy: want=keyword, got=%s", resp.Strategy)
	}
	if resp.Confidence != 0.95 {
		t.Errorf("Confidence: want=0.95, got=%f", resp.Confidence)
	}
	if resp.LatencyMs != 5 {
		t.Errorf("LatencyMs: want=5, got=%d", resp.LatencyMs)
	}
}

// =============================================================================
// 拦截器测试
// =============================================================================

func TestLoggingUnaryInterceptor(t *testing.T) {
	interceptor := LoggingUnaryInterceptor()

	// 模拟 handler
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "response", nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/ragent.grpc.GatewayService/GetStatus",
	}

	resp, err := interceptor(context.Background(), nil, info, handler)
	if err != nil {
		t.Fatalf("拦截器错误: %v", err)
	}
	if resp != "response" {
		t.Errorf("响应: want=response, got=%v", resp)
	}
}

// =============================================================================
// 集成测试（需要启动真实 gRPC 服务器）
// =============================================================================

func TestGatewayServer_Integration(t *testing.T) {
	// 启动服务器
	srv := NewGatewayServer(0) // 端口 0 让系统自动分配
	srv.SetProviderCount(2)

	// 在 goroutine 中启动服务器
	lis := createTestListener(t)
	go func() {
		grpcServer := createTestGRPCServer()
		pb.RegisterGatewayServiceServer(grpcServer, srv)
		grpcServer.Serve(lis)
	}()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)

	// 创建客户端连接
	conn, err := createTestClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	client := pb.NewGatewayServiceClient(conn)

	// 测试 GetStatus
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	status, err := client.GetStatus(ctx, &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus RPC 错误: %v", err)
	}
	if status.Version != "0.3.0" {
		t.Errorf("Version: want=0.3.0, got=%s", status.Version)
	}
	if status.ProviderCount != 2 {
		t.Errorf("ProviderCount: want=2, got=%d", status.ProviderCount)
	}

	// 测试 ClassifyIntent
	classify, err := client.ClassifyIntent(ctx, &pb.ClassifyRequest{
		Prompt: "hello",
		Model:  "test",
	})
	if err != nil {
		t.Fatalf("ClassifyIntent RPC 错误: %v", err)
	}
	if classify.Strategy != "fallback" {
		t.Errorf("Strategy: want=fallback, got=%s", classify.Strategy)
	}
}
