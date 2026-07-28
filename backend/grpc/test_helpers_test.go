package grpc

import (
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// createTestListener 创建用于测试的 TCP 监听器。
func createTestListener(t *testing.T) net.Listener {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建监听器失败: %v", err)
	}
	return lis
}

// createTestGRPCServer 创建用于测试的 gRPC 服务器。
func createTestGRPCServer() *grpc.Server {
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(LoggingUnaryInterceptor()),
	)
}

// createTestClient 创建用于测试的 gRPC 客户端连接。
func createTestClient(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}
