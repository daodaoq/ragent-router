package svc

import (
	"sync"

	"github.com/ragent/router/services/proxy/internal/config"
	"github.com/ragent/router/shared/mq"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type ServiceContext struct {
	Config   config.Config
	Redis    *redis.Redis
	Breakers sync.Map // provider_name → *circuitbreaker.CircuitBreaker
}

func NewServiceContext(c config.Config) *ServiceContext {
	rds := redis.MustNewRedis(c.Redis)

	// 初始化 RocketMQ Producer（如果配置了）
	mqTopic := c.RocketMQ.Topic
	if mqTopic == "" {
		mqTopic = mq.TopicRequestLog
	}
	mqGroup := c.RocketMQ.Group
	if mqGroup == "" {
		mqGroup = "ragent_proxy_producer"
	}
	mq.InitGlobalProducer(c.RocketMQ.NameServer, mqTopic, mqGroup)

	return &ServiceContext{
		Config: c,
		Redis:  rds,
	}
}
