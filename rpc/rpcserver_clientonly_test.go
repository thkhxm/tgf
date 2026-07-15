package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G3 单测：client-only 模式（消费方：Server.Run 的 clientOnly
// 分流 + runClientOnly + validateClientOnly + Destroy 停机序列）——
//  1. 配置冲突校验（service / WithoutServiceClient / WithoutConsul 互斥）；
//  2. Run 后不创建 rpcx server、不监听 rpcx 端口（"不伪装成 RPC 节点"的
//     核心契约），HTTP 服务照常工作；
//  3. 后端不可达时桥接请求返回干净的 503（ServiceNotFound→503 映射），
//     不 panic 不挂死；Destroy 幂等且关闭 HTTP 端口。
//
// "client-only 进程真实调通后端"的端到端链路（真实 Consul + 跨 rpcx 网络）
// 由 it_g_http_test.go 的 integration 用例覆盖。
//2026/6/10
//***************************************************

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2/rpc/internal"
	"github.com/thkhxm/tgf/v2/web"
)

// TestG3_ValidateClientOnly 配置冲突校验表。
func TestG3_ValidateClientOnly(t *testing.T) {
	cases := []struct {
		name    string
		build   func() *Server
		wantErr string // 空 = 期望通过
	}{
		{
			name:  "纯 client-only 合法",
			build: func() *Server { s := newBareServer(); s.clientOnly = true; return s },
		},
		{
			name: "未开启 clientOnly 时不校验",
			build: func() *Server {
				s := newBareServer()
				s.disableConsul = true // 对非 client-only 完全合法
				return s
			},
		},
		{
			name: "携带 service 冲突",
			build: func() *Server {
				s := newBareServer()
				s.clientOnly = true
				s.service = append(s.service, &g2UserService{})
				return s
			},
			wantErr: "不能装载 service",
		},
		{
			name: "WithoutServiceClient 冲突",
			build: func() *Server {
				s := newBareServer()
				s.clientOnly = true
				s.disableClient = true
				return s
			},
			wantErr: "WithoutServiceClient",
		},
		{
			name: "WithoutConsul 冲突",
			build: func() *Server {
				s := newBareServer()
				s.clientOnly = true
				s.disableConsul = true
				return s
			},
			wantErr: "WithoutConsul",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.build().validateClientOnly()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过校验, got err=%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want 包含 %q", err, tc.wantErr)
			}
		})
	}
}

// TestG3_ClientOnly_NoRPCListener_HTTPWorks client-only 核心契约：
// Run 后不创建 rpcx server、rpcx 端口不监听；HTTP 入口照常服务；
// 后端不可达时桥接返回干净 503；Destroy 关闭 HTTP 端口且幂等。
func TestG3_ClientOnly_NoRPCListener_HTTPWorks(t *testing.T) {
	g1Setup(t)
	// discovery 单例隔离：client-only 走真实 UseConsulDiscovery 路径，
	// Consul 指向必然拒绝的端口——client 初始化失败必须是干净降级（D4/D5）。
	internal.ResetDiscoveryForTest()
	t.Cleanup(internal.ResetDiscoveryForTest)
	deadConsulPort := freeF2Port(t)
	setEnvConfigForTest(t, "ConsulAddress", fmt.Sprintf("127.0.0.1:%d", deadConsulPort))

	rpcPort := freeF2Port(t)
	setEnvConfigForTest(t, "ServicePort", fmt.Sprintf("%d", rpcPort))

	s := NewRPCServer()
	s.WithClientOnly()
	s.WithHTTPService(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			r.POST("/api/echo", web.RPC[g2GetUserReq, g2GetUserRes]("g3-nobackend", "GetUser"))
		},
	})

	done := s.Run()
	if done == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	// ---- 1: 不伪装成 RPC 节点 ----
	if s.rpcServer != nil {
		t.Error("client-only 模式不应创建 rpcx server")
	}
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", rpcPort), 300*time.Millisecond); err == nil {
		closeRPCResource(t, "unexpected RPC listener connection", conn)
		t.Error("client-only 模式不应监听 rpcx ServicePort")
	}
	if len(s.httpServers) != 1 {
		t.Fatalf("httpServers = %d, want 1", len(s.httpServers))
	}
	addr := s.httpServers[0].Addr()

	// ---- 2: HTTP 入口照常；后端不可达 → 干净 503（ServiceNotFound 映射），不 panic ----
	resp, body := g2Post(t, addr, "/api/echo", `{"id":"x"}`, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("后端不可达 = (%d, %s), want 503", resp.StatusCode, body)
	}
	var er web.ErrorResponse
	if err := json.Unmarshal([]byte(body), &er); err != nil {
		t.Errorf("错误 envelope 解码失败: %v body=%s", err, body)
	} else if er.Code != http.StatusServiceUnavailable || er.Message == "" {
		t.Errorf("envelope = %+v, want code=503 且 message 非空", er)
	}

	// ---- 3: Destroy 关闭 HTTP 端口 + 幂等 ----
	s.Destroy()
	if conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		closeRPCResource(t, "unexpected HTTP listener connection", conn)
		t.Error("Destroy 后 HTTP 端口应已关闭")
	}
	s.Destroy() // 幂等：不得 panic
}
