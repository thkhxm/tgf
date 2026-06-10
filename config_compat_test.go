package tgf

// E2 配置收敛的端到端兼容性测试（验收标准：同一 env 变量在新旧 API 读取
// 结果逐项一致的表驱动测试）。
//
// 测试环境说明：tgf 包测试进程的 init() 已执行 InitConfig（加载 .env.dev +
// 唯一 parse pass + 注册 env 文件 loader）。一致性测试通过 t.Setenv 注入值后
// 调 config.Reload() 重新解析；为避免 loader 把 .env.dev 的值 Overload 回来
// 覆盖 t.Setenv 注入值，测试开头会注销 loader（RegisterEnvFileLoader(nil)）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
	tgfconfig "github.com/thkhxm/tgf/config"
)

// boolToInt 把新 API 的 bool 字段映射为旧 API GetStrConfig[int] 的语义。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// disableEnvFileLoader 注销 InitConfig 注入的 .env 文件 loader，并在测试结束后
// 把快照刷回真实环境（t.Setenv 的恢复先于本 cleanup 执行，Reload 读到的是
// 测试前的进程环境）。
func disableEnvFileLoader(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		tgfconfig.RegisterEnvFileLoader(nil)
		_, _ = tgfconfig.Reload()
	})
	tgfconfig.RegisterEnvFileLoader(nil)
}

// TestGetStrConfig_OldNewConsistency 对 config.Config 登记的全部配置项逐项验证：
// 同一个环境变量注入后，旧 GetStrConfig（按业务实际使用的类型读取）与新
// config.Current() 字段读到完全一致的结果。
func TestGetStrConfig_OldNewConsistency(t *testing.T) {
	disableEnvFileLoader(t)

	cases := []struct {
		name   string
		key    Environment
		val    string
		oldGet func() any
		newGet func(c *tgfconfig.Config) any
	}{
		// ---- Logger ----
		{"LogPath", EnvironmentLoggerPath, "/tmp/x.log",
			func() any { return GetStrConfig[string](EnvironmentLoggerPath) },
			func(c *tgfconfig.Config) any { return c.Logger.Path }},
		{"LogLevel", EnvironmentLoggerLevel, "warn",
			func() any { return GetStrConfig[string](EnvironmentLoggerLevel) },
			func(c *tgfconfig.Config) any { return c.Logger.Level }},
		{"LogIgnoredTags", EnvironmentLoggerIgnoredTags, "trace,login",
			func() any { return GetStrConfig[string](EnvironmentLoggerIgnoredTags) },
			func(c *tgfconfig.Config) any { return c.Logger.IgnoredTags }},
		{"LogMaxSize", EnvironmentLoggerMaxSize, "256",
			func() any { return GetStrConfig[int](EnvironmentLoggerMaxSize) },
			func(c *tgfconfig.Config) any { return c.Logger.MaxSize }},
		{"LogMaxAge", EnvironmentLoggerMaxAge, "7",
			func() any { return GetStrConfig[int](EnvironmentLoggerMaxAge) },
			func(c *tgfconfig.Config) any { return c.Logger.MaxAge }},
		{"LogMaxBackups", EnvironmentLoggerMaxBackups, "50",
			func() any { return GetStrConfig[int](EnvironmentLoggerMaxBackups) },
			func(c *tgfconfig.Config) any { return c.Logger.MaxBackups }},
		{"LogCompress bool宽松写法", EnvironmentLoggerCompress, "true",
			func() any { return GetStrConfig[int](EnvironmentLoggerCompress) },
			func(c *tgfconfig.Config) any { return boolToInt(c.Logger.Compress) }},
		{"LogLocalTime bool宽松写法", EnvironmentLoggerLocalTime, "off",
			func() any { return GetStrConfig[int](EnvironmentLoggerLocalTime) },
			func(c *tgfconfig.Config) any { return boolToInt(c.Logger.LocalTime) }},
		{"LogTimeFormat", EnvironmentLoggerTimeFormat, "2006/01/02",
			func() any { return GetStrConfig[string](EnvironmentLoggerTimeFormat) },
			func(c *tgfconfig.Config) any { return c.Logger.TimeFormat }},
		{"LogServiceFile", EnvironmentLoggerServiceFile, "svc/s.log",
			func() any { return GetStrConfig[string](EnvironmentLoggerServiceFile) },
			func(c *tgfconfig.Config) any { return c.Logger.ServiceFile }},
		{"LogDBFile", EnvironmentLoggerDBFile, "dbx/d.log",
			func() any { return GetStrConfig[string](EnvironmentLoggerDBFile) },
			func(c *tgfconfig.Config) any { return c.Logger.DBFile }},
		// ---- Redis ----
		{"RedisAddr", EnvironmentRedisAddr, "10.1.2.3:6390",
			func() any { return GetStrConfig[string](EnvironmentRedisAddr) },
			func(c *tgfconfig.Config) any { return c.Redis.Addr }},
		{"RedisPassword", EnvironmentRedisPassword, "p@ss",
			func() any { return GetStrConfig[string](EnvironmentRedisPassword) },
			func(c *tgfconfig.Config) any { return c.Redis.Password }},
		{"RedisDB", EnvironmentRedisDB, "7",
			func() any { return GetStrConfig[int](EnvironmentRedisDB) },
			func(c *tgfconfig.Config) any { return c.Redis.DB }},
		{"RedisCluster bool宽松写法(审计P1)", EnvironmentRedisCluster, "true",
			func() any { return GetStrConfig[int](EnvironmentRedisCluster) },
			func(c *tgfconfig.Config) any { return boolToInt(c.Redis.Cluster) }},
		// ---- MySQL ----
		{"MySqlUser", EnvironmentMySqlUser, "tgfuser",
			func() any { return GetStrConfig[string](EnvironmentMySqlUser) },
			func(c *tgfconfig.Config) any { return c.MySQL.User }},
		{"MySqlPwd", EnvironmentMySqlPwd, "mysql-pwd",
			func() any { return GetStrConfig[string](EnvironmentMySqlPwd) },
			func(c *tgfconfig.Config) any { return c.MySQL.Password }},
		{"MySqlAddr", EnvironmentMySqlAddr, "db.internal",
			func() any { return GetStrConfig[string](EnvironmentMySqlAddr) },
			func(c *tgfconfig.Config) any { return c.MySQL.Addr }},
		{"MySqlPort", EnvironmentMySqlPort, "3307",
			func() any { return GetStrConfig[string](EnvironmentMySqlPort) },
			func(c *tgfconfig.Config) any { return c.MySQL.Port }},
		{"MySqlDB", EnvironmentMySqlDB, "tgf_x",
			func() any { return GetStrConfig[string](EnvironmentMySqlDB) },
			func(c *tgfconfig.Config) any { return c.MySQL.DB }},
		{"MySqlMaxIdleConns", EnvironmentMySqlMaxIdleConns, "20",
			func() any { return GetStrConfig[int](EnvironmentMySqlMaxIdleConns) },
			func(c *tgfconfig.Config) any { return c.MySQL.MaxIdleConns }},
		{"MySqlMaxOpenConns", EnvironmentMySqlMaxOpenConns, "500",
			func() any { return GetStrConfig[int](EnvironmentMySqlMaxOpenConns) },
			func(c *tgfconfig.Config) any { return c.MySQL.MaxOpenConns }},
		{"MySqlConnMaxLifetimeSec", EnvironmentMySqlConnMaxLifetimeSec, "600",
			func() any { return GetStrConfig[int](EnvironmentMySqlConnMaxLifetimeSec) },
			func(c *tgfconfig.Config) any { return c.MySQL.ConnMaxLifetimeSec }},
		// ---- Consul / Service ----
		{"ConsulAddress", EnvironmentConsulAddress, "10.0.0.9:8500",
			func() any { return GetStrConfig[string](EnvironmentConsulAddress) },
			func(c *tgfconfig.Config) any { return c.Consul.Address }},
		{"ConsulPath", EnvironmentConsulPath, "/tgf-e2",
			func() any { return GetStrConfig[string](EnvironmentConsulPath) },
			func(c *tgfconfig.Config) any { return c.Consul.Path }},
		{"ServicePort", EnvironmentServicePort, "9001",
			func() any { return GetStrConfig[string](EnvironmentServicePort) },
			func(c *tgfconfig.Config) any { return c.Service.Port }},
		{"ServiceAddress", EnvironmentServiceAddress, "0.0.0.0",
			func() any { return GetStrConfig[string](EnvironmentServiceAddress) },
			func(c *tgfconfig.Config) any { return c.Service.Address }},
		// ---- Gate / Runtime ----
		{"GatePush bool宽松写法(审计P1)", EnvironmentGatePush, "true",
			func() any { return int(GetStrConfig[int32](EnvironmentGatePush)) },
			func(c *tgfconfig.Config) any { return boolToInt(c.Gate.Push) }},
		{"RuntimeModule", EnvironmentRuntimeModule, "test",
			func() any { return GetStrConfig[string](EnvironmentRuntimeModule) },
			func(c *tgfconfig.Config) any { return c.Runtime.Module }},
		// ---- RPC ----
		{"RPCDefaultTimeoutMs", EnvironmentRPCDefaultTimeoutMs, "2500",
			func() any { return GetStrConfig[int](EnvironmentRPCDefaultTimeoutMs) },
			func(c *tgfconfig.Config) any { return c.RPC.DefaultTimeoutMs }},
		{"TCPDeadLineSec", EnvironmentTCPDeadLineSec, "120",
			func() any { return GetStrConfig[int](EnvironmentTCPDeadLineSec) },
			func(c *tgfconfig.Config) any { return c.RPC.TCPDeadLineSec }},
		{"TCPWriteTimeoutMs", EnvironmentTCPWriteTimeoutMs, "8000",
			func() any { return GetStrConfig[int](EnvironmentTCPWriteTimeoutMs) },
			func(c *tgfconfig.Config) any { return c.RPC.TCPWriteTimeoutMs }},
		{"TCPSendChanTimeoutMs", EnvironmentTCPSendChanTimeoutMs, "4000",
			func() any { return GetStrConfig[int](EnvironmentTCPSendChanTimeoutMs) },
			func(c *tgfconfig.Config) any { return c.RPC.TCPSendChanTimeoutMs }},
		// ---- DB ----
		{"DBCacheTimeoutSec", EnvironmentDBCacheTimeoutSec, "3600",
			func() any { return GetStrConfig[int64](EnvironmentDBCacheTimeoutSec) },
			func(c *tgfconfig.Config) any { return c.DB.CacheTimeoutSec }},
		{"DBMemTimeoutSec", EnvironmentDBMemTimeoutSec, "900",
			func() any { return GetStrConfig[int64](EnvironmentDBMemTimeoutSec) },
			func(c *tgfconfig.Config) any { return c.DB.MemTimeoutSec }},
		// ---- Security（E2 登记的 D 档遗留直读变量）----
		{"LoginTokenSecret", EnvironmentLoginTokenSecret, "e2-secret",
			func() any { return GetStrConfig[string](EnvironmentLoginTokenSecret) },
			func(c *tgfconfig.Config) any { return c.Security.LoginTokenSecret }},
		{"ADMIN_TOKEN", EnvironmentAdminToken, "e2-admin-tok",
			func() any { return GetStrConfig[string](EnvironmentAdminToken) },
			func(c *tgfconfig.Config) any { return c.Security.AdminToken }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(string(tc.key), tc.val)
			if _, err := tgfconfig.Reload(); err != nil {
				t.Fatalf("Reload err: %v", err)
			}
			oldV := tc.oldGet()
			newV := tc.newGet(tgfconfig.Current())
			if oldV != newV {
				t.Fatalf("env %v=%q 新旧 API 读取不一致: 旧 GetStrConfig=%v(%T) 新 config.Current=%v(%T)",
					tc.key, tc.val, oldV, oldV, newV, newV)
			}
		})
	}
}

// TestGetStrConfig_BoolDriftFixed 针对审计 P1（RedisCluster / GatePush 双轨 bool
// 漂移）的回归：所有宽松 bool 写法在旧 int 读取路径下都得到正确的 0/1。
func TestGetStrConfig_BoolDriftFixed(t *testing.T) {
	disableEnvFileLoader(t)

	cases := []struct {
		raw  string
		want int
	}{
		{"true", 1}, {"TRUE", 1}, {"yes", 1}, {"on", 1}, {"1", 1},
		{"false", 0}, {"no", 0}, {"off", 0}, {"0", 0},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(string(EnvironmentRedisCluster), tc.raw)
			t.Setenv(string(EnvironmentGatePush), tc.raw)
			if _, err := tgfconfig.Reload(); err != nil {
				t.Fatalf("Reload err: %v", err)
			}
			// 旧路径按 db/redis.go、rpc/plugins.go 的真实读法
			if got := GetStrConfig[int](EnvironmentRedisCluster); got != tc.want {
				t.Errorf("RedisCluster=%q 旧 GetStrConfig[int]=%v, want %v", tc.raw, got, tc.want)
			}
			if got := GetStrConfig[int32](EnvironmentGatePush); got != int32(tc.want) {
				t.Errorf("GatePush=%q 旧 GetStrConfig[int32]=%v, want %v", tc.raw, got, tc.want)
			}
			// 新路径
			c := tgfconfig.Current()
			if boolToInt(c.Redis.Cluster) != tc.want || boolToInt(c.Gate.Push) != tc.want {
				t.Errorf("新 API 解析错: Cluster=%v Push=%v, want %v", c.Redis.Cluster, c.Gate.Push, tc.want)
			}
		})
	}
}

// TestGetStrConfig_UnregisteredKeyFallsBackToEnv 验证未登记 key 不再 nil-deref
// panic（审计 P2），而是兜底直读环境变量。
func TestGetStrConfig_UnregisteredKeyFallsBackToEnv(t *testing.T) {
	const customKey Environment = "TGF_E2_CUSTOM_KEY"

	// 未设置时返回零值且不 panic
	if got := GetStrConfig[int](customKey); got != 0 {
		t.Errorf("未设置的自定义 key 应返回 0, 实际 %v", got)
	}
	// 设置后兜底直读 os.Getenv
	t.Setenv(string(customKey), "custom-value")
	if got := GetStrConfig[string](customKey); got != "custom-value" {
		t.Errorf("自定义 key 应兜底读环境变量, 实际 %q", got)
	}
}

// TestGetStrListConfig_TrimAndEmpty 验证列表读取的修正语义：
// 空配置返回空切片（旧实现返回 [""]，审计 P2）、分段 trim 空白。
func TestGetStrListConfig_TrimAndEmpty(t *testing.T) {
	disableEnvFileLoader(t)

	t.Setenv(string(EnvironmentLoggerIgnoredTags), "")
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if got := GetStrListConfig(EnvironmentLoggerIgnoredTags); len(got) != 0 {
		t.Errorf("空配置应返回空切片, 实际 %#v", got)
	}

	t.Setenv(string(EnvironmentLoggerIgnoredTags), "a, b ,c")
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if got := GetStrListConfig(EnvironmentLoggerIgnoredTags); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("列表 trim 切分错, 实际 %#v", got)
	}

	// 未登记 key 的列表读取同样不 panic
	if got := GetStrListConfig("TGF_E2_CUSTOM_LIST"); len(got) != 0 {
		t.Errorf("未登记空 key 应返回空切片, 实际 %#v", got)
	}
}

// TestInitConfig_Idempotent 验证 InitConfig 重入安全（旧实现第二次调用会因
// flag 重复注册 panic，审计 P2）。包 init 已调用过一次，这里再连续调两次。
func TestInitConfig_Idempotent(t *testing.T) {
	InitConfig()
	InitConfig()
}

// TestReload_EnvFileRoundTrip 是热更链路的端到端用例：
// 改 .env 文件 → config.Reload()（显式触发 API）→ loader 重读文件 →
// 新旧两套 API 同时读到新值；再改文件再 Reload，第二轮同样生效。
func TestReload_EnvFileRoundTrip(t *testing.T) {
	// t.Setenv 登记当前值，测试结束自动恢复（Overload 会写真实进程 env）
	t.Setenv(string(EnvironmentRedisCluster), "0")
	t.Setenv(string(EnvironmentLoggerMaxSize), "512")

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env.e2-roundtrip")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
			t.Fatalf("写 env 文件失败: %v", err)
		}
	}

	// 模拟 InitConfig 注入的 loader（指向本测试的临时 .env 文件）
	tgfconfig.RegisterEnvFileLoader(func() error { return godotenv.Overload(envFile) })
	t.Cleanup(func() {
		tgfconfig.RegisterEnvFileLoader(nil)
		_, _ = tgfconfig.Reload()
	})

	// 第一轮：bool 用宽松写法 true
	write("RedisCluster=true\nLogMaxSize=256\n")
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if got := GetStrConfig[int](EnvironmentRedisCluster); got != 1 {
		t.Errorf("第一轮旧 API RedisCluster 应为 1, 实际 %v", got)
	}
	if got := GetStrConfig[int](EnvironmentLoggerMaxSize); got != 256 {
		t.Errorf("第一轮旧 API LogMaxSize 应为 256, 实际 %v", got)
	}
	c := tgfconfig.Current()
	if !c.Redis.Cluster || c.Logger.MaxSize != 256 {
		t.Errorf("第一轮新 API 读取错: Cluster=%v MaxSize=%v", c.Redis.Cluster, c.Logger.MaxSize)
	}

	// 第二轮：改文件再热更
	write("RedisCluster=off\nLogMaxSize=128\n")
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("第二轮 Reload err: %v", err)
	}
	if got := GetStrConfig[int](EnvironmentRedisCluster); got != 0 {
		t.Errorf("第二轮旧 API RedisCluster 应为 0, 实际 %v", got)
	}
	if got := GetStrConfig[int](EnvironmentLoggerMaxSize); got != 128 {
		t.Errorf("第二轮旧 API LogMaxSize 应为 128, 实际 %v", got)
	}
	c = tgfconfig.Current()
	if c.Redis.Cluster || c.Logger.MaxSize != 128 {
		t.Errorf("第二轮新 API 读取错: Cluster=%v MaxSize=%v", c.Redis.Cluster, c.Logger.MaxSize)
	}
}

// TestOnReload_SubscriberSeesConsistentSnapshot 验证 OnReload 订阅者机制的真实
// 触发路径：显式调用 config.Reload() 后，订阅者收到的新 *Config 与旧
// GetStrConfig 读到的快照来自同一次解析（Wave2 各包将按此模式消费可热更项）。
func TestOnReload_SubscriberSeesConsistentSnapshot(t *testing.T) {
	disableEnvFileLoader(t)

	var hookCluster, hookMaxSize int
	hookCalled := false
	tgfconfig.OnReload(func(c *tgfconfig.Config) {
		hookCalled = true
		hookCluster = boolToInt(c.Redis.Cluster)
		hookMaxSize = c.Logger.MaxSize
	})

	t.Setenv(string(EnvironmentRedisCluster), "yes")
	t.Setenv(string(EnvironmentLoggerMaxSize), "300")
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}

	if !hookCalled {
		t.Fatal("OnReload 订阅者未被显式 Reload 触发")
	}
	// 订阅者读数 == 旧 API 读数 == 新 API 读数（同一次解析）
	if old := GetStrConfig[int](EnvironmentRedisCluster); old != hookCluster || old != 1 {
		t.Errorf("订阅者(%v)与旧 API(%v)读数不一致或解析错", hookCluster, old)
	}
	if old := GetStrConfig[int](EnvironmentLoggerMaxSize); old != hookMaxSize || old != 300 {
		t.Errorf("订阅者(%v)与旧 API(%v)读数不一致或解析错", hookMaxSize, old)
	}
}
