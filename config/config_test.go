package config

import (
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setEnv / clearEnv 管理测试用的环境变量。t.Setenv 会自动清理，用它最省心。

func TestLoad_DefaultsWhenEnvEmpty(t *testing.T) {
	resetForTest()
	defer resetForTest()
	// 清理可能从 CI / 开发机带入的环境变量
	keys := []string{
		"LogPath", "LogLevel", "LogIgnoredTags",
		"RedisAddr", "RedisPassword", "RedisDB", "RedisCluster",
		"MySqlUser", "MySqlPwd", "MySqlAddr", "MySqlPort", "MySqlDB",
		"ConsulAddress", "ConsulPath",
		"ServicePort", "ServiceAddress",
		"GatePush", "RuntimeModule",
	}
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}
	if cfg.Logger.Path != "./log/tgf.log" {
		t.Errorf("默认 LogPath 错 %v", cfg.Logger.Path)
	}
	if cfg.Logger.Level != "debug" {
		t.Errorf("默认 LogLevel 错 %v", cfg.Logger.Level)
	}
	if cfg.Redis.Addr != "127.0.0.1:6379" {
		t.Errorf("默认 RedisAddr 错 %v", cfg.Redis.Addr)
	}
	if cfg.Redis.DB != 1 {
		t.Errorf("默认 RedisDB 错 %v", cfg.Redis.DB)
	}
	if cfg.Redis.Cluster != false {
		t.Errorf("默认 RedisCluster 错 %v", cfg.Redis.Cluster)
	}
	if cfg.MySQL.User != "root" {
		t.Errorf("默认 MySqlUser 错 %v", cfg.MySQL.User)
	}
	if cfg.Runtime.Module != "dev" {
		t.Errorf("默认 RuntimeModule 错 %v", cfg.Runtime.Module)
	}
	if cfg.Gate.Push != true {
		t.Errorf("默认 GatePush 错 %v", cfg.Gate.Push)
	}
}

func TestLoad_EnvOverridesDefault(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogPath", "/var/log/tgf.log")
	t.Setenv("LogLevel", "info")
	t.Setenv("RedisAddr", "10.0.0.1:6380")
	t.Setenv("RedisDB", "5")
	t.Setenv("RedisCluster", "1")
	t.Setenv("GatePush", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}
	if cfg.Logger.Path != "/var/log/tgf.log" {
		t.Errorf("LogPath 覆盖错 %v", cfg.Logger.Path)
	}
	if cfg.Logger.Level != "info" {
		t.Errorf("LogLevel 覆盖错 %v", cfg.Logger.Level)
	}
	if cfg.Redis.Addr != "10.0.0.1:6380" {
		t.Errorf("RedisAddr 覆盖错 %v", cfg.Redis.Addr)
	}
	if cfg.Redis.DB != 5 {
		t.Errorf("RedisDB 覆盖错 %v", cfg.Redis.DB)
	}
	if !cfg.Redis.Cluster {
		t.Errorf("RedisCluster 应为 true, 实际 %v", cfg.Redis.Cluster)
	}
	if cfg.Gate.Push {
		t.Errorf("GatePush 应为 false, 实际 %v", cfg.Gate.Push)
	}
}

func TestLoad_BadIntReturnsError(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("RedisDB", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("非法 int 应当返回错误")
	}
}

func TestLoad_BadBoolReturnsError(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("RedisCluster", "maybe")

	_, err := Load()
	if err == nil {
		t.Fatal("非法 bool 应当返回错误")
	}
}

func TestCurrent_BeforeLoadReturnsEmpty(t *testing.T) {
	resetForTest()
	defer resetForTest()

	cfg := Current()
	if cfg == nil {
		t.Fatal("Current 应当返回非 nil 的空 Config")
	}
	if cfg.Logger.Path != "" {
		t.Errorf("未 Load 前 Logger.Path 应为空串, 实际 %q", cfg.Logger.Path)
	}
}

func TestReload_ReplacesCurrent(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogLevel", "debug")
	if _, err := Load(); err != nil {
		t.Fatalf("first Load err: %v", err)
	}
	if Current().Logger.Level != "debug" {
		t.Fatalf("首次 Load 后期望 debug, 实际 %v", Current().Logger.Level)
	}

	t.Setenv("LogLevel", "warn")
	if _, err := Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if Current().Logger.Level != "warn" {
		t.Fatalf("Reload 后期望 warn, 实际 %v", Current().Logger.Level)
	}
}

func TestOnReload_HooksReceiveNewConfig(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogLevel", "info")
	_, _ = Load()

	var (
		receivedLevel atomic.Value
		calls         atomic.Int32
	)
	OnReload(func(c *Config) {
		calls.Add(1)
		receivedLevel.Store(c.Logger.Level)
	})

	t.Setenv("LogLevel", "error")
	if _, err := Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("OnReload 期望调用 1 次, 实际 %v", calls.Load())
	}
	if v, _ := receivedLevel.Load().(string); v != "error" {
		t.Errorf("回调应收到 error, 实际 %v", v)
	}
}

func TestOnReload_PanicRecovered(t *testing.T) {
	resetForTest()
	defer resetForTest()

	_, _ = Load()
	var survived atomic.Bool
	OnReload(func(*Config) { panic("boom") })
	OnReload(func(*Config) { survived.Store(true) })

	if _, err := Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if !survived.Load() {
		t.Fatal("panic 后续钩子应当继续执行")
	}
}

func TestReload_ConcurrentSafe(t *testing.T) {
	resetForTest()
	defer resetForTest()

	_, _ = Load()
	var wg sync.WaitGroup
	wg.Add(16)
	for i := 0; i < 16; i++ {
		go func() {
			defer wg.Done()
			_, _ = Reload()
			_ = Current()
		}()
	}
	wg.Wait()
}

// --- 类型转换边界测试 ---

// testConfig 是一个带所有支持类型的 struct，用来直接测 fillStruct 覆盖率。
type testConfig struct {
	S   string        `env:"TGF_TEST_S" default:"hello"`
	I   int           `env:"TGF_TEST_I" default:"42"`
	I64 int64         `env:"TGF_TEST_I64" default:"-100"`
	U   uint32        `env:"TGF_TEST_U" default:"7"`
	F   float64       `env:"TGF_TEST_F" default:"3.14"`
	B   bool          `env:"TGF_TEST_B" default:"true"`
	D   time.Duration `env:"TGF_TEST_D" default:"250ms"`
	L   []string      `env:"TGF_TEST_L" default:"a,b,c"`
}

func TestFillStruct_AllTypes(t *testing.T) {
	// 清空所有 TGF_TEST_* 让默认值生效
	keys := []string{"TGF_TEST_S", "TGF_TEST_I", "TGF_TEST_I64", "TGF_TEST_U", "TGF_TEST_F", "TGF_TEST_B", "TGF_TEST_D", "TGF_TEST_L"}
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}

	var c testConfig
	if err := fillStruct(reflectOf(&c)); err != nil {
		t.Fatalf("fillStruct err: %v", err)
	}
	if c.S != "hello" || c.I != 42 || c.I64 != -100 || c.U != 7 || c.F != 3.14 || !c.B || c.D != 250*time.Millisecond {
		t.Errorf("默认值填充错 %+v", c)
	}
	if len(c.L) != 3 || c.L[0] != "a" || c.L[2] != "c" {
		t.Errorf("[]string 默认值切分错 %+v", c.L)
	}
}

func TestFillStruct_EnvOverride(t *testing.T) {
	t.Setenv("TGF_TEST_S", "world")
	t.Setenv("TGF_TEST_I", "-1")
	t.Setenv("TGF_TEST_B", "no")
	t.Setenv("TGF_TEST_D", "2s")
	t.Setenv("TGF_TEST_L", "x, y, z")

	var c testConfig
	if err := fillStruct(reflectOf(&c)); err != nil {
		t.Fatalf("fillStruct err: %v", err)
	}
	if c.S != "world" || c.I != -1 || c.B || c.D != 2*time.Second {
		t.Errorf("env 覆盖错 %+v", c)
	}
	if len(c.L) != 3 || c.L[0] != "x" || c.L[1] != "y" || c.L[2] != "z" {
		t.Errorf("[]string env 切分错 %+v", c.L)
	}
}

// requiredConfig 演示 required tag
type requiredConfig struct {
	Must string `env:"TGF_TEST_MUST" required:"true"`
}

func TestFillStruct_RequiredMissing(t *testing.T) {
	_ = os.Unsetenv("TGF_TEST_MUST")
	var c requiredConfig
	err := fillStruct(reflectOf(&c))
	if err == nil {
		t.Fatal("required 字段缺失应当报错")
	}
}

// reflectOf 是测试用的小辅助，拿到可设置的 struct reflect.Value。
func reflectOf(p interface{}) reflect.Value {
	return reflect.ValueOf(p).Elem()
}
