package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf

	Redis redis.RedisConf

	RoutingCache struct {
		TTL     int // 路由结果缓存秒数
		MaxSize int // 最大缓存条目
	}
}
