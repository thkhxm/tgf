//go:build integration
// +build integration

package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F2 · Server.Run 注册时序真实 Consul 验收（testcontainers）
//
// 覆盖 F2 验收口径（roadmap 3.3：滚动重启零请求落到死节点）：
//   1. Startup 阻塞期间节点绝不出现在 Consul（旧时序"先注册后 Startup"下
//      这里必然可见）；Startup 放行后节点出现的瞬间监听必须可连接——
//      "节点在 Consul 可见 ⇒ 必然可服务"；同时 agent TTL health check passing；
//      Destroy 后 KV 与 agent service 双双摘除。
//   2. Startup 失败的服务在观察窗口内绝不出现在 Consul（无残留注册），
//      同节点其余正常服务不受影响。
//
//2026/6/10
//***************************************************

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	consulclient "github.com/thkhxm/rpcx-consul/v2/client"
)

// itF2BlockingService Startup 阻塞到 release 关闭——用于钉死"未 Startup 完成
// 绝不发布"的时序断言。
type itF2BlockingService struct {
	Module
	release     chan struct{}
	releaseOnce sync.Once
}

func (s *itF2BlockingService) GetName() string    { return "it-f2-order" }
func (s *itF2BlockingService) GetVersion() string { return "1.0" }
func (s *itF2BlockingService) Startup() (bool, error) {
	<-s.release
	return true, nil
}
func (s *itF2BlockingService) unblock() {
	s.releaseOnce.Do(func() { close(s.release) })
}

// itF2BrokenService Startup 必然失败。
type itF2BrokenService struct {
	Module
}

func (s *itF2BrokenService) GetName() string    { return "it-f2-broken" }
func (s *itF2BrokenService) GetVersion() string { return "1.0" }
func (s *itF2BrokenService) Startup() (bool, error) {
	return false, fmt.Errorf("injected startup failure")
}

// itF2KVHasNode 查询模块的 KV 发现结果是否包含指定节点。
func itF2KVHasNode(d *consulclient.ConsulDiscovery, serviceAddr string) bool {
	for _, kv := range d.GetServices() {
		if kv.Key == serviceAddr {
			return true
		}
	}
	return false
}

func TestIT_F2_ServeReadyBeforeRegister_DestroyCleansAll(t *testing.T) {
	env := ensureRPCIT(t)

	port := freeITPort(t)
	setITServicePort(t, port)
	hostPort := fmt.Sprintf("127.0.0.1:%d", port)
	serviceAddr := "tcp@" + hostPort
	// 与 internal/health.go buildRegistration 的 ID 规则一致：<base>-<host>:<port>。
	healthServiceID := fmt.Sprintf("%s-%s", "tgf_it_rpc", hostPort)
	healthCheckID := healthServiceID + "-ttl"

	svc := &itF2BlockingService{release: make(chan struct{})}
	t.Cleanup(svc.unblock) // 断言失败时也要放行，避免 Run goroutine 永久泄漏

	s := NewRPCServer()
	s.WithCustomServiceAddress() // 绑定 ServiceAddress=127.0.0.1（harness 已设）
	s.WithoutServiceClient()
	s.WithHealthCheck(time.Second) // TTL = 3s
	s.WithService(svc)
	t.Cleanup(s.Destroy)

	runReturned := make(chan struct{})
	go func() {
		_ = s.Run() // Startup 阻塞期间 Run 同步阻塞——正是要验证的窗口
		close(runReturned)
	}()

	d, err := consulclient.NewConsulDiscovery(itRPCConsulBasePath, "it-f2-order", []string{env.consulAddr}, nil)
	if err != nil {
		t.Fatalf("创建客户端 discovery 失败: %v", err)
	}
	defer d.Close()

	// ---- 1a. Startup 未完成：节点绝不能出现在 Consul（观察 2s 窗口）----
	// 旧时序（先 RegisterName 写 KV → 再 Startup）下，这个窗口内节点必然可见。
	probeDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(probeDeadline) {
		if itF2KVHasNode(d, serviceAddr) {
			t.Fatal("Startup 尚未完成节点就被发布到 Consul——违反 F2 注册时序")
		}
		select {
		case <-runReturned:
			t.Fatal("Startup 阻塞期间 Run 不应返回")
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}

	// ---- 1b. 放行 Startup → 节点出现 → 出现瞬间必须可连接 ----
	svc.unblock()
	rpcITWaitUntil(t, 30*time.Second, "Startup 放行后节点出现在 Consul", func() bool {
		return itF2KVHasNode(d, serviceAddr)
	})
	conn, derr := net.DialTimeout("tcp", hostPort, 2*time.Second)
	if derr != nil {
		t.Fatalf("节点已在 Consul 可见但不可连接——死节点窗口仍存在: %v", derr)
	}
	_ = conn.Close()
	<-runReturned

	// ---- 1c. agent TTL health check 已注册且 passing（心跳续约真实生效）----
	apiCli, err := api.NewClient(&api.Config{Address: env.consulAddr})
	if err != nil {
		t.Fatalf("创建 consul api client 失败: %v", err)
	}
	rpcITWaitUntil(t, 15*time.Second, "TTL health check passing", func() bool {
		checks, cerr := apiCli.Agent().Checks()
		if cerr != nil {
			return false
		}
		c, ok := checks[healthCheckID]
		return ok && c.Status == api.HealthPassing
	})
	// 跨越一个 TTL 周期仍 passing：证明是心跳续约在维持，不是注册初值的余晖。
	time.Sleep(4 * time.Second) // > TTL 3s
	checks, cerr := apiCli.Agent().Checks()
	if cerr != nil {
		t.Fatalf("查询 agent checks 失败: %v", cerr)
	}
	if c, ok := checks[healthCheckID]; !ok || c.Status != api.HealthPassing {
		t.Fatalf("跨 TTL 周期后 check 应仍 passing(心跳续约维持), got %+v", c)
	}

	// ---- 1d. Destroy → KV 节点 + agent TTL service 双双摘除 ----
	s.Destroy()
	rpcITWaitUntil(t, 20*time.Second, "Destroy 后 KV 节点消失", func() bool {
		return !itF2KVHasNode(d, serviceAddr)
	})
	rpcITWaitUntil(t, 10*time.Second, "Destroy 后 agent TTL service 摘除", func() bool {
		services, serr := apiCli.Agent().Services()
		if serr != nil {
			return false
		}
		_, ok := services[healthServiceID]
		return !ok
	})
}

func TestIT_F2_StartupFailure_NoResidueInConsul(t *testing.T) {
	env := ensureRPCIT(t)

	port := freeITPort(t)
	setITServicePort(t, port)
	serviceAddr := fmt.Sprintf("tcp@127.0.0.1:%d", port)

	s := NewRPCServer()
	s.WithCustomServiceAddress()
	s.WithoutServiceClient()
	s.WithService(&itF2BrokenService{})
	t.Cleanup(s.Destroy)
	if done := s.Run(); done == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}

	dBroken, err := consulclient.NewConsulDiscovery(itRPCConsulBasePath, "it-f2-broken", []string{env.consulAddr}, nil)
	if err != nil {
		t.Fatalf("创建 discovery 失败: %v", err)
	}
	defer dBroken.Close()
	dMonitor, err := consulclient.NewConsulDiscovery(itRPCConsulBasePath, "monitor", []string{env.consulAddr}, nil)
	if err != nil {
		t.Fatalf("创建 monitor discovery 失败: %v", err)
	}
	defer dMonitor.Close()

	// 同节点的 monitor 服务（Startup 恒成功）正常注册——证明节点本身在发布，
	// 失败服务是被精确剔除而非整体没注册。
	rpcITWaitUntil(t, 20*time.Second, "同节点 monitor 服务正常注册", func() bool {
		return itF2KVHasNode(dMonitor, serviceAddr)
	})

	// Startup 失败的服务在观察窗口内绝不出现（注册动作在 Startup 之后，
	// 此刻 Run 已返回——若会发布早已发布；窗口 3s 足够覆盖 KV 传播）。
	probeDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(probeDeadline) {
		if itF2KVHasNode(dBroken, serviceAddr) {
			t.Fatal("Startup 失败的服务残留在 Consul——违反 F2 无残留契约")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
