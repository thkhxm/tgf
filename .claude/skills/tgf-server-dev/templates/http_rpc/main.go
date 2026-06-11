// {{PROJECT_NAME}} —— tgf v2 混合服务骨架（HTTP 入口 + 游戏服 RPC）
//
// 形态：对外 HTTP/REST 入口，把请求经 HTTP→RPC 桥转给后端游戏 service。
// HTTP handler 不直接碰业务数据，而是经框架注入的 web.Backend 调用
// module.method —— 与游戏客户端走同一套 service 实现、同一条策略管道
// （限流/熔断/超时）、同一份 metrics，traceId 从 HTTP 头 X-Trace-Id
// 一路透传到后端 RPC。
//
// 本骨架是单进程混合部署（HTTP 入口 + 游戏 service 同进程，桥走
// localDispatcher 直通）。拆成多进程分布式见 README《拆分多进程》。
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
	"github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc"
	"github.com/thkhxm/tgf/v2/web"
)

// ====================================================================
// 1. 后端游戏 service（与游戏客户端共用同一套实现）
// ====================================================================

type GetPlayerReq struct {
	PlayerId string
}
type GetPlayerRes struct {
	PlayerId string
	Level    int
}

type GiveGiftReq struct {
	PlayerId string
	GiftId   string
}
type GiveGiftRes struct {
	OK      bool
	Message string
}

type PlayerService struct {
	rpc.Module
}

func NewPlayerService() *PlayerService {
	return &PlayerService{Module: rpc.Module{Name: "player", Version: "1.0"}}
}

func (s *PlayerService) Startup() (bool, error) {
	log.InfoTag("player", "PlayerService 启动 version=%v", s.Version)
	return true, nil
}

func (s *PlayerService) GetPlayer(ctx context.Context, req *GetPlayerReq, reply *GetPlayerRes) error {
	// TODO-业务：替换为真实数据读取
	reply.PlayerId = req.PlayerId
	reply.Level = 1
	return nil
}

func (s *PlayerService) GiveGift(ctx context.Context, req *GiveGiftReq, reply *GiveGiftRes) error {
	log.InfoTag("player", "发放礼包 playerId=%v giftId=%v", req.PlayerId, req.GiftId)
	reply.OK = true
	reply.Message = fmt.Sprintf("玩家 %s 已领取礼包 %s", req.PlayerId, req.GiftId)
	return nil
}

// ====================================================================
// 2. HTTP handler（经 web.Backend 桥到后端 RPC）
// ====================================================================

// GET /players/{id} → RPC player.GetPlayer
func getPlayerHandler(w http.ResponseWriter, r *http.Request) {
	backend, ok := web.BackendFromRequest(r)
	if !ok {
		http.Error(w, "backend 未注入", http.StatusInternalServerError)
		return
	}
	args := GetPlayerReq{PlayerId: r.PathValue("id")}
	var reply GetPlayerRes
	// Invoke(ctx, module, method, &args, &reply)：用请求 context，
	// traceId 自动透传，方法策略（限流/超时）自动生效。
	if err := backend.Invoke(r.Context(), "player", "GetPlayer", &args, &reply); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway) // 上游失败统一 502
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// POST /players/{id}/gift → RPC player.GiveGift
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
	if _, err := config.Load(); err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}

	done := rpc.NewRPCServer().
		// 单进程混合部署：桥走 localDispatcher 直通；拆多进程见 README
		WithSingleProcess().
		WithCache(tgf.CacheModuleClose).
		WithService(NewPlayerService()).
		// 方法策略：HTTP 桥与游戏客户端 RPC 共用同一条管道
		WithMethodPolicy("player.GiveGift", rpc.MethodPolicy{
			Timeout:   2 * time.Second,
			RateLimit: 500,
		}).
		// Options.Backend 留空 → 框架自动注入默认 HTTP→RPC 桥
		WithHTTPService(web.Options{
			Routes: func(r *web.Router) {
				r.GET("/health", web.Health())
				r.GET("/players/{id}", getPlayerHandler)
				r.POST("/players/{id}/gift", giveGiftHandler)
			},
			Limiter: web.NewTokenBucketLimiter(2000),
		}).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  {{PROJECT_NAME}} HTTP+RPC 混合服务已启动")
	fmt.Printf("  HTTP 入口 : http://127.0.0.1:%s\n", config.Current().HTTP.Port)
	fmt.Println("  后端模块  : player (GetPlayer / GiveGift)")
	fmt.Println("========================================")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		log.InfoTag("main", "server 退出")
	case s := <-sig:
		log.InfoTag("main", "收到信号 %v，正在优雅关闭...", s)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
