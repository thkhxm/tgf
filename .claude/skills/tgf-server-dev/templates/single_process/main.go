// {{PROJECT_NAME}} —— tgf v2 单进程游戏服骨架
//
// 形态：一个进程承载全部业务 Module + 客户端网关（TCP），零 Consul / 零 Redis /
// 零 MySQL 外部依赖，适合本地开发、小游戏后端、原型验证。
//
// 关键开关：
//   - WithSingleProcess()        → 不挂 Consul + 进程内 RPC 直通（localDispatcher）
//   - WithCache(CacheModuleClose) → 关闭缓存层，db.Run 不连 Redis/MySQL
//   - WithGatewayOptions          → 客户端 TCP 入口（可加 WSPath / KCP）
//
// 迁移到分布式只需两步（业务代码零改动）：
//   1. 去掉 WithSingleProcess()（默认模式自动挂 Consul）
//   2. 把各 Module 拆成独立进程（参考 distributed 模板）
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc"
)

// ====================================================================
// 1. RPC 契约（ServiceAPI 描述符 + 出入参）
// ====================================================================
//
// 业务规模变大后，把这一段移到 internal/api/ 单独成包（见 distributed 模板）。

type LoginReq struct {
	UserId string
}
type LoginRes struct {
	Welcome string
}

// UserLogin 是 user.Login 的 RPC 描述符。
// 调用方式：rpc.SendRPCMessage(ctx, UserLogin.NewRPC(&LoginReq{...}))
var UserLogin = &rpc.ServiceAPI[*LoginReq, *LoginRes]{
	ModuleName:  "user",
	Name:        "Login",
	MessageType: "user.Login",
}

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

// ====================================================================
// 2. UserService（用户模块）
// ====================================================================

type UserService struct {
	rpc.Module
}

func NewUserService() *UserService {
	return &UserService{Module: rpc.Module{Name: "user", Version: "1.0"}}
}

func (s *UserService) Startup() (bool, error) {
	log.InfoTag("user", "UserService 启动 version=%v", s.Version)
	return true, nil
}

// Login 处理登录：验证身份后跨 module 调 ShopService 发新手礼包。
// 方法签名固定为 (ctx, *Req, *Res) error —— rpcx 与 localDispatcher 都识别。
//
// 注意：本方法是"进程内/服务间 RPC"风格（普通 struct 参数），只能被
// rpc.SendRPCMessage 调用。要让 TCP/WS/KCP 客户端帧直达，参数必须改为
// rpc.Args[T]/rpc.Reply[T]（T 为 protobuf Message），见 README《接入客户端协议》。
func (s *UserService) Login(ctx context.Context, req *LoginReq, reply *LoginRes) error {
	log.InfoTag("user", "用户登录 userId=%v", req.UserId)

	// 跨 module RPC：单进程下走 localDispatcher 零网络直通，
	// 分布式下走 rpcx + Consul —— 业务代码完全一样。
	giftRes, err := rpc.SendRPCMessage(ctx, ShopGiveGift.NewRPC(
		&GiveGiftReq{UserId: req.UserId, GiftId: "welcome_pack"}))
	if err != nil {
		// 礼包失败不阻断登录，但必须留日志——禁止吞错。
		log.WarnTag("user", "新手礼包发放失败 userId=%v err=%v", req.UserId, err)
		reply.Welcome = fmt.Sprintf("欢迎 %s（礼包发放失败）", req.UserId)
		return nil
	}
	if giftRes.OK {
		reply.Welcome = fmt.Sprintf("欢迎 %s，新手礼包已发放！", req.UserId)
	} else {
		reply.Welcome = fmt.Sprintf("欢迎 %s", req.UserId)
	}
	return nil
}

// ====================================================================
// 3. ShopService（商城模块）
// ====================================================================

type ShopService struct {
	rpc.Module
}

func NewShopService() *ShopService {
	return &ShopService{Module: rpc.Module{Name: "shop", Version: "1.0"}}
}

func (s *ShopService) Startup() (bool, error) {
	log.InfoTag("shop", "ShopService 启动 version=%v", s.Version)
	return true, nil
}

func (s *ShopService) GiveGift(ctx context.Context, req *GiveGiftReq, reply *GiveGiftRes) error {
	log.InfoTag("shop", "发放礼包 userId=%v giftId=%v", req.UserId, req.GiftId)
	reply.OK = true
	return nil
}

// ====================================================================
// 4. main
// ====================================================================

func main() {
	// 加载配置（读 .env.<TGFMODULE>，默认 .env.dev）。
	// 业务统一经 config.Current() 读配置，禁止 os.Getenv 直读绕过配置系统。
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}

	done := rpc.NewRPCServer().
		// 单进程模式：零 Consul + 进程内 RPC 直通
		WithSingleProcess().
		// 本骨架无持久化需求；要接 Redis 改为 tgf.CacheModuleRedis
		WithCache(tgf.CacheModuleClose).
		// 方法级策略：给 shop.GiveGift 加 100 QPS 限流 + 2s 超时
		WithMethodPolicy("shop.GiveGift", rpc.MethodPolicy{
			Timeout:   2 * time.Second,
			RateLimit: 100,
		}).
		// 客户端网关：TCP 入口（要加 WebSocket 配 WSPath；KCP 见 README）
		WithGatewayOptions(rpc.GatewayOptions{
			TCPPort: "8082",
		}).
		WithService(NewUserService()).
		WithService(NewShopService()).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  {{PROJECT_NAME}} 单进程游戏服已启动")
	fmt.Println("  TCP Gateway : localhost:8082")
	fmt.Printf("  运行环境    : %s\n", cfg.Runtime.Module)
	fmt.Println("  Modules     : user, shop")
	fmt.Println("========================================")
	fmt.Println()

	// 冒烟自检：进程内模拟一次 user.Login（生成项目后首跑应看到登录成功输出）。
	// 业务稳定后可删除这一段。
	go func() {
		time.Sleep(1 * time.Second)
		res, err := rpc.SendRPCMessage(context.Background(),
			UserLogin.NewRPC(&LoginReq{UserId: "player_001"}))
		if err != nil {
			fmt.Printf("[冒烟] Login 失败: %v\n", err)
			return
		}
		fmt.Printf("[冒烟] Login 成功: %s\n", res.Welcome)
	}()

	// 优雅停机：SIGINT/SIGTERM 触发框架 Destroy 链
	// （停 accept → drain in-flight → write-behind 终末落库）。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		fmt.Println("server 退出")
	case s := <-sig:
		fmt.Printf("收到信号 %v，正在优雅关闭...\n", s)
	}
}
