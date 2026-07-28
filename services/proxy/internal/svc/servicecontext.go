package svc

import (
	"sync"

	"github.com/ragent/router/services/proxy/internal/config"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type ServiceContext struct {
	Config   config.Config
	Redis    *redis.Redis
	Breakers sync.Map // provider_name → *circuitbreaker.CircuitBreaker
}

func NewServiceContext(c config.Config) *ServiceContext {
	rds := redis.MustNewRedis(c.Redis)
	return &ServiceContext{
		Config: c,
		Redis:  rds,
	}
}
