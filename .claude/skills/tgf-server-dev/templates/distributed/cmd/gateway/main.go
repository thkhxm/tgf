// {{PROJECT_NAME}} —— 网关进程（gateway）
//
// 职责：终结客户端 TCP/WS/KCP 连接，把客户端帧路由到后端业务进程（经 rpcx + Consul），
// 并在 Redis 维护 用户→节点 映射。一个集群可水平扩多个网关实例。
//
// 依赖：Consul（服务发现）+ Redis（用户路由表）。本地一键起依赖：
//   docker compose -f deploy/docker-compose.yml up -d consul redis
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/rpc"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}

	done := rpc.NewRPCServer().
		// 网关需要 Redis 维护用户→节点映射（RedisAddr 在 .env.<module> 配置）
		WithCache(tgf.CacheModuleRedis).
		// 客户端入口：TCP 8082；要 WebSocket 加 WSPath；实时同步场景加 KCP：
		//   KCP: rpc.NewKCPBuilder("8300").WithAEADKey(key32)  // key32 为 32 字节预共享密钥
		WithGatewayOptions(rpc.GatewayOptions{
			TCPPort: "8082",
		}).
		// 登录鉴权默认开启（fail-closed）：密钥读 .env 的 LoginTokenSecret。
		// 仅内网联调可显式 WithoutLoginCheck()——生产严禁。
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  {{PROJECT_NAME}} gateway 已启动")
	fmt.Println("  TCP Gateway : localhost:8082")
	fmt.Printf("  Consul      : %s（path=%s）\n", cfg.Consul.Address, cfg.Consul.Path)
	fmt.Printf("  Redis       : %s\n", cfg.Redis.Addr)
	fmt.Println("========================================")

	// 优雅停机：SIGTERM/SIGINT → Consul 摘除 → 停 accept → drain → 终末落库。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		fmt.Println("gateway 退出")
	case s := <-sig:
		fmt.Printf("收到信号 %v，正在优雅关闭...\n", s)
	}
}
