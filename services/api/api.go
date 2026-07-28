package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/ragent/router/services/api/internal/config"
	"github.com/ragent/router/services/api/handlers"
	"github.com/ragent/router/services/api/internal/svc"
	"github.com/ragent/router/shared/redis"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	// 初始化 Redis（分布式限流）
	redis.Init()

	// 创建服务上下文
	svcCtx := svc.NewServiceContext(c)

	// 创建 HTTP 服务器
	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	// 注册路由
	msgHandler := handlers.NewMessagesHandler(svcCtx)
	server.AddRoute(rest.Route{
		Method:  http.MethodPost,
		Path:    "/v1/messages",
		Handler: msgHandler.ServeHTTP,
	})

	// 健康检查
	server.AddRoute(rest.Route{
		Method: http.MethodGet,
		Path:   "/healthz",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"ok","service":"api"}`))
		},
	})

	fmt.Printf("[API 网关] 启动: %s:%d\n", c.Host, c.Port)
	server.Start()
}
