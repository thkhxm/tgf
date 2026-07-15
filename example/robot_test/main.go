// tgf v2 示例：robot 在本机单进程服务上验证 TCP / WebSocket / KCP。
//
// 运行：cd tgf/example/robot_test && go run .
// 本示例不依赖 Consul / Redis / MySQL；它使用公开的本地测试密钥。
// 生产 KCP 必须换成由密钥管理系统分发的 32 字节预共享密钥。
package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"context"
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/robot"
	"github.com/thkhxm/tgf/v2/rpc"
	"github.com/thkhxm/tgf/v2/util"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	gameModule    = "game"
	getRoleMethod = "GetRole"
)

var localSelfTestAEADKey = []byte("tgf-local-robot-self-test-key-32")

// GameService 使用网关可达的 rpc.Args/rpc.Reply 签名。
type GameService struct {
	rpc.Module
	requests atomic.Int64
}

func NewGameService() *GameService {
	return &GameService{Module: rpc.Module{Name: gameModule, Version: "1.0"}}
}

func (g *GameService) Startup() (bool, error) { return true, nil }

func (g *GameService) GetRole(_ context.Context, args *rpc.Args[*wrapperspb.StringValue], reply *rpc.Reply[*wrapperspb.StringValue]) error {
	g.requests.Add(1)
	userID := args.GetData().GetValue()
	if err := reply.SetData(wrapperspb.String("Hero_" + userID)); err != nil {
		return err
	}
	reply.SetCode(0)
	return nil
}

func main() {
	fmt.Println("=== tgf v2 robot 本地自测：TCP / WS / KCP ===")

	service := NewGameService()
	done := rpc.NewRPCServer().
		WithSingleProcess().
		WithCache(tgf.CacheModuleClose).
		WithWhiteService(gameModule + "." + getRoleMethod).
		WithGatewayOptions(rpc.GatewayOptions{
			TCPPort: "18090",
			WSPath:  "/ws",
			KCP:     rpc.NewKCPBuilder("18300").WithAEADKey(localSelfTestAEADKey),
		}).
		WithService(service).
		Run()

	time.Sleep(500 * time.Millisecond)
	results := []bool{
		probeRobot("TCP", robot.NewRobotTcp(), "127.0.0.1:18090"),
		probeRobot("WebSocket", robot.NewRobotWs("/ws"), "127.0.0.1:18090"),
		probeRobot("KCP", robot.NewRobotKCP(localSelfTestAEADKey), "127.0.0.1:18300"),
	}

	for i, ok := range results {
		if !ok {
			fmt.Printf("自测未全部通过（失败索引 %d）；请检查端口占用与本机防火墙。\n", i)
			break
		}
	}
	fmt.Printf("服务端共处理 %d 个请求。\n", service.requests.Load())
	fmt.Println("按 Ctrl+C 触发框架优雅停机。")

	// SIGINT/SIGTERM 由框架统一处理；业务 main 只等待 Run 的 done。
	<-done
}

func probeRobot(label string, client robot.IRobot, address string) bool {
	result := make(chan string, 1)
	client.RegisterCallbackMessage(gameModule+"."+getRoleMethod, func(_ robot.IRobot, data []byte) {
		role := new(wrapperspb.StringValue)
		if err := proto.Unmarshal(data, role); err != nil {
			result <- ""
			return
		}
		result <- role.Value
	})
	client.Connect(address)
	time.Sleep(150 * time.Millisecond)
	client.SendMessage(gameModule, getRoleMethod, wrapperspb.String("player_001"))

	select {
	case got := <-result:
		ok := got == "Hero_player_001"
		fmt.Printf("[%s] response=%q pass=%v\n", label, got, ok)
		return ok
	case <-time.After(2 * time.Second):
		fmt.Printf("[%s] timeout\n", label)
		return false
	}
}

func init() {
	util.InitGoroutinePool()
}
