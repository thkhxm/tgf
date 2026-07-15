package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G2 端到端测试：HTTP→RPC 桥（web.RPC / web.Invoke）——
// 真实 HTTP 监听 + rpc.Server 装配（消费方：WithHTTPService + defaultWebBackend
// + web.RPC 泛型桥 handler + web.StatusFromError 错误映射 + http_bridge.go
// init 注册的框架哨兵映射）：
//  1. POST JSON body → Req 解码 → 后端 service → Res JSON 响应（单进程直通）；
//  2. traceId 全链路一致（HTTP X-Trace-Id == 后端 ctx 的 traceId）；
//  3. 错误码映射端到端：业务错误→502、tgf.ErrorRPCTimeOut→504、
//     web.WithHTTPStatus→自定义、限流（E1 策略管道 ErrRPCRateLimited）→429；
//  4. 非法 JSON→400；web.Invoke 手写 handler 路径（路径参数场景）。
//
//2026/6/10
//***************************************************

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/trace"
	"github.com/thkhxm/tgf/v2/web"
)

type g2GetUserReq struct {
	Id string `json:"id"`
}

type g2GetUserRes struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// g2ErrTeapot 业务侧用 web.WithHTTPStatus 精确控制桥接响应码的样例错误。
var g2ErrTeapot = web.WithHTTPStatus(errors.New("g2: short and stout"), http.StatusTeapot)

// g2UserService 是 G2 用例的最小后端：按 Id 分流出各种结果/错误形态。
type g2UserService struct {
	Module
	lastTraceID atomic.Value // string
}

func (s *g2UserService) GetName() string        { return "g2user" }
func (s *g2UserService) GetVersion() string     { return "1.0" }
func (s *g2UserService) Startup() (bool, error) { return true, nil }

func (s *g2UserService) GetUser(ctx context.Context, args *g2GetUserReq, reply *g2GetUserRes) error {
	s.lastTraceID.Store(trace.TraceIDFromContext(ctx))
	switch args.Id {
	case "boom":
		return errors.New("g2: 业务异常")
	case "timeout":
		return tgf.ErrorRPCTimeOut
	case "teapot":
		return g2ErrTeapot
	default:
		reply.Id = args.Id
		reply.Name = "user-" + args.Id
		return nil
	}
}

// g2Post 对真实地址发 POST JSON。
func g2Post(t *testing.T, addr, path, body string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s 失败: %v", path, err)
	}
	defer closeRPCResource(t, "HTTP bridge response body", resp.Body)
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestG2_Bridge_EndToEnd_SingleProcess 是 G2 的核心验收用例：
// HTTP 请求 → web.RPC 桥 → 默认 Backend（策略管道/metrics/traceId 透传）→
// 后端 service → JSON 返回，全链路真实 HTTP 监听。
func TestG2_Bridge_EndToEnd_SingleProcess(t *testing.T) {
	g1Setup(t)
	t.Cleanup(ClearMethodPolicies)

	svc := &g2UserService{}
	s := NewRPCServer()
	s.WithSingleProcess()
	s.WithService(svc)
	s.WithHTTPService(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			// 声明式桥：一行注册 REST→RPC 端点（G2 主交付物）。
			r.POST("/api/user", web.RPC[g2GetUserReq, g2GetUserRes]("g2user", "GetUser"))
			// 手写桥：路径参数参与构造 Req（web.Invoke helper）。
			r.GET("/api/user/{id}", func(w http.ResponseWriter, req *http.Request) {
				res, err := web.Invoke[g2GetUserReq, g2GetUserRes](req, "g2user", "GetUser",
					&g2GetUserReq{Id: req.PathValue("id")})
				if err != nil {
					web.WriteError(w, req, web.StatusFromError(err), err.Error())
					return
				}
				_ = web.WriteJSON(w, http.StatusOK, res)
			})
		},
	})

	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)
	addr := s.httpServers[0].Addr()

	// ---- 1: JSON body → 后端 → JSON 响应 ----
	resp, body := g2Post(t, addr, "/api/user", `{"id":"42"}`, map[string]string{web.HeaderTraceID: "g2-trace-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/user = (%d, %s), want 200", resp.StatusCode, body)
	}
	var res g2GetUserRes
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("响应解码失败: %v body=%s", err, body)
	}
	if res.Id != "42" || res.Name != "user-42" {
		t.Errorf("res = %+v, want {42 user-42}", res)
	}
	if ct := resp.Header.Get("Content-Type"); ct != web.ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", ct, web.ContentTypeJSON)
	}

	// ---- 2: traceId 全链路一致（HTTP 头 → 桥 → 后端 ctx）----
	if got, _ := svc.lastTraceID.Load().(string); got != "g2-trace-1" {
		t.Errorf("后端 ctx traceId = %q, want g2-trace-1（HTTP→桥→RPC 链路应同 id）", got)
	}

	// ---- 3: 错误码映射端到端 ----
	errCases := []struct {
		id   string
		want int
	}{
		{"boom", http.StatusBadGateway},        // 未知业务错误 → 502
		{"timeout", http.StatusGatewayTimeout}, // tgf.ErrorRPCTimeOut → 504（http_bridge.go init 注册）
		{"teapot", http.StatusTeapot},          // web.WithHTTPStatus 业务自定义 → 418
	}
	for _, tc := range errCases {
		resp, body = g2Post(t, addr, "/api/user", fmt.Sprintf(`{"id":%q}`, tc.id), nil)
		if resp.StatusCode != tc.want {
			t.Errorf("id=%v → (%d, %s), want %d", tc.id, resp.StatusCode, body, tc.want)
		}
		var er web.ErrorResponse
		if err := json.Unmarshal([]byte(body), &er); err != nil {
			t.Errorf("id=%v 错误 envelope 解码失败: %v body=%s", tc.id, err, body)
			continue
		}
		if er.Code != tc.want || er.Message == "" || er.TraceId == "" {
			t.Errorf("id=%v envelope = %+v, want code=%d 且 message/traceId 非空", tc.id, er, tc.want)
		}
	}

	// ---- 4: 非法 JSON → 400（不抵达后端）----
	resp, body = g2Post(t, addr, "/api/user", `{"id":`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON = (%d, %s), want 400", resp.StatusCode, body)
	}

	// ---- 5: web.Invoke 手写 handler（路径参数）----
	respGet, bodyGet := g1Get(t, addr, "/api/user/77", nil)
	if respGet.StatusCode != http.StatusOK || !strings.Contains(bodyGet, "user-77") {
		t.Errorf("GET /api/user/77 = (%d, %s), want 200 + user-77", respGet.StatusCode, bodyGet)
	}

	// ---- 6: E1 限流策略 → 429（框架哨兵 ErrRPCRateLimited 的映射接线）----
	// qps=1 的令牌桶初始 1 枚令牌：紧贴的两次调用第二次必拒。
	SetMethodPolicy("g2user.GetUser", MethodPolicy{RateLimit: 1})
	resp, _ = g2Post(t, addr, "/api/user", `{"id":"rl-1"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("限流后首个请求 = %d, want 200(桶内有初始令牌)", resp.StatusCode)
	}
	resp, body = g2Post(t, addr, "/api/user", `{"id":"rl-2"}`, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("限流第二个请求 = (%d, %s), want 429(ErrRPCRateLimited→429 映射)", resp.StatusCode, body)
	}
}
