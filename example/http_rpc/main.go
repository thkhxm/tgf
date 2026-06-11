// tgf v3 / G 档示例 ②：REST + 调用游戏服 RPC（分布式 web 服务接游戏后端）
//
// 演示"分布式 web 服务"形态：一个对外的 HTTP/REST 入口，把请求经 **HTTP→RPC 桥**
// 转发给后端游戏 service（rpcx）。HTTP handler 不直接碰业务数据，而是通过框架
// 注入的 web.Backend 调用任意后端 module.method——与游戏客户端走的是同一套
// service 实现、同一条策略管道（E1 限流/熔断）、同一份可观测埋点（E3 metrics），
// 且 traceId 从 HTTP 请求一路透传到后端 RPC（X-Trace-Id → rpcx ReqMetaData）。
//
// 关键点（与 BRIDGE 契约一致）：
//   - WithHTTPService 装配时，Options.Backend 为 nil → 框架自动注入默认后端
//     （defaultWebBackend）：单进程命中 localDispatcher 走进程内直通，否则走
//     SendRPCMessageByStr 分布式路径。两条路径业务代码完全一样。
//   - handler 内 web.BackendFromRequest(req) 取出 Backend，
//     Invoke(ctx, module, method, &args, &reply) 调后端——args/reply 为指针，
//     语义与 rpcx service 方法签名 (ctx, *Req, *Res) error 一致。
//
// 本示例为可直接 go run 的**单进程混合部署**：HTTP 入口 + 游戏 service 同进程，
// 用 WithSingleProcess() 让 Backend.Invoke 走 localDispatcher 直通。要拆成真正
// 的多进程分布式（HTTP 进程 + 若干游戏服进程，经 Consul 服务发现互联），只需：
//   - 游戏服进程：去掉 WithSingleProcess，正常 WithService(...).Run() 注册进 Consul；
//   - HTTP 进程：用 WithClientOnly() + WithHTTPServiceConsul(...)（client-only
//     纯 web 接入层）：WithClientOnly() 才是"不把自己伪装成 RPC 节点"的开关——
//     它不注册 service、不创建 rpcx server、不监听 rpcx 端口、不进入 RPC 服务
//     发现，但照常初始化 Consul discovery + RPC client，所以 Backend.Invoke
//     会经 SendRPCMessageByStr 跨节点找到游戏服。注意：默认 Run() 即便不
//     WithService 任何 service 仍会创建 rpcx server、监听 ServicePort、并在
//     discovery 非 nil 时注册进 Consul——那不是 client-only，必须显式
//     WithClientOnly() 才是。WithHTTPServiceConsul 再把这个 HTTP 接入层本身
//     注册进 Consul（带 health 路由 + TTL 续约），使其可被发现/负载均衡。
// 详见 example/http_rpc/README.md「拆成多进程」一节。
//
// 用法：
//
//	cd tgf/example/http_rpc
//	go run .
//	# 然后：
//	curl -i http://127.0.0.1:8091/players/player_001          # 查询玩家（GET → RPC GetPlayer）
//	curl -i -X POST http://127.0.0.1:8091/players/player_001/gift -d '{"giftId":"welcome"}'  # 发礼包（POST → RPC GiveGift）
//	curl -i -H "X-Trace-Id: my-trace-1" http://127.0.0.1:8091/players/player_001  # 看响应头回写同一个 traceId
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc"
	"github.com/thkhxm/tgf/v2/trace"
	"github.com/thkhxm/tgf/v2/web"
	"go.uber.org/zap"
)

// ====================================================================
// 1. 后端游戏 service（与游戏客户端共用同一套实现）
// ====================================================================

// 这些 Req/Res 就是普通的 RPC 出入参；HTTP 桥经 Backend.Invoke 传入指针。
type GetPlayerReq struct {
	PlayerId string
}
type GetPlayerRes struct {
	PlayerId string
	Level    int
	Coins    int
}

type GiveGiftReq struct {
	PlayerId string
	GiftId   string
}
type GiveGiftRes struct {
	OK      bool
	Message string
}

// PlayerService 是后端游戏 module（rpcx service）。
// 方法签名固定为 (ctx, *Req, *Res) error——rpcx 与 localDispatcher 都识别。
type PlayerService struct {
	rpc.Module
}

func NewPlayerService() *PlayerService {
	return &PlayerService{Module: rpc.Module{Name: "player", Version: "1.0"}}
}

func (s *PlayerService) Startup() (bool, error) {
	log.InfoTagW("player", "PlayerService 启动", zap.String("version", s.Version))
	return true, nil
}

// GetPlayer 查询玩家信息。HTTP GET /players/{id} 经桥调到这里。
func (s *PlayerService) GetPlayer(ctx context.Context, req *GetPlayerReq, reply *GetPlayerRes) error {
	// 演示：从 ctx 读链路 traceId，证明 HTTP→RPC 全链路同一个 traceId。
	// trace.TraceIDFromContext 与 web.Trace 中间件、rpcx ReqMetaData 同一套体系。
	log.InfoTagW("player", "查询玩家",
		zap.String("playerId", req.PlayerId),
		zap.String("traceId", trace.TraceIDFromContext(ctx)),
	)
	reply.PlayerId = req.PlayerId
	reply.Level = 10 // 演示用假数据
	reply.Coins = 9999
	return nil
}

// GiveGift 给玩家发礼包。HTTP POST /players/{id}/gift 经桥调到这里。
func (s *PlayerService) GiveGift(ctx context.Context, req *GiveGiftReq, reply *GiveGiftRes) error {
	log.InfoTagW("player", "发放礼包",
		zap.String("playerId", req.PlayerId),
		zap.String("giftId", req.GiftId),
	)
	reply.OK = true
	reply.Message = fmt.Sprintf("玩家 %s 已领取礼包 %s", req.PlayerId, req.GiftId)
	return nil
}

// ====================================================================
// 2. HTTP handler（经 web.Backend 桥到后端 RPC）
// ====================================================================

// getPlayerHandler 处理 GET /players/{id}：经桥调 player.GetPlayer。
func getPlayerHandler(w http.ResponseWriter, r *http.Request) {
	backend, ok := web.BackendFromRequest(r)
	if !ok {
		// 经 rpc.WithHTTPService 装配时框架会自动注入默认 Backend，这里通常不会发生。
		http.Error(w, "backend 未注入", http.StatusInternalServerError)
		return
	}

	args := GetPlayerReq{PlayerId: r.PathValue("id")}
	var reply GetPlayerRes
	// Invoke(ctx, module, method, &args, &reply)——ctx 用请求 context，
	// 框架会把 HTTP 链路 traceId 透传进后端 RPC，并应用 E1 策略 + A7 超时。
	if err := backend.Invoke(r.Context(), "player", "GetPlayer", &args, &reply); err != nil {
		// 后端调用失败统一回 502 Bad Gateway（上游网关语义）。
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// giveGiftHandler 处理 POST /players/{id}/gift：解析 body 后经桥调 player.GiveGift。
func giveGiftHandler(w http.ResponseWriter, r *http.Request) {
	backend, ok := web.BackendFromRequest(r)
	if !ok {
		http.Error(w, "backend 未注入", http.StatusInternalServerError)
		return
	}

	var body struct {
		GiftId string `json:"giftId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.GiftId == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "giftId 必填"})
		return
	}

	args := GiveGiftReq{PlayerId: r.PathValue("id"), GiftId: body.GiftId}
	var reply GiveGiftRes
	if err := backend.Invoke(r.Context(), "player", "GiveGift", &args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// ====================================================================
// 3. main
// ====================================================================

func main() {
	done := rpc.NewRPCServer().
		// 单进程混合部署：HTTP 入口 + 游戏 service 同进程，Backend 走 localDispatcher 直通。
		// 拆多进程时这一行改掉（见文件头与 README）。
		WithSingleProcess().
		// 无持久化需求，关闭缓存层。
		WithCache(tgf.CacheModuleClose).
		// 注册后端游戏 service（HTTP 桥与游戏客户端共用同一套实现）。
		WithService(NewPlayerService()).
		// 给 player.GiveGift 加方法策略（限流 + 超时）——HTTP 桥与 RPC 同一条管道。
		WithMethodPolicy("player.GiveGift", rpc.MethodPolicy{
			Timeout:   2 * time.Second,
			RateLimit: 500, // QPS
		}).
		// 装载 HTTP 服务。Options.Backend 留空 → 框架自动注入默认后端（HTTP→RPC 桥）。
		WithHTTPService(web.Options{
			Addr: ":8091",
			Routes: func(r *web.Router) {
				r.GET("/health", func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
				})
				r.GET("/players/{id}", getPlayerHandler)
				r.POST("/players/{id}/gift", giveGiftHandler)
			},
			// HTTP 入口侧也可叠加全局限流（与后端方法策略各司其职）。
			Limiter: web.NewTokenBucketLimiter(2000),
		}).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  tgf v3 G档 · REST + 游戏服 RPC 混合示例已启动")
	fmt.Println("  HTTP 入口: http://127.0.0.1:8091")
	fmt.Println("  后端 module: player (GetPlayer / GiveGift)")
	fmt.Println("  桥: HTTP handler --web.Backend--> rpcx service（单进程 localDispatcher 直通）")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("  试一试:")
	fmt.Println("    curl -i http://127.0.0.1:8091/players/player_001")
	fmt.Println(`    curl -i -X POST http://127.0.0.1:8091/players/player_001/gift -d '{"giftId":"welcome"}'`)
	fmt.Println("    curl -i -H 'X-Trace-Id: my-trace-1' http://127.0.0.1:8091/players/player_001")
	fmt.Println()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		log.InfoTag("http_rpc", "server 退出")
	case s := <-sig:
		log.InfoTag("http_rpc", "收到信号 %v，正在优雅关闭...", s)
	}
}

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
