package svc

import (
	"github.com/ragent/router/services/router/internal/config"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type ServiceContext struct {
	Config config.Config
	Redis  *redis.Redis
}

func NewServiceContext(c config.Config) *ServiceContext {
	rds := redis.MustNewRedis(c.Redis)
	return &ServiceContext{
		Config: c,
		Redis:  rds,
	}
}
