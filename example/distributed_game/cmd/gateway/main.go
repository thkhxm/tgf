// gateway 进程同时提供 TCP / WebSocket / KCP 入口，通过 Consul
// 发现 game 进程，并用 Redis 维护用户节点路由。
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/rpc"
)

const kcpKeyEnv = "TGF_EXAMPLE_KCP_AEAD_KEY_HEX"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway configuration error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	kcpKey, err := loadKCPAEADKey()
	if err != nil {
		return err
	}

	done := rpc.NewRPCServer().
		WithCache(tgf.CacheModuleRedis).
		WithGatewayOptions(rpc.GatewayOptions{
			TCPPort: "8082",
			WSPath:  "/ws",
			KCP:     rpc.NewKCPBuilder("8300").WithAEADKey(kcpKey),
		}).
		Run()
	fmt.Println("distributed gateway started: tcp=:8082 ws=/ws kcp=:8300")
	<-done
	return nil
}

func loadKCPAEADKey() ([]byte, error) {
	raw := os.Getenv(kcpKeyEnv)
	if raw == "" {
		return nil, fmt.Errorf("%s must contain a 64-character hex key", kcpKeyEnv)
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to exactly 32 bytes", kcpKeyEnv)
	}
	return key, nil
}
