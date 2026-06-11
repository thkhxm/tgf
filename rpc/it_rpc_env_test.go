//go:build integration
// +build integration

package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · rpc 包集成测试容器 harness（Consul + Redis）
//
// 为 rpc 包的真实依赖集成测试（Server.Run 全链路、登录锁协调器、admin 控制面）
// 提供共享容器环境：
//   - Consul（服务注册/发现/反注册）
//   - Redis（登录锁 / gate owner meta）
//   - MySQL 不在 rpc 集成范围（db 包已覆盖），指向必然拒绝的端口 fail-fast。
//
// Docker 不可用时 Skip；TGF_IT_REQUIRE_DOCKER=1 升级为 Fatal（防 CI 静默绿）。
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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
)

// rpcITFixedHostPort 把容器端口绑定到固定 host 端口（同 db harness 的取舍，
// 见 db/it_env_test.go fixedHostPortModifier 注释）。
func rpcITFixedHostPort(containerPort string, hostPort int) func(*dockercontainer.HostConfig) {
	return func(hc *dockercontainer.HostConfig) {
		if hc.PortBindings == nil {
			hc.PortBindings = nat.PortMap{}
		}
		hc.PortBindings[nat.Port(containerPort)] = []nat.PortBinding{
			{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", hostPort)},
		}
	}
}

const itRPCConsulBasePath = "/tgf_it_rpc"

type rpcITEnv struct {
	consulC testcontainers.Container
	redisC  testcontainers.Container

	consulAddr string
	redisAddr  string

	err error
}

var (
	rpcITOnce   sync.Once
	rpcITShared *rpcITEnv
)

func ensureRPCIT(t *testing.T) *rpcITEnv {
	t.Helper()
	rpcITOnce.Do(func() {
		rpcITShared = bootRPCIT()
	})
	if rpcITShared.err != nil {
		if os.Getenv("TGF_IT_REQUIRE_DOCKER") == "1" {
			t.Fatalf("rpc 集成环境启动失败(TGF_IT_REQUIRE_DOCKER=1): %v", rpcITShared.err)
		}
		t.Skipf("rpc 集成环境不可用(需要 Docker), 跳过: %v", rpcITShared.err)
	}
	return rpcITShared
}

// freeITPort 申请一个空闲 TCP 端口（容器固定 host 端口 / rpcx 监听端口用）。
func freeITPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func bootRPCIT() *rpcITEnv {
	env := &rpcITEnv{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	consulPort, err := freePortNoT()
	if err != nil {
		env.err = err
		return env
	}
	redisPort, err := freePortNoT()
	if err != nil {
		env.err = err
		return env
	}
	// MySQL 不起容器：分配后立刻释放的端口必然连接拒绝，db.Run 的 mysql ping fail-fast。
	mysqlDeadPort, err := freePortNoT()
	if err != nil {
		env.err = err
		return env
	}

	consulC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              "hashicorp/consul:1.18",
			ExposedPorts:       []string{"8500/tcp"},
			HostConfigModifier: rpcITFixedHostPort("8500/tcp", consulPort),
			WaitingFor:         wait.ForListeningPort("8500/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		env.err = fmt.Errorf("启动 consul 容器失败(Docker 可用吗?): %w", err)
		return env
	}
	env.consulC = consulC
	env.consulAddr = fmt.Sprintf("127.0.0.1:%d", consulPort)
	if err = waitRPCConsulLeader(env.consulAddr, time.Minute); err != nil {
		env.err = err
		return env
	}

	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              "redis:7-alpine",
			ExposedPorts:       []string{"6379/tcp"},
			HostConfigModifier: rpcITFixedHostPort("6379/tcp", redisPort),
			WaitingFor:         wait.ForListeningPort("6379/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		env.err = fmt.Errorf("启动 redis 容器失败: %w", err)
		return env
	}
	env.redisC = redisC
	env.redisAddr = fmt.Sprintf("127.0.0.1:%d", redisPort)

	os.Setenv("ConsulAddress", env.consulAddr)
	os.Setenv("ConsulPath", itRPCConsulBasePath)
	os.Setenv("RedisAddr", env.redisAddr)
	os.Setenv("RedisPassword", "")
	os.Setenv("RedisDB", "1")
	os.Setenv("RedisCluster", "false")
	os.Setenv("MySqlAddr", "127.0.0.1")
	os.Setenv("MySqlPort", fmt.Sprintf("%d", mysqlDeadPort))
	os.Setenv("ServiceAddress", "127.0.0.1")
	tgfconfig.RegisterEnvFileLoader(nil)
	if _, rerr := tgfconfig.Reload(); rerr != nil {
		env.err = fmt.Errorf("config.Reload 失败: %w", rerr)
		return env
	}
	return env
}

func freePortNoT() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("申请空闲端口失败: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitRPCConsulLeader(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s/v1/status/leader", addr)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			body := make([]byte, 64)
			n, _ := resp.Body.Read(body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && n > 2 {
				return nil
			}
			lastErr = fmt.Errorf("leader 未就绪 status=%d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("consul 选主等待超时: %w", lastErr)
}

// rpcITWaitUntil 通用轮询断言。
func rpcITWaitUntil(t *testing.T, timeout time.Duration, what string, fn func() bool) {
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

// setITServicePort 把 rpcx 监听端口指到 port 并 Reload（每个会调 Run 的用例
// 必须用独立端口——Run 的 Serve goroutine 在端口冲突时会 os.Exit(1) 杀掉测试进程）。
func setITServicePort(t *testing.T, port int) {
	t.Helper()
	os.Setenv("ServicePort", fmt.Sprintf("%d", port))
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("config.Reload 失败: %v", err)
	}
}
