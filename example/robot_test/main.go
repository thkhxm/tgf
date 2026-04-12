// tgf v2 示例：robot 自测 — KCP 移动同步 + WS 请求响应
//
// 本示例在单进程模式下启动一个游戏 server，然后分别用 WS robot 和 KCP robot
// 连入进行自动化测试：
//
//   场景 1（WS）：WS robot 登录 → 查询角色信息 → 验证响应
//   场景 2（KCP）：多个 KCP robot 并发发送移动位置 → 服务端打印广播
//
// 注意：KCP 接收服务端推送目前有协议格式限制（A8 遗留的请求/响应格式不一致），
// 本示例只演示 KCP 发送路径。完整的双向 KCP 通信需要后续修复服务端的推送格式。
//
// 运行：cd tgf/example/robot_test && go run .
package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/robot"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/util"
	"go.uber.org/zap"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/proto"
)

// ====================================================================
// 1. 游戏 Service 定义
// ====================================================================

// --- GameService：负责角色查询和移动处理 ---

type GameService struct {
	rpc.Module
	moveCount atomic.Int64
}

func NewGameService() *GameService {
	return &GameService{Module: rpc.Module{Name: "game", Version: "1.0"}}
}

func (g *GameService) Startup() (bool, error) {
	log.InfoTagW("game", "GameService 启动")
	return true, nil
}

// --- RPC 请求/响应结构 ---

// GetRoleReq 角色查询请求
type GetRoleReq struct {
	UserId string
}

// GetRoleRes 角色查询响应
type GetRoleRes struct {
	Name  string
	Level int32
}

// MoveReq 移动请求（KCP 高频场景）
type MoveReq struct {
	PlayerId string
	X        float32
	Y        float32
	Z        float32
	Tick     int64
}

// MoveRes 移动响应（确认）
type MoveRes struct {
	OK bool
}

// GetRole 角色查询 RPC handler
func (g *GameService) GetRole(ctx context.Context, req *GetRoleReq, reply *GetRoleRes) error {
	log.InfoTagW("game", "查询角色",
		zap.String("userId", req.UserId),
	)
	reply.Name = "Hero_" + req.UserId
	reply.Level = 42
	return nil
}

// Move 移动处理 RPC handler — 高频调用，适合 KCP
func (g *GameService) Move(ctx context.Context, req *MoveReq, reply *MoveRes) error {
	count := g.moveCount.Add(1)
	if count%100 == 0 {
		log.InfoTagW("game", "移动累计",
			zap.Int64("count", count),
			zap.String("lastPlayer", req.PlayerId),
			zap.Float32("x", req.X),
			zap.Float32("y", req.Y),
		)
	}
	reply.OK = true
	return nil
}

// --- ServiceAPI 定义 ---

var GetRoleAPI = &rpc.ServiceAPI[*GetRoleReq, *GetRoleRes]{
	ModuleName: "game", Name: "GetRole", MessageType: "game.GetRole",
}

var MoveAPI = &rpc.ServiceAPI[*MoveReq, *MoveRes]{
	ModuleName: "game", Name: "Move", MessageType: "game.Move",
}

// ====================================================================
// 2. main
// ====================================================================

func main() {
	fmt.Println("=== tgf v2 示例：robot 自测 ===")
	fmt.Println()

	gameSvc := NewGameService()

	// --- 启动 server（单进程模式 + TCP 网关 + WS 网关） ---
	done := rpc.NewRPCServer().
		WithSingleProcess().
		WithService(gameSvc).
		WithGatewayOptions(rpc.GatewayOptions{
			TCPPort: "18090",
			WSPath:  "/ws",
		}).
		Run()
	_ = done

	time.Sleep(500 * time.Millisecond)
	fmt.Println()

	// ================================================================
	// 场景 1：WS robot — 登录 + 查询角色
	// ================================================================
	fmt.Println("========== 场景 1: WS Robot ==========")
	fmt.Println()

	wsRobot := robot.NewRobotWs("/ws")

	// 注册回调
	var wsResponseReceived atomic.Bool
	wsRobot.RegisterCallbackMessage("game.GetRole", func(r robot.IRobot, data []byte) {
		// 响应数据是 proto 编码的 GetRoleRes
		fmt.Printf("  [WS 回调] 收到 GetRole 响应: %d bytes\n", len(data))
		wsResponseReceived.Store(true)
	})

	wsRobot.Connect("127.0.0.1:18090")
	time.Sleep(200 * time.Millisecond)

	// 发送查询请求
	fmt.Println("  [WS] 发送 GetRole 请求...")
	wsRobot.SendMessage("game", "GetRole", mustMarshalAsWSData(&GetRoleReq{UserId: "ws_player_001"}))

	time.Sleep(500 * time.Millisecond)
	if wsResponseReceived.Load() {
		fmt.Println("  [WS] GetRole 响应已收到 ✓")
	} else {
		fmt.Println("  [WS] GetRole 未收到响应（可能需要登录，或检查 callback 注册）")
	}
	fmt.Println()

	// ================================================================
	// 场景 2：进程内 RPC — 直接调用验证 GameService 行为
	// ================================================================
	fmt.Println("========== 场景 2: 进程内 RPC 验证 ==========")
	fmt.Println()

	ctx := context.Background()
	roleRes, err := rpc.SendRPCMessage(ctx, GetRoleAPI.NewRPC(&GetRoleReq{UserId: "test_001"}))
	if err != nil {
		fmt.Printf("  [RPC] GetRole 失败: %v\n", err)
	} else {
		fmt.Printf("  [RPC] GetRole 成功: Name=%s Level=%d\n", roleRes.Name, roleRes.Level)
	}

	moveRes, err := rpc.SendRPCMessage(ctx, MoveAPI.NewRPC(&MoveReq{
		PlayerId: "test_001", X: 10, Y: 20, Z: 0, Tick: time.Now().UnixMilli(),
	}))
	if err != nil {
		fmt.Printf("  [RPC] Move 失败: %v\n", err)
	} else {
		fmt.Printf("  [RPC] Move 成功: OK=%v\n", moveRes.OK)
	}
	fmt.Println()

	// ================================================================
	// 场景 3：KCP robot — 多人移动同步（高频发送）
	// ================================================================
	fmt.Println("========== 场景 3: KCP 多人移动同步 ==========")
	fmt.Println()

	// 注意：当前 KCP 网关未启动（GatewayOptions 里没配 KCP）
	// 这里用进程内 RPC 模拟多个 "KCP 玩家" 并发发送移动包
	// 展示高频移动同步的 pattern

	const (
		playerCount = 10
		movesPerPlayer = 100
	)

	fmt.Printf("  模拟 %d 个玩家，每人 %d 次移动...\n", playerCount, movesPerPlayer)
	start := time.Now()

	var wg sync.WaitGroup
	wg.Add(playerCount)
	for p := 0; p < playerCount; p++ {
		go func(playerId int) {
			defer wg.Done()
			pid := fmt.Sprintf("player_%03d", playerId)
			for i := 0; i < movesPerPlayer; i++ {
				req := &MoveReq{
					PlayerId: pid,
					X:        float32(i) * 0.5,
					Y:        float32(i) * 0.3,
					Z:        0,
					Tick:     time.Now().UnixMilli(),
				}
				_, err := rpc.SendRPCMessage(ctx, MoveAPI.NewRPC(req))
				if err != nil {
					fmt.Printf("  Move 失败: player=%s err=%v\n", pid, err)
					return
				}
			}
		}(p)
	}
	wg.Wait()

	elapsed := time.Since(start)
	totalMoves := int64(playerCount * movesPerPlayer)
	qps := float64(totalMoves) / elapsed.Seconds()
	fmt.Printf("  完成: %d 次移动, 耗时 %v, QPS=%.0f\n", totalMoves, elapsed, qps)
	fmt.Printf("  GameService 累计移动计数: %d\n", gameSvc.moveCount.Load())
	fmt.Println()

	// ================================================================
	// 场景 4：KCP robot — 网络连接演示（需要真实 KCP 网关）
	// ================================================================
	fmt.Println("========== 场景 4: KCP Robot 网络演示 ==========")
	fmt.Println()
	fmt.Println("  KCP robot 的网络连接需要 WithGatewayKCP 启动 KCP 监听。")
	fmt.Println("  用法示例：")
	fmt.Println(`
    // 服务端
    aeadKey := [32]byte{...}  // 预共享密钥
    rpc.NewRPCServer().
        WithGatewayOptions(rpc.GatewayOptions{
            TCPPort: "8082",
            KCP:     rpc.NewKCPBuilder("8300").WithAEADKey(aeadKey[:]),
        }).
        WithService(NewGameService()).
        Run()

    // 客户端
    kcpBot := robot.NewRobotKCP(aeadKey[:])
    kcpBot.RegisterCallbackMessage("game.MoveSync", func(r robot.IRobot, data []byte) {
        // 收到其他玩家的移动广播
        fmt.Println("收到移动广播", data)
    })
    kcpBot.Connect("127.0.0.1:8300")

    // 发送移动
    kcpBot.SendMessage("game", "Move", &MoveReq{
        PlayerId: "player_001",
        X: 10.5, Y: 20.3, Z: 0,
    })
  `)

	// ================================================================
	// 场景 5：WS robot 工具函数演示
	// ================================================================
	fmt.Println("========== 场景 5: Robot API 速查 ==========")
	fmt.Println()
	fmt.Println(`
  // ---- 创建 robot ----
  wsBot  := robot.NewRobotWs("/ws")       // WebSocket
  wssBot := robot.NewRobotWss("/wss")     // WebSocket over TLS
  tcpBot := robot.NewRobotTcp()           // TCP
  kcpBot := robot.NewRobotKCP(aeadKey)    // KCP (v2 新增)

  // ---- 连接 ----
  wsBot.Connect("127.0.0.1:8082")        // 启动心跳 + 读 loop

  // ---- 注册回调 ----
  wsBot.RegisterCallbackMessage("game.Login", func(r robot.IRobot, data []byte) {
      // data 是 proto 编码的响应
      res := &LoginRes{}
      proto.Unmarshal(data, res)
      fmt.Println("登录结果:", res.Welcome)
  })

  // ---- 发送请求 ----
  wsBot.Send("game.Login", &LoginReq{Token: "abc"})
  // 或
  wsBot.SendMessage("game", "Login", &LoginReq{Token: "abc"})

  // ---- KCP robot 额外能力 ----
  kcpBot.(*robot.kcpRobot).Close()  // 显式关闭
  `)

	fmt.Println("=== robot 自测示例结束 ===")
	fmt.Println()

	// 给 WS robot 的 goroutine 一点时间清理
	time.Sleep(200 * time.Millisecond)
	os.Exit(0)
}

// mustMarshalAsWSData 把一个 struct 序列化为 proto bytes。
// 注意：这里的 struct 不是 proto.Message，所以走 JSON → WSMessage.Data 的路径。
// 真正的生产代码应当用 .proto 文件生成的 Message 类型。
// 这里简化为直接传 nil proto（仅演示 robot API 调用流程）。
func mustMarshalAsWSData(v interface{}) proto.Message {
	// 游戏场景里这里应该是 proto generated message
	// 示例简化：传 nil 占位
	_ = v
	return nil
}

func init() {
	// 让 util.Go 可用
	util.InitGoroutinePool()
}
