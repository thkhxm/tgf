// tgf v3 / G 档示例 ①：纯 REST API（常规 http web 服务）
//
// 演示把 tgf 当一个**普通 HTTP web 框架**用——不接任何游戏 RPC 后端，
// 只用 WithHTTPService 起一个标准 REST 服务，展示一等公民 HTTP 能力的基础面：
//
//   - WithHTTPService(web.Options{...})  → HTTP 与 RPC 平级的服务构建器
//   - web.Router 路由：GET/POST + 路径参数 {id}（Go 1.22 ServeMux 语义）
//   - 内置中间件链：Trace（X-Trace-Id）/ AccessLog / Metrics / Recover 默认全开
//   - Options.Limiter：全局令牌桶限流（E1 同款语义），超限自动 429
//   - 自定义中间件：Options.Middlewares 追加在内置链之后
//   - 鉴权分组：r.Group("/admin", web.Auth(web.StaticBearerToken(token)))
//   - 与 RPC 共享 D3 优雅停机：Run() 启动监听、收到信号后 Destroy() 优雅 drain
//
// 这里用 WithStandalone()——纯 web 进程不需要 Consul，也不启动 RPC client watch。
// 端口默认读配置 HTTPPort（8090）；本示例显式给 Addr 便于本地直跑。
//
// 用法：
//
//	cd tgf/example/http_rest
//	go run .
//	# 然后：
//	curl -i http://127.0.0.1:8090/health
//	curl -i http://127.0.0.1:8090/users/42
//	curl -i -X POST http://127.0.0.1:8090/users -d '{"name":"tim"}'
//	curl -i http://127.0.0.1:8090/admin/stats                       # 401（无 token）
//	curl -i -H "Authorization: Bearer s3cr3t" http://127.0.0.1:8090/admin/stats  # 200
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/web"
)

// adminToken 演示用的静态管理口令。
// 生产应从配置/密钥管理读（web.BearerToken(provider) 支持每次请求动态取，便于热轮换）。
const adminToken = "s3cr3t"

// ====================================================================
// 1. 简单的内存数据 + 业务 handler（纯 web，无 RPC 后端）
// ====================================================================

// userStore 是演示用的进程内"数据库"。真实项目这里会是 DB / 缓存 / 后端 RPC。
type userStore struct {
	seq  atomic.Int64
	hits atomic.Int64 // 访问计数，给 /admin/stats 用
}

type userDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// getUser 处理 GET /users/{id}：从路径参数取 id 并回 JSON。
func (s *userStore) getUser(w http.ResponseWriter, r *http.Request) {
	s.hits.Add(1)
	id := r.PathValue("id") // Go 1.22 ServeMux 路径参数
	writeJSON(w, http.StatusOK, userDTO{ID: parseID(id), Name: "user-" + id})
}

// createUser 处理 POST /users：解析 body，分配 id 后回 201。
func (s *userStore) createUser(w http.ResponseWriter, r *http.Request) {
	s.hits.Add(1)
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name 必填"})
		return
	}
	id := s.seq.Add(1)
	writeJSON(w, http.StatusCreated, userDTO{ID: id, Name: in.Name})
}

// stats 处理 GET /admin/stats（受 Bearer 鉴权保护）。
func (s *userStore) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"created": s.seq.Load(),
		"hits":    s.hits.Load(),
		"traceId": r.Header.Get(web.HeaderTraceID), // Trace 中间件已注入响应头
	})
}

// ====================================================================
// 2. 一个自定义中间件（演示 Options.Middlewares）
// ====================================================================

// serverHeader 给每个响应加一个 Server 头——展示业务自定义中间件如何接入。
// web.Middleware 与 net/http 生态完全同构（func(http.Handler) http.Handler），
// 任何第三方中间件都能直接当 web.Middleware 用。
func serverHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "tgf-web")
		next.ServeHTTP(w, r)
	})
}

// ====================================================================
// 3. main
// ====================================================================

func main() {
	store := &userStore{}

	// 构建并启动：HTTP 作为一等公民挂在 rpc.Server 上，共享 D3 优雅停机。
	done := rpc.NewRPCServer().
		// 纯 web 进程：不挂 Consul、不启动 RPC client watch。
		WithStandalone().
		// 无持久化需求，关闭缓存层避免 db.Run 尝试连 Redis/MySQL。
		WithCache(tgf.CacheModuleClose).
		// 装载 HTTP 服务（可多次调用挂多个端口的 HTTP 实例）。
		WithHTTPService(web.Options{
			Addr: ":8090", // 省略则读配置 HTTPPort（默认 8090）
			Routes: func(r *web.Router) {
				// 健康检查（不限 method，无鉴权）——容器/网关探活用。
				r.GET("/health", func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
				})
				// REST 资源路由。
				r.GET("/users/{id}", store.getUser)
				r.POST("/users", store.createUser)

				// 鉴权分组：/admin/* 全部要求 Bearer 口令。
				// Auth 语义：未配置口令 → 503(fail-closed)；带错/缺口令 → 401。
				admin := r.Group("/admin", web.Auth(web.StaticBearerToken(adminToken)))
				admin.GET("/stats", store.stats)
			},
			// 全局限流：1000 QPS 令牌桶（E1 同款），超限自动 429。
			Limiter: web.NewTokenBucketLimiter(1000),
			// 追加在内置链之后、路由之前的自定义中间件。
			Middlewares: []web.Middleware{serverHeader},
			// 优雅停机 drain 上限（也可省略，默认 10s）。
			ShutdownTimeout: 10 * time.Second,
		}).
		Run()

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  tgf v3 G档 · 纯 REST API 示例已启动")
	fmt.Println("  HTTP: http://127.0.0.1:8090")
	fmt.Println("  路由: GET /health | GET /users/{id} | POST /users | GET /admin/stats(鉴权)")
	fmt.Println("  Consul: 无（WithStandalone）")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("  试一试:")
	fmt.Println("    curl -i http://127.0.0.1:8090/health")
	fmt.Println("    curl -i http://127.0.0.1:8090/users/42")
	fmt.Println(`    curl -i -X POST http://127.0.0.1:8090/users -d '{"name":"tim"}'`)
	fmt.Println("    curl -i -H 'Authorization: Bearer s3cr3t' http://127.0.0.1:8090/admin/stats")
	fmt.Println()

	// 优雅关闭：收到 SIGINT/SIGTERM 后，框架的 Destroy 链会优雅 drain HTTP（http.Server.Shutdown）。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		log.InfoTag("http_rest", "server 退出")
	case s := <-sig:
		log.InfoTag("http_rest", "收到信号 %v，正在优雅关闭...", s)
	}
}

// ---- 小工具 ----

// writeJSON 写 JSON 响应（先写状态码再写 body，与 net/http 语义一致）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// parseID 把字符串 id 转 int64（解析失败回 0，演示用容错）。
func parseID(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
