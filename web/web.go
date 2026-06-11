// Package web 是 tgf 的 HTTP 服务核心（G1：HTTP 一等公民）。
//
// 设计原则：
//  1. **自包含、不 import rpc 包**（防 import 环）：本包只依赖标准库 net/http
//     与 tgf 的 log / metrics / trace 三个叶子包。rpc 包单向 import 本包，
//     通过 rpc.Server.WithHTTPService 把"调用后端 RPC 的能力"以 Backend 接口
//     注入进来（见 Options.Backend / BackendFromRequest）。
//  2. **标准库优先**：路由基于 Go 1.22 ServeMux（method+path 模式、{id} 路径
//     参数），不引入第三方路由/Web 框架；中间件形状与 net/http 生态同构。
//  3. **生命周期与 RPC 同编排**：Server.Start 同步建立监听（失败立刻可知），
//     Server.Shutdown 走 http.Server.Shutdown 带超时 drain——由 rpc.Server 的
//     D3 优雅停机序列统一调度（摘流量 → 停 accept → drain HTTP → drain RPC →
//     终末 flush）。
//
// 单独使用（不结合 rpc.Server）的最小示例：
//
//	srv := web.NewServer(web.Options{
//	    Addr: ":8090",
//	    Routes: func(r *web.Router) {
//	        r.GET("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
//	            _, _ = w.Write([]byte(req.PathValue("id")))
//	        })
//	        admin := r.Group("/admin", web.Auth(web.StaticBearerToken("secret")))
//	        admin.GET("/stats", statsHandler)
//	    },
//	    Limiter: web.NewTokenBucketLimiter(1000), // 全局 1000 QPS
//	})
//	if err := srv.Start(); err != nil { ... }
//	defer srv.Shutdown(context.Background())
//
// 结合 rpc.Server（推荐，自动注入 Backend + 共享优雅停机）见
// rpc.Server.WithHTTPService 的文档。
package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/thkhxm/tgf/v2/log"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G1：HTTP Server 封装（Options / 生命周期 / Backend 注入）
//2026/6/10
//***************************************************

// 默认值。Options 对应字段为零值时生效；结合 rpc.WithHTTPService 使用时，
// 端口/超时还会先经配置项（HTTPPort 等，见 tgf/config）填充。
const (
	// DefaultAddr 默认监听地址（与配置项 HTTPPort 的默认值 8090 一致）。
	DefaultAddr = ":8090"
	// DefaultReadHeaderTimeout 读请求头默认超时（防 slowloris）。
	DefaultReadHeaderTimeout = 5 * time.Second
	// DefaultIdleTimeout keep-alive 连接默认空闲超时。
	DefaultIdleTimeout = 60 * time.Second
	// DefaultShutdownTimeout 优雅停机 drain 默认超时。
	DefaultShutdownTimeout = 10 * time.Second
)

// ErrServerAlreadyStarted 表示 Start 被重复调用。
var ErrServerAlreadyStarted = errors.New("tgf/web: server already started")

// ---- Backend：后端 RPC 调用能力的注入点 ----

// Backend 是"HTTP handler 调用后端 RPC"能力的注入接口。
// 本包不知道 RPC 的存在——rpc.Server.WithHTTPService 会把
// rpc.SendRPCMessageByStr（含单进程直通、策略管道、超时、metrics、traceId
// 透传）包装成 Backend 注入进来；HTTP→RPC 桥（G2）也通过本接口接线。
//
// handler 内通过 BackendFromRequest(r) 获取：
//
//	backend, ok := web.BackendFromRequest(req)
//	var reply MyRes
//	err := backend.Invoke(req.Context(), "user", "GetUser", &args, &reply)
type Backend interface {
	// Invoke 同步调用后端 module.method。args / reply 为指针，语义与
	// rpcx service 方法签名 (ctx, args, reply) error 一致。
	Invoke(ctx context.Context, module, method string, args, reply any) error
}

// BackendFunc 是 Backend 的函数适配器。
type BackendFunc func(ctx context.Context, module, method string, args, reply any) error

// Invoke 实现 Backend。
func (f BackendFunc) Invoke(ctx context.Context, module, method string, args, reply any) error {
	return f(ctx, module, method, args, reply)
}

type backendCtxKey struct{}

// WithBackend 把 Backend 挂到 context 上（Server 内置中间件使用；
// 单测/自定义装配也可直接调用）。
func WithBackend(ctx context.Context, b Backend) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, backendCtxKey{}, b)
}

// BackendFromContext 取出注入的 Backend。
func BackendFromContext(ctx context.Context) (Backend, bool) {
	b, ok := ctx.Value(backendCtxKey{}).(Backend)
	return b, ok
}

// BackendFromRequest 是 BackendFromContext(r.Context()) 的便捷入口。
func BackendFromRequest(r *http.Request) (Backend, bool) {
	return BackendFromContext(r.Context())
}

// backendMiddleware 把 Backend 注入每个请求的 context。
func backendMiddleware(b Backend) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithBackend(r.Context(), b)))
		})
	}
}

// ---- Options ----

// Options 是 HTTP 服务的全部构建配置（rpc.Server.WithHTTPService 的参数）。
// 零值字段取默认（见 Default* 常量）；结合 rpc 使用时 Addr / 超时为零还会
// 先经 tgf 配置项（HTTPPort / HTTPReadHeaderTimeoutSec / HTTPShutdownTimeoutSec）填充。
type Options struct {
	// Addr 监听地址，形如 ":8090" / "127.0.0.1:8090"。空 → DefaultAddr。
	Addr string

	// Routes 路由注册回调，NewServer 构造期执行一次。
	// 路由/分组/中间件用法见 Router 文档。
	Routes func(r *Router)

	// Middlewares 追加在内置链之后、路由分发之前的全局中间件
	//（内置链顺序：Trace → AccessLog → Metrics → Recover → RateLimit → Backend 注入）。
	Middlewares []Middleware

	// Limiter 全局限流器（E1 同款令牌桶语义见 NewTokenBucketLimiter，可注入
	// 自定义实现）。nil = 不限流。
	Limiter Limiter

	// Backend 后端 RPC 调用能力（见 Backend 接口注释）。
	// 经 rpc.WithHTTPService 装配时为 nil 会自动注入框架默认实现；
	// 单独使用本包时为 nil 则 handler 内 BackendFromRequest 返回 (nil,false)。
	Backend Backend

	// DisableTrace / DisableAccessLog / DisableMetrics / DisableRecover
	// 关闭对应内置中间件（默认全开）。
	DisableTrace     bool
	DisableAccessLog bool
	DisableMetrics   bool
	DisableRecover   bool

	// ReadHeaderTimeout 读请求头超时；零值 → DefaultReadHeaderTimeout。
	ReadHeaderTimeout time.Duration
	// ReadTimeout / WriteTimeout 整请求读/写超时；零值 = 不限制（标准库语义）。
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	// IdleTimeout keep-alive 空闲超时；零值 → DefaultIdleTimeout。
	IdleTimeout time.Duration
	// ShutdownTimeout 优雅停机 drain 超时；零值 → DefaultShutdownTimeout。
	// rpc.Server.Destroy 用它限定 http.Server.Shutdown 的等待上限。
	ShutdownTimeout time.Duration
}

// normalize 填充零值默认。
func (o *Options) normalize() {
	if o.Addr == "" {
		o.Addr = DefaultAddr
	}
	if o.ReadHeaderTimeout <= 0 {
		o.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.ShutdownTimeout <= 0 {
		o.ShutdownTimeout = DefaultShutdownTimeout
	}
}

// ---- Server ----

// Server 是 HTTP 服务实例：路由 + 中间件链 + http.Server 生命周期。
// 并发安全：Start / Shutdown / Addr 可被不同 goroutine 调用。
type Server struct {
	opts    Options
	router  *Router
	handler http.Handler

	mu      sync.Mutex
	httpSrv *http.Server
	ln      net.Listener
	started bool
	closed  bool
}

// NewServer 构造 HTTP 服务：填默认值 → 建 Router → 执行 Options.Routes →
// 组装内置中间件链。构造后、Start 前仍可通过 Router() 追加路由。
func NewServer(opts Options) *Server {
	opts.normalize()
	s := &Server{opts: opts, router: NewRouter()}
	if opts.Routes != nil {
		opts.Routes(s.router)
	}
	s.handler = s.buildHandler()
	return s
}

// buildHandler 组装服务级中间件链（外 → 内）：
//
//	Trace → AccessLog → Metrics → Recover → RateLimit → Backend 注入 → 用户全局中间件 → 路由
//
// 顺序理由：
//   - Trace 最外：让日志/指标/恢复路径都拿得到 traceId；
//   - AccessLog / Metrics 在 Recover 外：panic 被转成的 500 会被如实记录与计数；
//   - RateLimit 在 Recover 内：429 拒绝同样经过日志与指标；
//   - Backend 注入紧贴路由：业务中间件与 handler 都能取到。
func (s *Server) buildHandler() http.Handler {
	chain := make([]Middleware, 0, 8)
	if !s.opts.DisableTrace {
		chain = append(chain, Trace())
	}
	if !s.opts.DisableAccessLog {
		chain = append(chain, AccessLog())
	}
	if !s.opts.DisableMetrics {
		chain = append(chain, Metrics())
	}
	if !s.opts.DisableRecover {
		chain = append(chain, Recover())
	}
	if s.opts.Limiter != nil {
		chain = append(chain, RateLimit(s.opts.Limiter))
	}
	if s.opts.Backend != nil {
		chain = append(chain, backendMiddleware(s.opts.Backend))
	}
	chain = append(chain, s.opts.Middlewares...)
	return Chain(s.router, chain...)
}

// Router 返回根路由器（Start 前可继续注册路由）。
func (s *Server) Router() *Router { return s.router }

// Handler 返回完整中间件链 + 路由的 http.Handler（httptest 场景直挂）。
func (s *Server) Handler() http.Handler { return s.handler }

// ShutdownTimeout 返回归一化后的优雅停机超时（rpc.Server.Destroy 消费）。
func (s *Server) ShutdownTimeout() time.Duration { return s.opts.ShutdownTimeout }

// Start 同步建立监听并在后台 goroutine 开始服务。
// 监听失败（端口占用等）同步返回 error——启动期问题必须立刻暴露；
// 重复调用返回 ErrServerAlreadyStarted。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrServerAlreadyStarted
	}
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.httpSrv = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       s.opts.ReadTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
	}
	s.started = true
	srv := s.httpSrv
	go func() {
		// Shutdown 触发的退出返回 http.ErrServerClosed——正常停机路径不打错误日志。
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.ErrorTag("web", "HTTP 服务异常退出 addr=%v err=%v", ln.Addr(), serveErr)
		}
	}()
	log.InfoTag("web", "HTTP 服务监听 addr=%v", ln.Addr())
	return nil
}

// Addr 返回实际监听地址（Start 之后是含真实端口的地址，如 ":0" 被内核分配后
// 的 "127.0.0.1:54321"；Start 之前返回配置地址）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.opts.Addr
}

// Shutdown 优雅停机：停止接收新连接并 drain in-flight 请求，直到全部完成或
// ctx 到期。幂等；未 Start 时为安全 no-op。ctx 为 nil 时按 ShutdownTimeout 兜底。
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.started || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	srv := s.httpSrv
	s.mu.Unlock()

	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
		defer cancel()
	}
	return srv.Shutdown(ctx)
}
