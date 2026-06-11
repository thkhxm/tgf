//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · 集成测试容器 harness（testcontainers-go）
//
// 本文件为 db 包的真实依赖集成测试提供共享基础设施：
//   - 通过 testcontainers-go 启动真实的 Redis(redis:7-alpine) 与 MySQL(mysql:8.0) 容器；
//   - 把容器映射地址写进进程 env 并 config.Reload()，随后 Run() 让框架以
//     生产路径连接真实依赖（不是 mock / miniredis / sqlmock）；
//   - 为"三断故障注入"提供 stop/start 容器原语——host 端口在容器创建时固定，
//     stop/start 后端口不变，框架客户端（go-redis / database/sql 连接池）得以
//     在恢复后自动重连，验证"断后不 panic、恢复后自愈"。
//
// Docker 不可用时所有集成测试 t.Skip（CI 的 integration job 带 Docker，必跑）；
// 设置 TGF_IT_REQUIRE_DOCKER=1 可把 Skip 升级为 Fatal（防 CI 静默绿）。
//
//2026/6/10
//***************************************************

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/thkhxm/tgf/v2"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
)

// fixedHostPortModifier 把容器端口绑定到固定的本机 host 端口。
// 不用 ExposedPorts 的 "ip:host:container" 写法——testcontainers v0.32 对该写法的
// 端口就绪检查有缺陷（"port is not mapped yet" 误报）；HostConfigModifier 是官方
// 推荐的固定端口方式。固定端口是故障注入的前提：容器 stop/start 后端口不变，
// 客户端才能自动重连验证"自愈"。
func fixedHostPortModifier(containerPort string, hostPort int) func(*dockercontainer.HostConfig) {
	return func(hc *dockercontainer.HostConfig) {
		if hc.PortBindings == nil {
			hc.PortBindings = nat.PortMap{}
		}
		hc.PortBindings[nat.Port(containerPort)] = []nat.PortBinding{
			{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", hostPort)},
		}
	}
}

const (
	itMySQLUser = "root"
	itMySQLPwd  = "it-root-pwd"
	itMySQLDB   = "tgf_it"
)

// itEnv 是 db 包集成测试的共享环境（进程内单例，懒加载）。
type itEnv struct {
	redisC testcontainers.Container
	mysqlC testcontainers.Container

	redisAddr string // host:port
	mysqlHost string
	mysqlPort string

	// rawDB 是绕过框架直连 MySQL 的断言句柄（验证 write-behind 真实落库）。
	rawDB *sql.DB

	err error
}

var (
	itOnce   sync.Once
	itShared *itEnv
)

// ensureIT 懒加载共享容器环境。Docker 不可用时 Skip（或按 env 升级为 Fatal）。
func ensureIT(t *testing.T) *itEnv {
	t.Helper()
	itOnce.Do(func() {
		itShared = bootIT()
	})
	if itShared.err != nil {
		if os.Getenv("TGF_IT_REQUIRE_DOCKER") == "1" {
			t.Fatalf("集成测试环境启动失败(TGF_IT_REQUIRE_DOCKER=1, 不允许跳过): %v", itShared.err)
		}
		t.Skipf("集成测试环境不可用(需要 Docker), 跳过: %v", itShared.err)
	}
	return itShared
}

// freeHostPort 向 OS 申请一个空闲 TCP 端口并立刻释放，
// 用于给容器申请固定 host 端口（stop/start 后端口不变，自愈测试依赖该性质）。
func freeHostPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func bootIT() *itEnv {
	env := &itEnv{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	redisPort, err := freeHostPort()
	if err != nil {
		env.err = fmt.Errorf("申请 redis host 端口失败: %w", err)
		return env
	}
	mysqlPort, err := freeHostPort()
	if err != nil {
		env.err = fmt.Errorf("申请 mysql host 端口失败: %w", err)
		return env
	}

	// ---- Redis 容器（固定 host 端口绑定） ----
	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              "redis:7-alpine",
			ExposedPorts:       []string{"6379/tcp"},
			HostConfigModifier: fixedHostPortModifier("6379/tcp", redisPort),
			WaitingFor:         wait.ForListeningPort("6379/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		env.err = fmt.Errorf("启动 redis 容器失败(Docker 可用吗?): %w", err)
		return env
	}
	env.redisC = redisC
	env.redisAddr = fmt.Sprintf("127.0.0.1:%d", redisPort)

	// ---- MySQL 容器 ----
	mysqlC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              "mysql:8.0",
			ExposedPorts:       []string{"3306/tcp"},
			HostConfigModifier: fixedHostPortModifier("3306/tcp", mysqlPort),
			Env: map[string]string{
				"MYSQL_ROOT_PASSWORD": itMySQLPwd,
				"MYSQL_DATABASE":      itMySQLDB,
			},
			// mysql 镜像初始化期间会先起一个临时 server 再重启，
			// "ready for connections" 必须出现两次才是真正就绪。
			WaitingFor: wait.ForLog("ready for connections").
				WithOccurrence(2).WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		env.err = fmt.Errorf("启动 mysql 容器失败: %w", err)
		return env
	}
	env.mysqlC = mysqlC
	env.mysqlHost = "127.0.0.1"
	env.mysqlPort = fmt.Sprintf("%d", mysqlPort)

	// ---- 框架配置指向容器（统一配置系统是唯一真源，E2 契约） ----
	os.Setenv("RedisAddr", env.redisAddr)
	os.Setenv("RedisPassword", "")
	os.Setenv("RedisDB", "1")
	os.Setenv("RedisCluster", "false")
	os.Setenv("MySqlUser", itMySQLUser)
	os.Setenv("MySqlPwd", itMySQLPwd)
	os.Setenv("MySqlAddr", env.mysqlHost)
	os.Setenv("MySqlPort", env.mysqlPort)
	os.Setenv("MySqlDB", itMySQLDB)
	// 测试不允许 .env 文件覆盖刚注入的容器地址。
	tgfconfig.RegisterEnvFileLoader(nil)
	if _, rerr := tgfconfig.Reload(); rerr != nil {
		env.err = fmt.Errorf("config.Reload 失败: %w", rerr)
		return env
	}

	// ---- 直连 MySQL 的断言句柄 + 建表 ----
	dsn := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?charset=utf8mb4&parseTime=True&loc=Local",
		itMySQLUser, itMySQLPwd, env.mysqlHost, env.mysqlPort, itMySQLDB)
	raw, err := sql.Open("mysql", dsn)
	if err != nil {
		env.err = fmt.Errorf("打开 mysql 断言句柄失败: %w", err)
		return env
	}
	env.rawDB = raw
	if err = pingUntil(raw, 60*time.Second); err != nil {
		env.err = fmt.Errorf("mysql 就绪等待失败: %w", err)
		return env
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS it_player (
			id       varchar(64)  NOT NULL,
			nickname varchar(255) NOT NULL DEFAULT '',
			level    bigint       NOT NULL DEFAULT 0,
			state    tinyint unsigned NOT NULL DEFAULT 1,
			PRIMARY KEY (id)
		)`,
		`CREATE TABLE IF NOT EXISTS it_item (
			user_id varchar(64) NOT NULL,
			prop_id varchar(64) NOT NULL,
			amount  bigint unsigned NOT NULL DEFAULT 0,
			state   tinyint unsigned NOT NULL DEFAULT 1,
			PRIMARY KEY (user_id, prop_id)
		)`,
	} {
		if _, err = raw.Exec(ddl); err != nil {
			env.err = fmt.Errorf("建表失败: %w", err)
			return env
		}
	}

	// ---- 框架以生产路径接入真实依赖 ----
	// 显式指定 Redis 模块：同一测试二进制里其它用例可能改过进程级 cacheModule。
	WithCacheModule(tgf.CacheModuleRedis)
	Run()
	if cache == nil {
		env.err = fmt.Errorf("框架 redis 初始化失败(cache==nil) addr=%v", env.redisAddr)
		return env
	}
	if !dbService.isRunning() {
		env.err = fmt.Errorf("框架 mysql 初始化失败 addr=%v:%v", env.mysqlHost, env.mysqlPort)
		return env
	}
	return env
}

// pingUntil 轮询 Ping 直到成功或超时。
func pingUntil(d *sql.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		last = d.PingContext(ctx)
		cancel()
		if last == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return last
}

// waitUntil 通用轮询断言：fn 在 timeout 内返回 true 即通过，否则 Fatal。
func waitUntil(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("等待超时(%v): %s", timeout, what)
}

// ---- 故障注入原语 ----

// stopRedis 停止 Redis 容器（模拟 Redis 整体宕机）。
func (e *itEnv) stopRedis(t *testing.T) {
	t.Helper()
	d := 10 * time.Second
	if err := e.redisC.Stop(context.Background(), &d); err != nil {
		t.Fatalf("停止 redis 容器失败: %v", err)
	}
}

// startRedis 重新拉起 Redis 容器并等待框架客户端可用（同端口，自动重连）。
func (e *itEnv) startRedis(t *testing.T) {
	t.Helper()
	if err := e.redisC.Start(context.Background()); err != nil {
		t.Fatalf("启动 redis 容器失败: %v", err)
	}
	waitUntil(t, 30*time.Second, "redis 恢复后框架客户端可达", func() bool {
		cli := GetRedisClient()
		if cli == nil {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return cli.Ping(ctx).Err() == nil
	})
}

// stopMySQL 停止 MySQL 容器（模拟 MySQL 整体宕机）。
func (e *itEnv) stopMySQL(t *testing.T) {
	t.Helper()
	d := 20 * time.Second
	if err := e.mysqlC.Stop(context.Background(), &d); err != nil {
		t.Fatalf("停止 mysql 容器失败: %v", err)
	}
}

// startMySQL 重新拉起 MySQL 容器并等待可用（同端口，连接池自动重连）。
func (e *itEnv) startMySQL(t *testing.T) {
	t.Helper()
	if err := e.mysqlC.Start(context.Background()); err != nil {
		t.Fatalf("启动 mysql 容器失败: %v", err)
	}
	if err := pingUntil(e.rawDB, 90*time.Second); err != nil {
		t.Fatalf("mysql 恢复等待失败: %v", err)
	}
}

// queryPlayer 直连 MySQL 查询 it_player 行（断言 write-behind 真实落库）。
func (e *itEnv) queryPlayer(id string) (nickname string, level int64, state int, ok bool) {
	row := e.rawDB.QueryRow("select nickname, level, state from it_player where id = ?", id)
	if err := row.Scan(&nickname, &level, &state); err != nil {
		return "", 0, 0, false
	}
	return nickname, level, state, true
}

// queryItemState 直连 MySQL 查询 it_item 行状态。
func (e *itEnv) queryItemState(userId, propId string) (amount uint64, state int, ok bool) {
	row := e.rawDB.QueryRow("select amount, state from it_item where user_id = ? and prop_id = ?", userId, propId)
	if err := row.Scan(&amount, &state); err != nil {
		return 0, 0, false
	}
	return amount, state, true
}

// ---- 集成测试用模型 ----

// itPlayer 是 write-behind 集成测试模型，对应表 it_player。
type itPlayer struct {
	Model
	Id       string `orm:"pk"`
	Nickname string
	Level    int64
}

func (p *itPlayer) GetTableName() string { return "it_player" }

// itItem 是 hash 管理器集成测试模型，对应表 it_item。
type itItem struct {
	Model
	UserId string `orm:"pk;pkList"`
	PropId string `orm:"pk"`
	Amount uint64
}

func (i *itItem) GetTableName() string                     { return "it_item" }
func (i *itItem) HashCachePkKey(key ...string) string      { return key[0] }
func (i *itItem) HashCacheFieldByVal() string              { return i.PropId }
func (i *itItem) HashCacheFieldByKeys(key ...string) string { return key[1] }
