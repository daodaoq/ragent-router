package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	rest.RestConf

	Auth struct {
		AccessSecret string
		AccessExpire int64
	}

	Redis redis.RedisConf

	RateLimit struct {
		Global struct {
			Rate  float64
			Burst int
		}
		PerUser struct {
			Rate  float64
			Burst int
		}
		PerProvider struct {
			Rate  float64
			Burst int
		}
	}

	// 下游 RPC 服务发现
	ProviderRPC zrpc.RpcClientConf
	RouterRPC   zrpc.RpcClientConf
	ProxyRPC    zrpc.RpcClientConf
}
