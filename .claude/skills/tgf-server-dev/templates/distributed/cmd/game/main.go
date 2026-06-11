// {{PROJECT_NAME}} —— 业务进程（game）
//
// 职责：承载业务 Module（user 等），注册进 Consul 供网关/其他服务发现。
// 同一台机器可起多个实例（WithRandomServicePort 自动避开端口冲突），
// 跨 module 调用统一走 rpc.SendRPCMessage（描述符见 internal/api）。
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/rpc"

	"{{PROJECT_NAME}}/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}

	done := rpc.NewRPCServer().
		// 业务数据走 Redis 热层；要加 MySQL write-behind 冷层见 README《数据层》
		WithCache(tgf.CacheModuleRedis).
		// 同机多实例随机端口（rpcx 内部端口），与网关 .env 的 ServicePort=8081 错开
		WithRandomServicePort(20000, 20100).
		WithService(service.NewUserService()).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  {{PROJECT_NAME}} game 已启动")
	fmt.Printf("  Consul  : %s（path=%s）\n", cfg.Consul.Address, cfg.Consul.Path)
	fmt.Println("  Modules : user")
	fmt.Println("========================================")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		fmt.Println("game 退出")
	case s := <-sig:
		fmt.Printf("收到信号 %v，正在优雅关闭...\n", s)
	}
}
