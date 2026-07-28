package grpc

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ────────────────────────────────────────────────────────────
// Unary 拦截器
// ────────────────────────────────────────────────────────────

// LoggingUnaryInterceptor 记录每次 Unary RPC 的方法名、耗时和状态码。
//
// 拦截器执行顺序（ChainUnaryInterceptor）：
//
//	请求 → LoggingUnaryInterceptor → 实际处理函数 → LoggingUnaryInterceptor（记录）
//
// 类似 HTTP 中间件，但作用于 gRPC 层。
func LoggingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()

		// 调用实际的处理函数
		resp, err := handler(ctx, req)

		// 记录日志
		duration := time.Since(start)
		code := status.Code(err)
		log.Printf("[gRPC] Unary %s | %s | %v",
			info.FullMethod,
			code.String(),
			duration.Round(time.Microsecond),
		)

		return resp, err
	}
}

// AuthUnaryInterceptor 认证拦截器（示例）。
//
// 从 metadata 中提取 Bearer Token 并验证。
// 在生产环境中应替换为真实的 JWT 验证逻辑。
func AuthUnaryInterceptor(validTokens map[string]bool) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// 从 metadata 提取 authorization
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		token := authHeaders[0]
		// 去掉 "Bearer " 前缀
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		if !validTokens[token] {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		return handler(ctx, req)
	}
}

// ────────────────────────────────────────────────────────────
// Stream 拦截器
// ────────────────────────────────────────────────────────────

// LoggingStreamInterceptor 记录 Stream RPC 的开始和结束。
//
// Stream 拦截器与 Unary 拦截器的区别：
//   - Unary：每次请求调用一次拦截器
//   - Stream：在流建立时调用一次拦截器，handler 内部持续处理消息
//
// 因此 Stream 拦截器记录的是流的生命周期（开始→结束），
// 而非每个消息的处理时间。
func LoggingStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()

		log.Printf("[gRPC] Stream %s | 开始", info.FullMethod)

		// 调用实际的处理函数
		err := handler(srv, stream)

		duration := time.Since(start)
		code := status.Code(err)
		log.Printf("[gRPC] Stream %s | %s | %v",
			info.FullMethod,
			code.String(),
			duration.Round(time.Microsecond),
		)

		return err
	}
}
