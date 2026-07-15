// game 进程注册 user 业务服务，通过 Consul 被网关发现。
package main

import (
	"fmt"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/example/distributed_game/internal/service"
	"github.com/thkhxm/tgf/v2/rpc"
)

func main() {
	done := rpc.NewRPCServer().
		WithCache(tgf.CacheModuleClose).
		WithService(service.NewUserService()).
		Run()
	fmt.Println("distributed game service started: module=user")
	<-done
}
