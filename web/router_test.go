package web

// G1 单测：Router——method+path 模式、路径参数、分组前缀、中间件链顺序、
// "Use 只影响之后注册的路由" 契约。

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// doReq 对 handler 发一次请求，返回状态码与 body。
func doReq(t *testing.T, h http.Handler, method, path string, header map[string]string) (int, string, http.Header) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer closeWebResource(t, "response body", resp.Body)
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// TestRouter_MethodAndPathParam 验证 "GET /users/{id}" 模式：
// 路径参数经 req.PathValue 读取；错误 method 返回 405。
func TestRouter_MethodAndPathParam(t *testing.T) {
	r := NewRouter()
	r.GET("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("user:" + req.PathValue("id")))
	})

	if st, body, _ := doReq(t, r, http.MethodGet, "/users/42", nil); st != http.StatusOK || body != "user:42" {
		t.Errorf("GET /users/42 = (%d, %q), want (200, user:42)", st, body)
	}
	if st, _, _ := doReq(t, r, http.MethodPost, "/users/42", nil); st != http.StatusMethodNotAllowed {
		t.Errorf("POST /users/42 状态码 = %d, want 405", st)
	}
	if st, _, _ := doReq(t, r, http.MethodGet, "/nope", nil); st != http.StatusNotFound {
		t.Errorf("GET /nope 状态码 = %d, want 404", st)
	}
}

// TestRouter_VerbHelpers 验证 POST/PUT/DELETE/PATCH 便捷方法注册。
func TestRouter_VerbHelpers(t *testing.T) {
	r := NewRouter()
	mk := func(tag string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tag)) }
	}
	r.POST("/x", mk("post"))
	r.PUT("/x", mk("put"))
	r.DELETE("/x", mk("delete"))
	r.PATCH("/x", mk("patch"))

	for _, tc := range []struct{ method, want string }{
		{http.MethodPost, "post"}, {http.MethodPut, "put"},
		{http.MethodDelete, "delete"}, {http.MethodPatch, "patch"},
	} {
		if st, body, _ := doReq(t, r, tc.method, "/x", nil); st != http.StatusOK || body != tc.want {
			t.Errorf("%s /x = (%d, %q), want (200, %q)", tc.method, st, body, tc.want)
		}
	}
}

// TestRouter_PatternWithoutMethod 验证不带 method 的模式对任意 method 生效。
func TestRouter_PatternWithoutMethod(t *testing.T) {
	r := NewRouter()
	r.HandleFunc("/any", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if st, body, _ := doReq(t, r, m, "/any", nil); st != http.StatusOK || body != "ok" {
			t.Errorf("%s /any = (%d, %q), want (200, ok)", m, st, body)
		}
	}
}

// orderRecorder 记录中间件/handler 的执行顺序（并发安全）。
type orderRecorder struct {
	mu    sync.Mutex
	order []string
}

func (o *orderRecorder) add(tag string) {
	o.mu.Lock()
	o.order = append(o.order, tag)
	o.mu.Unlock()
}

func (o *orderRecorder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.order...)
}

func (o *orderRecorder) reset() {
	o.mu.Lock()
	o.order = nil
	o.mu.Unlock()
}

// mkOrderMW 构造一个把 tag 记入 recorder 的中间件。
func mkOrderMW(rec *orderRecorder, tag string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.add(tag)
			next.ServeHTTP(w, r)
		})
	}
}

// TestRouter_GroupPrefixAndMiddlewareOrder 验证：
//   - Group 前缀正确拼接（含嵌套）；
//   - 中间件执行顺序 = 根链 → 分组链 → handler；
//   - 分组中间件不影响根路由。
func TestRouter_GroupPrefixAndMiddlewareOrder(t *testing.T) {
	rec := &orderRecorder{}
	r := NewRouter()
	r.Use(mkOrderMW(rec, "root"))

	api := r.Group("/api", mkOrderMW(rec, "api"))
	v1 := api.Group("/v1", mkOrderMW(rec, "v1"))
	v1.GET("/ping", func(w http.ResponseWriter, _ *http.Request) {
		rec.add("handler")
		_, _ = w.Write([]byte("pong"))
	})
	r.GET("/top", func(w http.ResponseWriter, _ *http.Request) {
		rec.add("top-handler")
		_, _ = w.Write([]byte("top"))
	})

	if st, body, _ := doReq(t, r, http.MethodGet, "/api/v1/ping", nil); st != http.StatusOK || body != "pong" {
		t.Fatalf("GET /api/v1/ping = (%d, %q), want (200, pong)", st, body)
	}
	want := []string{"root", "api", "v1", "handler"}
	got := rec.snapshot()
	if len(got) != len(want) {
		t.Fatalf("中间件执行顺序 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("中间件执行顺序第 %d 步 = %v, want %v (完整 %v)", i, got[i], want[i], got)
		}
	}

	// 根路由不应执行分组中间件
	rec.reset()
	if st, _, _ := doReq(t, r, http.MethodGet, "/top", nil); st != http.StatusOK {
		t.Fatalf("GET /top 状态码 = %d, want 200", st)
	}
	got = rec.snapshot()
	if len(got) != 2 || got[0] != "root" || got[1] != "top-handler" {
		t.Errorf("根路由执行链 = %v, want [root top-handler]", got)
	}
}

// TestRouter_UseAfterHandleNotApplied 锁定契约：Use 只影响之后注册的路由。
func TestRouter_UseAfterHandleNotApplied(t *testing.T) {
	rec := &orderRecorder{}
	r := NewRouter()
	r.GET("/before", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("b")) })
	r.Use(mkOrderMW(rec, "late"))
	r.GET("/after", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("a")) })

	doReq(t, r, http.MethodGet, "/before", nil)
	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("先注册的路由不应套上后 Use 的中间件, 实际执行 = %v", got)
	}
	doReq(t, r, http.MethodGet, "/after", nil)
	if got := rec.snapshot(); len(got) != 1 || got[0] != "late" {
		t.Errorf("后注册的路由应套上中间件, 实际执行 = %v", got)
	}
}

// TestRouter_InvalidPatternPanics 验证非法模式在注册期 panic（启动期暴露路由表错误）。
func TestRouter_InvalidPatternPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(r *Router)
	}{
		{"路径不以/开头", func(r *Router) { r.HandleFunc("GET nope", okHandlerFunc) }},
		{"nil handler", func(r *Router) { r.Handle("GET /x", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("非法注册应 panic")
				}
			}()
			tc.fn(NewRouter())
		})
	}
}

func okHandlerFunc(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}
