# gRPC 内部服务通信

## 面试考点

1. **gRPC vs REST？** gRPC 基于 HTTP/2 + Protobuf，二进制序列化更高效，支持流式 RPC
2. **四种通信模式？** Unary / Server Streaming / Client Streaming / Bidirectional
3. **Protobuf 编码原理？** Varint + ZigZag + Tag-Length-Value
4. **gRPC 拦截器？** 类似中间件，支持链式调用
5. **服务发现？** 与 Consul/etcd 集成

## Proto 定义

```protobuf
syntax = "proto3";
package ragent;

service GatewayService {
    // 一元 RPC：获取网关状态
    rpc GetStatus(StatusRequest) returns (StatusResponse);

    // 服务端流式 RPC：实时推送指标
    rpc StreamMetrics(StreamMetricsRequest) returns (stream MetricsUpdate);

    // 一元 RPC：获取路由决策
    rpc ClassifyIntent(ClassifyRequest) returns (ClassifyResponse);
}
```

## 当前实现

由于项目是单体架构，gRPC 服务主要用于展示能力：
- `grpc/server.go` — gRPC 服务器实现
- 与 HTTP 服务器并行运行（不同端口）
- 支持 Unary RPC 和 Server Streaming RPC
