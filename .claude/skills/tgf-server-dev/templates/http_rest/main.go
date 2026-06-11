// {{PROJECT_NAME}} —— tgf v2 纯 HTTP REST 服务骨架
//
// 形态：把 tgf 当常规 web 框架用——不接游戏 RPC 后端，WithStandalone 起一个
// 标准 REST 服务。内置中间件链（Trace[X-Trace-Id] / AccessLog / Metrics / Recover）
// 默认全开；限流、Bearer 鉴权、优雅停机开箱即用。
//
// 需要从 HTTP 调后端游戏服 RPC 的形态请用 http_rpc 模板（HTTP→RPC 桥）。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc"
	"github.com/thkhxm/tgf/v2/trace"
	"github.com/thkhxm/tgf/v2/web"
)

// ====================================================================
// 1. 业务 handler（演示用内存存储；真实项目换 DB/缓存）
// ====================================================================

type userStore struct {
	seq atomic.Int64
}

type userDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// GET /users/{id} —— Go 1.22 ServeMux 路径参数语义
func (s *userStore) getUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	writeJSON(w, http.StatusOK, userDTO{ID: 0, Name: "user-" + id})
}

// POST /users
func (s *userStore) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name 必填"})
		return
	}
	writeJSON(w, http.StatusCreated, userDTO{ID: s.seq.Add(1), Name: in.Name})
}

// GET /admin/stats（Bearer 鉴权保护）
func (s *userStore) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"created": s.seq.Load(),
		// Trace 中间件已注入链路 id（入站 X-Trace-Id 复用，否则新生成；
		// 响应头总会回写 X-Trace-Id）
		"traceId": trace.TraceIDFromContext(r.Context()),
	})
}

// ====================================================================
// 2. main
// ====================================================================

func main() {
	if _, err := config.Load(); err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}
	store := &userStore{}

	done := rpc.NewRPCServer().
		// 纯 web 进程：不挂 Consul、不启动 RPC client watch。
		// 要注册进 Consul 供发现/负载均衡时，改用：
		//   WithClientOnly().WithHTTPServiceConsul(opt, rpc.HTTPRegistration{ServiceName: "{{PROJECT_NAME}}-web"})
		WithStandalone().
		// 无持久化需求；要接 Redis 改 tgf.CacheModuleRedis
		WithCache(tgf.CacheModuleClose).
		WithHTTPService(web.Options{
			// Addr 省略时读配置 HTTPPort（默认 8090）
			Routes: func(r *web.Router) {
				// 探活端点（容器/网关健康检查）
				r.GET("/health", web.Health())

				r.GET("/users/{id}", store.getUser)
				r.POST("/users", store.createUser)

				// 鉴权分组：/admin/* 要求 Bearer 口令。
				// 口令经 provider 每次请求动态读配置（支持热轮换）；
				// 未配置 ADMIN_TOKEN → 503 fail-closed；口令错/缺 → 401。
				admin := r.Group("/admin", web.Auth(web.BearerToken(func() string {
					return config.Current().Security.AdminToken
				})))
				admin.GET("/stats", store.stats)
			},
			// 全局令牌桶限流，超限自动 429
			Limiter: web.NewTokenBucketLimiter(1000),
		}).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  {{PROJECT_NAME}} REST 服务已启动")
	fmt.Printf("  HTTP : http://127.0.0.1:%s\n", config.Current().HTTP.Port)
	fmt.Println("  路由 : GET /health | GET /users/{id} | POST /users | GET /admin/stats(鉴权)")
	fmt.Println("========================================")

	// 优雅停机：SIGTERM/SIGINT → http.Server.Shutdown drain in-flight 请求
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		log.InfoTag("main", "server 退出")
	case s := <-sig:
		log.InfoTag("main", "收到信号 %v，正在优雅关闭...", s)
	}
}

// writeJSON 统一 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
