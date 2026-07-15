// tgf v2 单进程模式示例
//
// 演示一个完整的"单进程多 Module"场景：
//   - UserService（用户模块）：处理登录，跨 module 调 ShopService 发新手礼包
//   - ShopService（商城模块）：处理购买、发礼包
//   - GateService（网关）：TCP 连接入口
//
// 用法：
//
//	cd tgf/example/single_process
//	go run .
//
// 然后用 TCP 客户端（telnet / robot / 自己写）连 localhost:8082 发帧。
//
// 关键 v2 特性演示：
//   - WithSingleProcess()           → 零 Consul 依赖
//   - SendRPCMessage 进程内直通      → UserService 直接反射调 ShopService
//   - WithMethodPolicy              → 给 shop.GiveGift 加限流
//   - config.Load                   → 新配置系统
//   - metrics.NewMemoryProvider     → 内存 metrics 观察埋点
//   - log.InfoTagW                  → zap.Field 零分配日志
//
// v2 说明：单进程 + 网关组合可用，TCP 客户端发来的 Logic 帧会经
// localDispatcher 直通本地 service。
// 网关可达的方法参数必须是 rpc.Args[T]/rpc.Reply[T] 形式；本示例的方法是
// 普通 struct 参数（进程内 RPC 风格），客户端帧不可直达，详见 README。
//
// v2 说明：框架默认开启登录鉴权（gate.Login 要求 LoginReq.Token）。
// 本示例不走 gate.Login，无需配置；需要客户端登录的项目参考 README 的
// 「登录鉴权」一节（GenerateLoginToken / UserLoginWithToken / WithoutLoginCheck）。
package main

import (
	"fmt"
	"os"
	"time"

	"context"
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/metrics"
	"github.com/thkhxm/tgf/v2/rpc"
	"go.uber.org/zap"
)

// ====================================================================
// 1. 定义 RPC 接口（ServiceAPI 声明）
// ====================================================================

// --- UserService 的 RPC ---

type LoginReq struct {
	UserId string
}
type LoginRes struct {
	Welcome string
}

// UserLogin 是 UserService.Login 的 RPC 描述符。
// 业务代码用 rpc.SendRPCMessage(ctx, UserLogin.NewRPC(&LoginReq{...})) 发起调用。
var UserLogin = &rpc.ServiceAPI[*LoginReq, *LoginRes]{
	ModuleName:  "user",
	Name:        "Login",
	MessageType: "user.Login",
}

// --- ShopService 的 RPC ---

type GiveGiftReq struct {
	UserId string
	GiftId string
}
type GiveGiftRes struct {
	OK bool
}

var ShopGiveGift = &rpc.ServiceAPI[*GiveGiftReq, *GiveGiftRes]{
	ModuleName:  "shop",
	Name:        "GiveGift",
	MessageType: "shop.GiveGift",
}

type BuyItemReq struct {
	UserId string
	ItemId string
}
type BuyItemRes struct {
	Success bool
	Message string
}

var ShopBuyItem = &rpc.ServiceAPI[*BuyItemReq, *BuyItemRes]{
	ModuleName:  "shop",
	Name:        "BuyItem",
	MessageType: "shop.BuyItem",
}

// ====================================================================
// 2. UserService
// ====================================================================

type UserService struct {
	rpc.Module
}

func NewUserService() *UserService {
	return &UserService{
		Module: rpc.Module{Name: "user", Version: "1.0"},
	}
}

func (s *UserService) Startup() (bool, error) {
	log.InfoTagW("user", "UserService 启动", zap.String("version", s.Version))
	return true, nil
}

// Login 处理用户登录：验证身份后跨 module 调 ShopService 发新手礼包。
// 签名必须是 (ctx, *Req, *Res) error —— rpcx 和本地 dispatcher 都识别。
func (s *UserService) Login(ctx context.Context, req *LoginReq, reply *LoginRes) error {
	log.InfoTagW("user", "用户登录", zap.String("userId", req.UserId))

	// 跨 module RPC：在单进程模式下，这里走 localDispatcher 反射调用，
	// 不经过任何网络。和分布式模式下的调用代码完全一样。
	giftReq := &GiveGiftReq{UserId: req.UserId, GiftId: "welcome_pack"}
	giftRes, err := rpc.SendRPCMessage(ctx, ShopGiveGift.NewRPC(giftReq))
	if err != nil {
		log.WarnTagW("user", "发新手礼包失败", zap.String("userId", req.UserId), zap.Error(err))
		reply.Welcome = fmt.Sprintf("欢迎 %s（礼包发放失败）", req.UserId)
		return nil // 登录本身不因礼包失败而失败
	}

	if giftRes.OK {
		reply.Welcome = fmt.Sprintf("欢迎 %s，新手礼包已发放！", req.UserId)
	} else {
		reply.Welcome = fmt.Sprintf("欢迎 %s", req.UserId)
	}

	log.InfoTagW("user", "登录完成",
		zap.String("userId", req.UserId),
		zap.String("welcome", reply.Welcome),
	)
	return nil
}

// ====================================================================
// 3. ShopService
// ====================================================================

type ShopService struct {
	rpc.Module
	giftCount int // 简单计数器演示状态
}

func NewShopService() *ShopService {
	return &ShopService{
		Module: rpc.Module{Name: "shop", Version: "1.0"},
	}
}

func (s *ShopService) Startup() (bool, error) {
	log.InfoTagW("shop", "ShopService 启动", zap.String("version", s.Version))
	return true, nil
}

// GiveGift 发礼包。由 UserService.Login 通过 SendRPCMessage 跨 module 调用。
func (s *ShopService) GiveGift(ctx context.Context, req *GiveGiftReq, reply *GiveGiftRes) error {
	s.giftCount++
	log.InfoTagW("shop", "发放礼包",
		zap.String("userId", req.UserId),
		zap.String("giftId", req.GiftId),
		zap.Int("totalGifts", s.giftCount),
	)
	reply.OK = true
	return nil
}

// BuyItem 购买物品。
func (s *ShopService) BuyItem(ctx context.Context, req *BuyItemReq, reply *BuyItemRes) error {
	log.InfoTagW("shop", "购买物品",
		zap.String("userId", req.UserId),
		zap.String("itemId", req.ItemId),
	)
	reply.Success = true
	reply.Message = fmt.Sprintf("用户 %s 成功购买 %s", req.UserId, req.ItemId)
	return nil
}

// ====================================================================
// 4. main
// ====================================================================

func main() {
	// --- 4.1 加载配置（C3 新配置系统） ---
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("配置加载成功: runtime=%s redis=%s\n", cfg.Runtime.Module, cfg.Redis.Addr)

	// --- 4.2 设置 metrics（B4） ---
	// 这里用内存 provider 演示；生产替换为 Prometheus adapter
	memMetrics := metrics.NewMemoryProvider()

	// --- 4.3 构建并启动 server ---
	done := rpc.NewRPCServer().
		// C8: 单进程模式 = 不挂 Consul + 进程内 RPC 直通
		WithSingleProcess().
		// 本示例无需持久化，关闭缓存层避免 db.Run 尝试连 Redis/MySQL
		WithCache(tgf.CacheModuleClose).
		// B4: 注入 metrics provider
		WithMetrics(memMetrics).
		// C6: 给 shop.GiveGift 加限流（演示策略化）
		WithMethodPolicy("shop.GiveGift", rpc.MethodPolicy{
			Timeout:   2 * time.Second,
			RateLimit: 100, // QPS
		}).
		// A6: 健康心跳（演示骨架）
		WithHealthCheck(10 * time.Second).
		// C1: 统一网关入口（演示 TCP 监听）
		WithGatewayOptions(rpc.GatewayOptions{
			TCPPort: "8082",
		}).
		// 注册多个 module
		WithService(NewUserService()).
		WithService(NewShopService()).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  tgf v2 单进程模式示例已启动")
	fmt.Println("  TCP Gateway: localhost:8082")
	fmt.Println("  Consul: 无（单进程模式）")
	fmt.Println("  Modules: user, shop")
	fmt.Println("========================================")
	fmt.Println()

	// --- 4.4 模拟一次进程内 RPC 调用（演示 SendRPCMessage 走 local dispatcher） ---
	go func() {
		time.Sleep(1 * time.Second) // 等 server 完全启动

		fmt.Println("[演示] 模拟用户登录 RPC 调用...")
		ctx := context.Background()
		loginReq := &LoginReq{UserId: "player_001"}
		loginRes, err := rpc.SendRPCMessage(ctx, UserLogin.NewRPC(loginReq))
		if err != nil {
			fmt.Printf("[演示] Login 失败: %v\n", err)
		} else {
			fmt.Printf("[演示] Login 成功: %s\n", loginRes.Welcome)
		}

		// 再调一次 BuyItem
		buyReq := &BuyItemReq{UserId: "player_001", ItemId: "sword_001"}
		buyRes, err := rpc.SendRPCMessage(ctx, ShopBuyItem.NewRPC(buyReq))
		if err != nil {
			fmt.Printf("[演示] BuyItem 失败: %v\n", err)
		} else {
			fmt.Printf("[演示] BuyItem 成功: %s\n", buyRes.Message)
		}

		// 打印 metrics 快照
		fmt.Println()
		fmt.Println("[metrics] 网关连接数:", memMetrics.GaugeValue("tgf_gate_connections"))
		fmt.Println("[metrics] RPC 调用总数:", memMetrics.CounterValue("tgf_rpc_calls_total"))
		fmt.Println("[metrics] RPC 失败总数:", memMetrics.CounterValue("tgf_rpc_calls_fail_total"))
		fmt.Println("[metrics] RPC 延迟样本数:", len(memMetrics.HistogramObservations("tgf_rpc_latency_ms")))
	}()

	// --- 4.5 优雅关闭 ---
	// SIGINT/SIGTERM 由框架统一处理；业务 main 只等待 Run 的 done。
	<-done
	fmt.Println("server 退出")
}
