package svc

import (
	"github.com/ragent/router/services/provider/internal/config"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type ServiceContext struct {
	Config    config.Config
	Redis     *redis.Redis
	// DB 和其他依赖在这里注入
}

func NewServiceContext(c config.Config) *ServiceContext {
	rds := redis.MustNewRedis(c.Redis)
	return &ServiceContext{
		Config: c,
		Redis:  rds,
	}
}
