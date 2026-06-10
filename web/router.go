package web

import (
	"net/http"
	"strings"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G1：HTTP 路由——基于 Go 1.22 net/http.ServeMux 的薄封装，
//提供 method+path 模式（"GET /users/{id}"）、路由分组（前缀 + 分组中间件）
//与中间件链。本文件不引入任何第三方路由库，路径参数读取直接用标准库
//req.PathValue("id")。
//2026/6/10
//***************************************************

// Middleware 是 HTTP 中间件的统一形状：包装下一跳 handler 并返回新 handler。
// 与标准库完全同构（func(http.Handler) http.Handler），任何第三方生态的
// net/http 中间件都可以直接当 web.Middleware 用。
type Middleware func(next http.Handler) http.Handler

// Chain 把 mws 依次套在 h 外侧：mws[0] 在最外层（最先收到请求）。
// nil 中间件会被跳过（容忍可选项未配置的调用方写法）。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] == nil {
			continue
		}
		h = mws[i](h)
	}
	return h
}

// Router 是路由注册器。一个 Server 持有一个根 Router；Group 派生的子 Router
// 与根共享同一个底层 ServeMux，只是携带了不同的路径前缀与中间件链。
//
// 模式语法（与 Go 1.22 ServeMux 一致）：
//   - "GET /users/{id}"  —— method + 路径参数，handler 内 req.PathValue("id") 读取
//   - "/health"          —— 不限 method
//   - "GET /static/"     —— 以 / 结尾为子树匹配
//   - "GET /{$}"         —— 仅精确匹配根路径
//
// 注意：Use 注册的中间件只对**之后**注册的路由生效（注册时即被烘焙进
// handler 链）——全部 Use 应在 Handle/GET/POST 之前完成。
type Router struct {
	mux    *http.ServeMux
	prefix string
	chain  []Middleware
}

// NewRouter 构造一个独立的根 Router。
// 通常不需要直接调用——web.NewServer 内部会创建并通过 Options.Routes 回调
// 暴露；导出它是为了 httptest 场景下可以单独测路由（Router 实现 http.Handler）。
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux()}
}

// ServeHTTP 让 Router 自身可作为 http.Handler 使用（httptest / 自定义装配）。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Use 追加中间件到当前 Router 的链尾。只影响之后注册的路由（见类型注释）。
func (r *Router) Use(mws ...Middleware) *Router {
	r.chain = append(r.chain, mws...)
	return r
}

// Group 派生一个带路径前缀的子 Router；mws 追加在父链之后（父中间件在外层）。
// 前缀会被规范化为 "/xxx" 形式（自动补前导 /、去尾部 /）；嵌套 Group 前缀累加。
func (r *Router) Group(prefix string, mws ...Middleware) *Router {
	chain := make([]Middleware, 0, len(r.chain)+len(mws))
	chain = append(chain, r.chain...)
	chain = append(chain, mws...)
	return &Router{
		mux:    r.mux,
		prefix: r.prefix + normalizePrefix(prefix),
		chain:  chain,
	}
}

// Handle 注册一条路由。pattern 形如 "GET /users/{id}" 或 "/health"（不限 method）。
// 非法模式（空路径 / 路径不以 / 开头 / 与既有路由冲突）会 panic——与标准库
// ServeMux 的注册语义一致，路由表错误应当在启动期暴露而不是运行期吞掉。
func (r *Router) Handle(pattern string, h http.Handler) {
	if h == nil {
		panic("tgf/web: Handle 收到 nil handler, pattern=" + pattern)
	}
	method, path := splitPattern(pattern)
	if path == "" || !strings.HasPrefix(path, "/") {
		panic("tgf/web: 路由路径必须以 / 开头, pattern=" + pattern)
	}
	full := r.prefix + path
	wrapped := Chain(h, r.chain...)
	if method != "" {
		r.mux.Handle(method+" "+full, wrapped)
		return
	}
	r.mux.Handle(full, wrapped)
}

// HandleFunc 是 Handle 的函数式便捷入口。
func (r *Router) HandleFunc(pattern string, h http.HandlerFunc) {
	r.Handle(pattern, h)
}

// GET / POST / PUT / DELETE / PATCH 是常用 method 的便捷注册方法。
// path 形如 "/users/{id}"。
func (r *Router) GET(path string, h http.HandlerFunc)    { r.Handle(http.MethodGet+" "+path, h) }
func (r *Router) POST(path string, h http.HandlerFunc)   { r.Handle(http.MethodPost+" "+path, h) }
func (r *Router) PUT(path string, h http.HandlerFunc)    { r.Handle(http.MethodPut+" "+path, h) }
func (r *Router) DELETE(path string, h http.HandlerFunc) { r.Handle(http.MethodDelete+" "+path, h) }
func (r *Router) PATCH(path string, h http.HandlerFunc)  { r.Handle(http.MethodPatch+" "+path, h) }

// splitPattern 把 "GET /users/{id}" 拆为 ("GET", "/users/{id}")；
// 无 method 段（首段含 "/"）时返回 ("", pattern)。
func splitPattern(pattern string) (method, path string) {
	p := strings.TrimSpace(pattern)
	if i := strings.IndexByte(p, ' '); i > 0 && !strings.Contains(p[:i], "/") {
		return strings.ToUpper(p[:i]), strings.TrimSpace(p[i+1:])
	}
	return "", p
}

// normalizePrefix 规范化分组前缀：空/"/" → ""；补前导 /；去尾部 /。
func normalizePrefix(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}
