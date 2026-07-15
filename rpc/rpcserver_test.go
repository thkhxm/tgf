//go:build integration
// +build integration

package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description E5 重写：Server.Run 全链路集成测试（真实 Consul）。
//
// 原版（2023/2/23）的问题（V3 审计 P3）：四个测试全部以永不 Done 的
// WaitGroup.Wait() 收尾——永久挂死，整个 integration 套件从未跑完过；
// 且依赖外部手工起的 Consul 与另一个进程里的服务端，非自包含。
//
// 现版本是一条有界端到端用例，覆盖框架对外承诺的完整生命周期：
//   NewRPCServer → WithService → Run（rpcx Serve + Consul 注册）
//   → 真实 rpcx 客户端经 Consul 发现并调用服务方法（断言回包）
//   → Server.Destroy（D3 优雅停机）→ Consul 反注册（节点从发现结果消失）
//
//2023/2/23（E5 重写 2026/6/10）
//***************************************************

import (
	"fmt"
	"testing"
	"time"

	"context"

	consulclient "github.com/thkhxm/rpcx-consul/v2/client"
	rpcxclient "github.com/thkhxm/rpcx/v2/client"
)

// itEchoService 是端到端用例的最小业务服务。
type itEchoService struct {
	Module
}

func (s *itEchoService) GetName() string        { return "it-echo" }
func (s *itEchoService) GetVersion() string     { return "1.0" }
func (s *itEchoService) Startup() (bool, error) { return true, nil }

func (s *itEchoService) RPCEcho(ctx context.Context, args *string, reply *string) error {
	*reply = "echo:" + *args
	return nil
}

func TestIT_ServerRun_RegisterDiscoverCallDeregister(t *testing.T) {
	env := ensureRPCIT(t)

	// 每个调 Run 的用例必须独占端口：Serve 端口冲突会 os.Exit(1) 杀掉测试进程。
	port := freeITPort(t)
	setITServicePort(t, port)
	serviceAddr := fmt.Sprintf("tcp@127.0.0.1:%d", port)

	s := NewRPCServer()
	s.WithCustomServiceAddress() // 绑定 ServiceAddress=127.0.0.1（harness 已设）
	s.WithoutServiceClient()     // 本用例只验服务端生命周期，客户端用裸 rpcx 直连
	s.WithService(&itEchoService{})
	done := s.Run()
	if done == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}

	// ---- 发现：经真实 Consul 看到本节点 ----
	d, err := consulclient.NewConsulDiscovery(itRPCConsulBasePath, "it-echo", []string{env.consulAddr}, nil)
	if err != nil {
		t.Fatalf("创建客户端 discovery 失败: %v", err)
	}
	defer d.Close()

	rpcITWaitUntil(t, 20*time.Second, "Consul 中出现本节点 "+serviceAddr, func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return true
			}
		}
		return false
	})

	// ---- 真实调用：rpcx 客户端经 Consul 发现并调用 RPCEcho ----
	xc := rpcxclient.NewXClient("it-echo", rpcxclient.Failtry, rpcxclient.RandomSelect, d, rpcxclient.DefaultOption)
	args := "ping"
	var reply string
	callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = xc.Call(callCtx, "RPCEcho", &args, &reply); err != nil {
		t.Fatalf("端到端 RPC 调用失败: %v", err)
	}
	if reply != "echo:ping" {
		t.Fatalf("回包不符: got %q, want %q", reply, "echo:ping")
	}
	// 先断客户端连接，避免 Destroy 的 drain 阶段等到超时。
	_ = xc.Close()

	// ---- 优雅停机：Destroy → Consul 反注册，节点从发现结果消失 ----
	s.Destroy()
	rpcITWaitUntil(t, 20*time.Second, "Destroy 后节点从 Consul 消失", func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return false
			}
		}
		return true
	})

	// Destroy 幂等：重复调用不得 panic。
	s.Destroy()
}
