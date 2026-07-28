package svc

import (
	providerRpc "github.com/ragent/router/rpc/provider"
	routerRpc "github.com/ragent/router/rpc/router"
	proxyRpc "github.com/ragent/router/rpc/proxy"
	"github.com/ragent/router/services/api/internal/config"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config   config.Config
	Redis    *redis.Redis

	// 下游 RPC 客户端（go-zero 服务发现 + P2C 负载均衡）
	ProviderRPC providerRpc.ProviderServiceClient
	RouterRPC   routerRpc.RouterServiceClient
	ProxyRPC    proxyRpc.ProxyServiceClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	rds := redis.MustNewRedis(c.Redis)

	// go-zero 自动服务发现 + P2C 负载均衡
	// 高并发方案 5：Power of Two Choices 算法
	providerConn := zrpc.MustNewClient(c.ProviderRPC)
	routerConn := zrpc.MustNewClient(c.RouterRPC)
	proxyConn := zrpc.MustNewClient(c.ProxyRPC)

	return &ServiceContext{
		Config:      c,
		Redis:       rds,
		ProviderRPC: providerRpc.NewProviderServiceClient(providerConn.Conn()),
		RouterRPC:   routerRpc.NewRouterServiceClient(routerConn.Conn()),
		ProxyRPC:    proxyRpc.NewProxyServiceClient(proxyConn.Conn()),
	}
}
