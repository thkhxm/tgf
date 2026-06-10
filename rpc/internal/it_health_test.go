//go:build integration
// +build integration

package internal

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F2 · Consul agent TTL health check 真实环境验收（testcontainers）
//
// 覆盖 F2 验收口径：
//   1. TTL check 全生命周期：注册即 passing → 续约维持 → 停止续约 TTL 内转
//      critical → 续约恢复 passing → Deregister 摘除 → Renew 自愈重注册；
//   2. kill 进程（TerminateProcess，等价 kill -9）：
//      - agent TTL check 在 TTL 内转 critical（健康层的"宕机即发现"）；
//      - rpcx-consul KV 节点随 session TTL 失效消失（路由层的"宕机即摘除"，
//        框架 discovery 走 KV）——ConsulDiscovery.GetServices 不再返回死节点。
//
// 子进程用 go test 标准的 re-exec 模式（TestF2HelperNode + 环境变量门控）。
//
//2026/6/10
//***************************************************

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rcrowley/go-metrics"
	"github.com/thkhxm/rpcx-consul/v2/serverplugin"
)

// itAgentCheckStatus 查询 agent 上指定 checkID 的状态；不存在返回 ""。
func itAgentCheckStatus(t *testing.T, consulAddr, checkID string) string {
	t.Helper()
	cli, err := api.NewClient(&api.Config{Address: consulAddr})
	if err != nil {
		t.Fatalf("创建 consul api client 失败: %v", err)
	}
	checks, err := cli.Agent().Checks()
	if err != nil {
		return ""
	}
	if c, ok := checks[checkID]; ok {
		return c.Status
	}
	return ""
}

// itAgentServiceExists 查询 agent 上指定 serviceID 是否存在。
func itAgentServiceExists(t *testing.T, consulAddr, serviceID string) bool {
	t.Helper()
	cli, err := api.NewClient(&api.Config{Address: consulAddr})
	if err != nil {
		t.Fatalf("创建 consul api client 失败: %v", err)
	}
	services, err := cli.Agent().Services()
	if err != nil {
		return false
	}
	_, ok := services[serviceID]
	return ok
}

// TestIT_TTLHealth_Lifecycle TTL check 全生命周期（注册→续约→过期→恢复→摘除→自愈）。
func TestIT_TTLHealth_Lifecycle(t *testing.T) {
	env := ensureConsulIT(t)

	h, err := NewConsulTTLHealth(TTLHealthOptions{
		ServiceAddress: "127.0.0.1:19601",
		Modules:        []string{"it-ttl-module"},
		Interval:       time.Second, // TTL = 3s
		ConsulAddress:  env.addr,
		BasePath:       itConsulBasePath,
	})
	if err != nil {
		t.Fatalf("NewConsulTTLHealth 失败: %v", err)
	}
	t.Cleanup(func() { _ = h.Deregister() })

	// 1. 注册即 passing（F2 时序保证注册发生在 Serve 就绪后，无需等首次续约）。
	itWaitUntil(t, 10*time.Second, "注册后 check passing", func() bool {
		return itAgentCheckStatus(t, env.addr, h.CheckID()) == api.HealthPassing
	})

	// 2. 续约维持 passing 跨越一个完整 TTL 周期（证明 UpdateTTL 真实生效，
	//    而不是初始 passing 的余晖）。
	for i := 0; i < 5; i++ {
		if err = h.Renew("lifecycle-renew"); err != nil {
			t.Fatalf("第 %d 次续约失败: %v", i, err)
		}
		time.Sleep(time.Second)
	}
	if got := itAgentCheckStatus(t, env.addr, h.CheckID()); got != api.HealthPassing {
		t.Fatalf("持续续约 5s(>TTL 3s) 后 check = %q, want passing", got)
	}

	// 3. 停止续约 → TTL 内转 critical（节点宕机的健康表现）。
	criticalDeadline := 3*time.Second + 5*time.Second // TTL + 轮询余量
	itWaitUntil(t, criticalDeadline, "停止续约后 TTL 内转 critical", func() bool {
		return itAgentCheckStatus(t, env.addr, h.CheckID()) == api.HealthCritical
	})

	// 4. 恢复续约 → passing（critical 不是终态，节点活过来就恢复）。
	if err = h.Renew("recover"); err != nil {
		t.Fatalf("critical 状态下续约失败: %v", err)
	}
	itWaitUntil(t, 5*time.Second, "续约后恢复 passing", func() bool {
		return itAgentCheckStatus(t, env.addr, h.CheckID()) == api.HealthPassing
	})

	// 5. Deregister → agent service / check 消失（优雅停机路径）。
	if err = h.Deregister(); err != nil {
		t.Fatalf("Deregister 失败: %v", err)
	}
	itWaitUntil(t, 5*time.Second, "Deregister 后 service 消失", func() bool {
		return !itAgentServiceExists(t, env.addr, h.ServiceID())
	})

	// 6. check 丢失后 Renew 自愈：重注册 service+check 并续约成功
	//    （对应 Consul agent 重启丢 TTL check 的生产场景）。
	if err = h.Renew("self-heal"); err != nil {
		t.Fatalf("check 丢失后 Renew 应自愈重注册, got err: %v", err)
	}
	itWaitUntil(t, 5*time.Second, "自愈后 check 重新 passing", func() bool {
		return itAgentCheckStatus(t, env.addr, h.CheckID()) == api.HealthPassing
	})
}

// ---- kill 进程验收（re-exec 子进程） ----

const (
	itF2HelperEnv   = "TGF_IT_F2_HELPER"
	itF2KillModule  = "it-kill-module"
	itF2HelperReady = "HELPER_READY"
)

// TestF2HelperNode 不是常规测试——是 TestIT_TTLHealth_ProcessKill_AutoRemove
// re-exec 出来的子进程入口：注册 KV 节点（与生产 RegisterServer 同款插件配置）
// + TTL health（1s 续约 / 3s TTL），打印 HELPER_READY 后驻留等待被 kill。
func TestF2HelperNode(t *testing.T) {
	if os.Getenv(itF2HelperEnv) != "1" {
		t.Skip("仅作为 kill 验收的子进程入口运行")
	}
	consulAddr := os.Getenv("TGF_IT_F2_CONSUL")
	addr := os.Getenv("TGF_IT_F2_ADDR") // host:port
	serviceAddr := "tcp@" + addr

	// KV 注册：UpdateInterval 必须 ≥10s（libkv consul session TTL 下限，
	// 见 newITRegisterPlugin 注释）；session TTL = 2×UpdateInterval = 20s。
	p := &serverplugin.ConsulRegisterPlugin{
		ServiceAddress: serviceAddr,
		ConsulServers:  []string{consulAddr},
		BasePath:       itConsulBasePath,
		Metrics:        metrics.NewRegistry(),
		UpdateInterval: time.Second * 10,
	}
	if err := p.Start(); err != nil {
		fmt.Printf("HELPER_ERR plugin start: %v\n", err)
		os.Exit(1)
	}
	if err := p.Register(itF2KillModule, nil, "version=1.0"); err != nil {
		fmt.Printf("HELPER_ERR register: %v\n", err)
		os.Exit(1)
	}

	h, err := NewConsulTTLHealth(TTLHealthOptions{
		ServiceAddress: addr,
		Modules:        []string{itF2KillModule},
		Interval:       time.Second, // TTL = 3s
		ConsulAddress:  consulAddr,
		BasePath:       itConsulBasePath,
	})
	if err != nil {
		fmt.Printf("HELPER_ERR ttl health: %v\n", err)
		os.Exit(1)
	}
	go func() {
		for {
			time.Sleep(time.Second)
			_ = h.Renew("helper-heartbeat")
		}
	}()

	fmt.Println(itF2HelperReady)
	// 驻留等待父进程 kill；正常不应自然退出。
	time.Sleep(10 * time.Minute)
}

// TestIT_TTLHealth_ProcessKill_AutoRemove kill -9 等价验收：
// 子进程注册（KV + TTL health）→ 父进程硬杀 → 断言
//   a) agent TTL check 在 TTL(3s)+余量 内转 critical / 或 service 已被摘除；
//   b) KV 节点随 session 失效消失（框架 discovery 视角死节点被摘除，
//      session TTL 20s，Consul 失效判定最坏 2×TTL + 传播）。
func TestIT_TTLHealth_ProcessKill_AutoRemove(t *testing.T) {
	env := ensureConsulIT(t)

	port, err := freeConsulHostPort()
	if err != nil {
		t.Fatalf("申请端口失败: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	serviceAddr := "tcp@" + addr
	// 与 health.go buildRegistration 的 ID 规则一致：<base>-<host>:<port>。
	serviceID := fmt.Sprintf("%s-%s", "tgf_it_internal", addr)
	checkID := serviceID + "-ttl"

	cmd := exec.Command(os.Args[0], "-test.run", "^TestF2HelperNode$", "-test.v", "-test.timeout", "15m")
	cmd.Env = append(os.Environ(),
		itF2HelperEnv+"=1",
		"TGF_IT_F2_CONSUL="+env.addr,
		"TGF_IT_F2_ADDR="+addr,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe 失败: %v", err)
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatalf("启动子进程失败: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// 等子进程注册完成。
	readyCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if line == itF2HelperReady {
				readyCh <- nil
				// 继续排空 stdout，防子进程写管道阻塞。
				for scanner.Scan() {
				}
				return
			}
		}
		readyCh <- fmt.Errorf("子进程未输出 %s 即退出", itF2HelperReady)
	}()
	select {
	case rerr := <-readyCh:
		if rerr != nil {
			t.Fatalf("子进程就绪失败: %v", rerr)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("等待子进程就绪超时")
	}

	// 杀前状态：KV 可发现 + TTL check passing。
	cd := new(ConsulDiscovery)
	cd.initStruct()
	d := cd.RegisterDiscovery(itF2KillModule)
	if d == nil {
		t.Fatal("RegisterDiscovery 失败（Consul 在线时不应失败）")
	}
	t.Cleanup(d.Close)
	itWaitUntil(t, 15*time.Second, "kill 前 KV 节点可发现", func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return true
			}
		}
		return false
	})
	itWaitUntil(t, 10*time.Second, "kill 前 TTL check passing", func() bool {
		return itAgentCheckStatus(t, env.addr, checkID) == api.HealthPassing
	})

	// ---- kill -9（TerminateProcess）----
	if err = cmd.Process.Kill(); err != nil {
		t.Fatalf("kill 子进程失败: %v", err)
	}
	_, _ = cmd.Process.Wait()
	killedAt := time.Now()

	// a) TTL(3s) 内转 critical（含轮询/传播余量共 12s）。
	//    DeregisterCriticalServiceAfter(1m 硬下限) 可能先把 service 整个摘掉，
	//    那是更强的结果——两者都算"宕机被发现"。
	itWaitUntil(t, 12*time.Second, "kill 后 TTL 内 check 转 critical(或 service 已摘除)", func() bool {
		st := itAgentCheckStatus(t, env.addr, checkID)
		return st == api.HealthCritical || st == "" // "" = service 已被 reaper 摘除
	})
	t.Logf("kill 后 %.1fs 健康层发现节点死亡", time.Since(killedAt).Seconds())

	// b) KV 节点消失：session TTL 20s，Consul 失效判定允许到 2×TTL，再留传播余量。
	itWaitUntil(t, 90*time.Second, "kill 后 KV 节点随 session 失效消失(死节点摘除)", func() bool {
		for _, kv := range d.GetServices() {
			if kv.Key == serviceAddr {
				return false
			}
		}
		return true
	})
	t.Logf("kill 后 %.1fs 路由层(KV discovery)摘除死节点", time.Since(killedAt).Seconds())
}
