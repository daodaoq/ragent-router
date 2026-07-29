package main

import (
	"flag"
	"fmt"

	"github.com/ragent/router/services/proxy/internal/config"
	"github.com/ragent/router/services/proxy/internal/svc"
	"github.com/ragent/router/shared/mq"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/proxy.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	svcCtx := svc.NewServiceContext(c)
	_ = svcCtx

	// 优雅退出时关闭 RocketMQ Producer
	defer mq.CloseGlobalProducer()

	// 启动 gRPC 服务器
	server := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		// 注册 RPC 服务
	})

	fmt.Printf("[Proxy 服务] 启动: %s\n", c.ListenOn)
	server.Start()
}
