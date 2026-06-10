//go:build integration
// +build integration

package internal

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · Consul 注册 / 发现 / 反注册 + Consul 断故障注入（真实容器）
//
// 默认 tag 的 discovery_test.go 用 fakeKVStore 桩覆盖了并发/缓存语义；
// 本文件补上真实 Consul 的端到端行为：
//   1. serverplugin 注册（与 RegisterServer 完全同一插件类型）→
//      ConsulDiscovery.RegisterDiscovery 真实发现 → Unregister 后节点消失；
//   2. Consul 宕机：RegisterDiscovery 失败返回 nil 且绝不缓存（D5 修复的
//      真实环境证明）、既有 discovery 不 panic；恢复后重试创建成功（自愈）。
//
// 容器 host 端口创建时固定，stop/start 后不变——恢复后客户端可直连原地址。
//
//2026/6/10
//***************************************************

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/rcrowley/go-metrics"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/thkhxm/rpcx-consul/v2/serverplugin"
	tgfconfig "github.com/thkhxm/tgf/config"
)

// itFixedHostPort 把容器端口绑定到固定 host 端口（同 db harness 的取舍：
// testcontainers v0.32 对 "ip:host:container" 形式 ExposedPorts 的就绪检查有缺陷，
// 且故障注入需要 stop/start 后端口不变）。
func itFixedHostPort(containerPort string, hostPort int) func(*dockercontainer.HostConfig) {
	return func(hc *dockercontainer.HostConfig) {
		if hc.PortBindings == nil {
			hc.PortBindings = nat.PortMap{}
		}
		hc.PortBindings[nat.Port(containerPort)] = []nat.PortBinding{
			{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", hostPort)},
		}
	}
}

const itConsulBasePath = "/tgf_it_internal"

type itConsulEnv struct {
	consulC testcontainers.Container
	addr    string // host:port
	err     error
}

var (
	itConsulOnce sync.Once
	itConsulEnvS *itConsulEnv
)

func ensureConsulIT(t *testing.T) *itConsulEnv {
	t.Helper()
	itConsulOnce.Do(func() {
		itConsulEnvS = bootConsulIT()
	})
	if itConsulEnvS.err != nil {
		if os.Getenv("TGF_IT_REQUIRE_DOCKER") == "1" {
			t.Fatalf("Consul 集成环境启动失败(TGF_IT_REQUIRE_DOCKER=1): %v", itConsulEnvS.err)
		}
		t.Skipf("Consul 集成环境不可用(需要 Docker), 跳过: %v", itConsulEnvS.err)
	}
	return itConsulEnvS
}

func freeConsulHostPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func bootConsulIT() *itConsulEnv {
	env := &itConsulEnv{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	port, err := freeConsulHostPort()
	if err != nil {
		env.err = fmt.Errorf("申请 consul host 端口失败: %w", err)
		return env
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              "hashicorp/consul:1.18",
			ExposedPorts:       []string{"8500/tcp"},
			HostConfigModifier: itFixedHostPort("8500/tcp", port),
			WaitingFor:         wait.ForListeningPort("8500/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		env.err = fmt.Errorf("启动 consul 容器失败(Docker 可用吗?): %w", err)
		return env
	}
	env.consulC = c
	env.addr = fmt.Sprintf("127.0.0.1:%d", port)

	if err = waitConsulLeader(env.addr, time.Minute); err != nil {
		env.err = err
		return env
	}

	// 框架配置指向容器（internal 包经 tgf.GetStrConfig 适配层读统一配置快照）。
	os.Setenv("ConsulAddress", env.addr)
	os.Setenv("ConsulPath", itConsulBasePath)
	tgfconfig.RegisterEnvFileLoader(nil)
	if _, rerr := tgfconfig.Reload(); rerr != nil {
		env.err = fmt.Errorf("config.Reload 失败: %w", rerr)
		return env
	}
	return env
}

// waitConsulLeader 轮询 /v1/status/leader 直到选主完成（dev 模式很快）。
func waitConsulLeader(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s/v1/status/leader", addr)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			body := make([]byte, 64)
			n, _ := resp.Body.Read(body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && n > 2 { // 形如 "127.0.0.1:8300"，空 leader 是 ""
				return nil
			}
			lastErr = fmt.Errorf("leader 未就绪 status=%d body=%s", resp.StatusCode, body[:n])
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("consul 选主等待超时: %w", lastErr)
}

func (e *itConsulEnv) stopConsul(t *testing.T) {
	t.Helper()
	d := 10 * time.Second
	if err := e.consulC.Stop(context.Background(), &d); err != nil {
		t.Fatalf("停止 consul 容器失败: %v", err)
	}
}

func (e *itConsulEnv) startConsul(t *testing.T) {
	t.Helper()
	if err := e.consulC.Start(context.Background()); err != nil {
		t.Fatalf("启动 consul 容器失败: %v", err)
	}
	if err := waitConsulLeader(e.addr, time.Minute); err != nil {
		t.Fatalf("consul 恢复等待失败: %v", err)
	}
}

func itWaitUntil(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("等待超时(%v): %s", timeout, what)
}

// newITRegisterPlugin 构造与 ConsulDiscovery.RegisterServer 同款的注册插件
// （字段一一对应 consul.go RegisterServer 的生产配置）。
// UpdateInterval 必须保持 10s：libkv 的 Consul session TTL 下限是 10s，
// 设小会报 "cannot set or renew session for ttl, unable to operate on sessions"。
func newITRegisterPlugin(t *testing.T, env *itConsulEnv, serviceAddress string) *serverplugin.ConsulRegisterPlugin {
	t.Helper()
	p := &serverplugin.ConsulRegisterPlugin{
		ServiceAddress: serviceAddress,
		ConsulServers:  []string{env.addr},
		BasePath:       itConsulBasePath,
		Metrics:        metrics.NewRegistry(),
		UpdateInterval: time.Second * 10,
	}
	if err := p.Start(); err != nil {
		t.Fatalf("注册插件 Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

// TestIT_Consul_RegisterDiscoverDeregister 注册 → 发现 → 反注册全生命周期。
func TestIT_Consul_RegisterDiscoverDeregister(t *testing.T) {
	env := ensureConsulIT(t)

	const moduleName = "it-module"
	const serviceAddr = "tcp@127.0.0.1:19501"
	plugin := newITRegisterPlugin(t, env, serviceAddr)
	if err := plugin.Register(moduleName, nil, "version=1.0"); err != nil {
		t.Fatalf("服务注册失败: %v", err)
	}

	// 经框架封装的 ConsulDiscovery 真实发现该节点。
	cd := new(ConsulDiscovery)
	cd.initStruct()
	d := cd.RegisterDiscovery(moduleName)
	if d == nil {
		t.Fatal("RegisterDiscovery 返回 nil（Consul 在线时不应失败）")
	}
	t.Cleanup(d.Close)

	itWaitUntil(t, 15*time.Second, "发现已注册节点 "+serviceAddr, func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return true
			}
		}
		return false
	})

	// 幂等：同 moduleName 第二次注册返回同一实例（A6 契约，防连接泄漏）。
	if d2 := cd.RegisterDiscovery(moduleName); d2 != d {
		t.Fatal("同 moduleName 重复 RegisterDiscovery 应复用同一实例")
	}

	// 反注册后节点从发现结果中消失。
	if err := plugin.Unregister(moduleName); err != nil {
		t.Fatalf("Unregister 失败: %v", err)
	}
	itWaitUntil(t, 15*time.Second, "反注册后节点消失", func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return false
			}
		}
		return true
	})
}

// TestIT_Consul_DownNoNilCache_RecoverSelfHeal Consul 断故障注入：
//   - 断前先建好一个 discovery（验证断中读取不 panic）；
//   - 断中 RegisterDiscovery 新模块：返回 nil、绝不把 nil 缓存进 discoveryMap
//     （D5 修复——旧实现 `d, _ :=` 吞错后缓存 nil，恢复后永久故障）；
//   - 恢复后同名模块重试创建成功（自愈），并能发现新注册的节点。
func TestIT_Consul_DownNoNilCache_RecoverSelfHeal(t *testing.T) {
	env := ensureConsulIT(t)

	const aliveModule = "it-alive-module"
	const downModule = "it-down-module"
	const serviceAddr = "tcp@127.0.0.1:19502"

	plugin := newITRegisterPlugin(t, env, serviceAddr)
	if err := plugin.Register(aliveModule, nil, "version=1.0"); err != nil {
		t.Fatalf("服务注册失败: %v", err)
	}

	cd := new(ConsulDiscovery)
	cd.initStruct()
	alive := cd.RegisterDiscovery(aliveModule)
	if alive == nil {
		t.Fatal("断前 RegisterDiscovery 不应失败")
	}
	t.Cleanup(alive.Close)

	// ---- 断 ----
	env.stopConsul(t)
	consulRestored := false
	defer func() {
		if !consulRestored {
			env.startConsul(t)
		}
	}()

	// 断中：既有 discovery 读取不 panic（返回缓存/空集均可，关键是进程活着）。
	_ = alive.GetServices()

	// 断中：新模块创建失败 → 返回 nil 且不缓存（失败可重试，不留永久 nil 占位）。
	if got := cd.RegisterDiscovery(downModule); got != nil {
		t.Fatal("Consul 已断, RegisterDiscovery 不应成功")
	}
	if got := cd.GetDiscovery(downModule); got != nil {
		t.Fatal("失败的 RegisterDiscovery 绝不能把 nil/实例缓存进 discoveryMap（D5 契约）")
	}

	// ---- 恢复 ----
	env.startConsul(t)
	consulRestored = true

	// 自愈：同名模块重试创建成功（旧实现此处因缓存了 nil 而永久失败）。
	var healed bool
	itWaitUntil(t, 30*time.Second, "恢复后 RegisterDiscovery 自愈", func() bool {
		if d := cd.RegisterDiscovery(downModule); d != nil {
			t.Cleanup(d.Close)
			healed = true
			return true
		}
		return false
	})
	if !healed {
		t.Fatal("恢复后 RegisterDiscovery 仍失败")
	}

	// 恢复后注册新节点可被发现（注册插件 TTL 刷新会把 KV 重新写回）。
	if err := plugin.Register(downModule, nil, "version=1.0"); err != nil {
		t.Fatalf("恢复后服务注册失败: %v", err)
	}
	d := cd.GetDiscovery(downModule)
	if d == nil {
		t.Fatal("自愈后的 discovery 应已缓存")
	}
	itWaitUntil(t, 15*time.Second, "恢复后发现新注册节点", func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return true
			}
		}
		return false
	})
}
