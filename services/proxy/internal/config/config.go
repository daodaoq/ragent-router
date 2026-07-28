package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf

	Redis redis.RedisConf

	Resilience struct {
		GlobalRateLimit  float64 // 全局 QPS
		MaxConcurrent    int     // 最大并发
		MaxRetries       int     // 最大重试
		RequestTimeout   int     // 请求超时秒数
		UpstreamTimeout  int     // 上游超时秒数
		FailureThreshold float64 // 熔断阈值
		OpenTimeout      int     // 熔断恢复秒数
	}
}
