package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf

	DB struct {
		Type string
		DSN  string
	}

	Redis redis.RedisConf

	HealthCheck struct {
		Interval        int // 探测间隔（秒）
		Timeout         int // 探测超时（秒）
		FailThreshold   int // 连续失败 → 禁用
		RecoverThreshold int // 连续成功 → 恢复
	}
}
