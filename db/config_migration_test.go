package db

// E4/E2 · db 包配置读点迁移测试：
// 旧 tgf.GetStrConfig 读点已迁到统一配置系统（tgfconfig.Current()），
// 本文件用表驱动用例锁住各读点的取值逻辑 + 一条 env → Reload → 读点 的
// 端到端回环（证明新读点与环境变量真实联动）。

import (
	"strings"
	"testing"
	"time"

	tgfconfig "github.com/thkhxm/tgf/v2/config"
)

func TestMysqlDSNFromConfig_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		mod  func(c *tgfconfig.Config)
		want string
	}{
		{
			name: "常规配置",
			mod: func(c *tgfconfig.Config) {
				c.MySQL.User = "tim"
				c.MySQL.Password = "secret"
				c.MySQL.Addr = "10.0.0.8"
				c.MySQL.Port = "3307"
				c.MySQL.DB = "game"
			},
			want: "tim:secret@tcp(10.0.0.8:3307)/game?charset=utf8mb4&parseTime=True&loc=Local",
		},
		{
			name: "空密码",
			mod: func(c *tgfconfig.Config) {
				c.MySQL.User = "root"
				c.MySQL.Addr = "127.0.0.1"
				c.MySQL.Port = "3306"
				c.MySQL.DB = "tgf"
			},
			want: "root:@tcp(127.0.0.1:3306)/tgf?charset=utf8mb4&parseTime=True&loc=Local",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &tgfconfig.Config{}
			tc.mod(cfg)
			if got := mysqlDSNFromConfig(cfg); got != tc.want {
				t.Errorf("DSN 不匹配\n got=%s\nwant=%s", got, tc.want)
			}
		})
	}
}

func TestMysqlPoolFromConfig_TableDriven(t *testing.T) {
	cases := []struct {
		name                         string
		idle, open, life             int
		wantIdle, wantOpen, wantLife int
	}{
		{"全部配置", 5, 50, 60, 5, 50, 60},
		{"零值回退历史默认", 0, 0, 0, 10, 200, 300},
		{"负值回退历史默认", -1, -1, -1, 10, 200, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &tgfconfig.Config{}
			cfg.MySQL.MaxIdleConns = tc.idle
			cfg.MySQL.MaxOpenConns = tc.open
			cfg.MySQL.ConnMaxLifetimeSec = tc.life
			i, o, l := mysqlPoolFromConfig(cfg)
			if i != tc.wantIdle || o != tc.wantOpen || l != tc.wantLife {
				t.Errorf("期望 %d/%d/%d，实际 %d/%d/%d",
					tc.wantIdle, tc.wantOpen, tc.wantLife, i, o, l)
			}
		})
	}
}

func TestRedisParamsFromConfig_TableDriven(t *testing.T) {
	cases := []struct {
		name        string
		addr        string
		cluster     bool
		db          int
		wantAddrs   []string
		wantCluster bool
	}{
		{"单节点", "127.0.0.1:6379", false, 2, []string{"127.0.0.1:6379"}, false},
		{"集群多地址", "a:6379,b:6379,c:6379", true, 0, []string{"a:6379", "b:6379", "c:6379"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &tgfconfig.Config{}
			cfg.Redis.Addr = tc.addr
			cfg.Redis.Cluster = tc.cluster
			cfg.Redis.DB = tc.db
			p := redisParamsFromConfig(cfg)
			if strings.Join(p.addrs, ",") != strings.Join(tc.wantAddrs, ",") {
				t.Errorf("addrs 期望 %v，实际 %v", tc.wantAddrs, p.addrs)
			}
			if p.cluster != tc.wantCluster {
				t.Errorf("cluster 期望 %v，实际 %v", tc.wantCluster, p.cluster)
			}
			if p.db != tc.db {
				t.Errorf("db 期望 %d，实际 %d", tc.db, p.db)
			}
		})
	}
}

func TestApplyDefaultCacheTimeouts_TableDriven(t *testing.T) {
	oldCache, oldMem := defaultCacheTimeOut, defaultMemTimeOutSecond
	t.Cleanup(func() {
		defaultCacheTimeOut, defaultMemTimeOutSecond = oldCache, oldMem
	})

	cases := []struct {
		name      string
		cacheSec  int64
		memSec    int64
		wantCache time.Duration
		wantMem   int64
	}{
		{"全部配置", 100, 200, 100 * time.Second, 200},
		{"零值保持现状", 0, 0, 100 * time.Second, 200}, // 上一个 case 的值不被零值覆盖
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &tgfconfig.Config{}
			cfg.DB.CacheTimeoutSec = tc.cacheSec
			cfg.DB.MemTimeoutSec = tc.memSec
			applyDefaultCacheTimeouts(cfg)
			if defaultCacheTimeOut != tc.wantCache {
				t.Errorf("defaultCacheTimeOut 期望 %v，实际 %v", tc.wantCache, defaultCacheTimeOut)
			}
			if defaultMemTimeOutSecond != tc.wantMem {
				t.Errorf("defaultMemTimeOutSecond 期望 %d，实际 %d", tc.wantMem, defaultMemTimeOutSecond)
			}
		})
	}
}

// 端到端回环：设置环境变量 → tgfconfig.Reload() → db 读点拿到新值。
// 证明迁移后的读点与统一配置系统真实联动（而不是只对手工构造的 Config 生效）。
//
// 注意：Reload 语义是"重读 .env.<module>，文件值覆盖进程 env"，db/ 目录下的
// .env.dev 占用了 RedisAddr / RedisDB / MySqlAddr / LogIgnoredTags 四个 key，
// 本用例刻意只用未被该文件占用的 key 做注入。
func TestConfigReadpoints_EnvReloadRoundTrip(t *testing.T) {
	// 先注册恢复钩子：env 被 t.Setenv 还原后再 Reload 一次，
	// 把全局 Current() 复位为进程原始环境的解析结果（Cleanup LIFO：本钩子最后执行）。
	t.Cleanup(func() { _, _ = tgfconfig.Reload() })

	t.Setenv("MySqlUser", "e2user")
	t.Setenv("MySqlPort", "3399")
	t.Setenv("RedisPassword", "pw9")
	t.Setenv("RedisCluster", "yes") // 宽松 bool 写法由配置系统统一规范化
	t.Setenv("DBCacheTimeoutSec", "12345")

	cfg, err := tgfconfig.Reload()
	if err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}

	dsn := mysqlDSNFromConfig(cfg)
	if !strings.Contains(dsn, "e2user:") || !strings.Contains(dsn, ":3399)") {
		t.Errorf("DSN 应包含 env 注入的用户与端口，实际 %s", dsn)
	}
	p := redisParamsFromConfig(cfg)
	if p.password != "pw9" {
		t.Errorf("redis password 应来自 env，实际 %q", p.password)
	}
	if !p.cluster {
		t.Errorf("RedisCluster=yes 应解析为 cluster=true（宽松 bool 契约）")
	}

	oldCache, oldMem := defaultCacheTimeOut, defaultMemTimeOutSecond
	t.Cleanup(func() {
		defaultCacheTimeOut, defaultMemTimeOutSecond = oldCache, oldMem
	})
	applyDefaultCacheTimeouts(cfg)
	if defaultCacheTimeOut != 12345*time.Second {
		t.Errorf("DBCacheTimeoutSec=12345 应生效，实际 %v", defaultCacheTimeOut)
	}
}
