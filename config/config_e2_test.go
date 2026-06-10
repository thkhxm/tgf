package config

// E2 配置收敛新增能力的测试：
//   - LoadLenient 宽容加载（坏值回退默认值 + 错误收集）
//   - GetString / Snapshot 规范化快照（bool → "1"/"0"）
//   - RegisterEnvFileLoader + Reload 的 env 文件重读接线
//   - Reload 失败保旧值（坏配置不被热更吃进去）
//   - SecurityConfig（LoginTokenSecret / ADMIN_TOKEN 登记）

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLoadLenient_BadValueFallsBackDefault(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("RedisDB", "not-a-number")
	t.Setenv("LogLevel", "warn")

	cfg, errs := LoadLenient()
	if len(errs) == 0 {
		t.Fatal("坏值应当收集到错误")
	}
	if cfg.Redis.DB != 1 {
		t.Errorf("坏值字段应回退默认值 1, 实际 %v", cfg.Redis.DB)
	}
	if cfg.Logger.Level != "warn" {
		t.Errorf("好值字段应正常生效, 实际 %v", cfg.Logger.Level)
	}
	// LoadLenient 也要建立快照与 current
	if got := Current().Logger.Level; got != "warn" {
		t.Errorf("Current 应反映 LoadLenient 结果, 实际 %v", got)
	}
	if v, ok := GetString("RedisDB"); !ok || v != "1" {
		t.Errorf("快照中坏值字段应为默认值 \"1\", 实际 %q ok=%v", v, ok)
	}
}

func TestLoadLenient_CollectsMultipleErrors(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("RedisDB", "abc")
	t.Setenv("LogMaxSize", "huge")
	t.Setenv("GatePush", "maybe")

	cfg, errs := LoadLenient()
	if len(errs) != 3 {
		t.Fatalf("应收集 3 个错误, 实际 %d: %v", len(errs), errs)
	}
	// 三个坏值字段全部回退默认值
	if cfg.Redis.DB != 1 || cfg.Logger.MaxSize != 512 || cfg.Gate.Push != true {
		t.Errorf("坏值字段应全部回退默认值, 实际 %+v %+v %+v", cfg.Redis.DB, cfg.Logger.MaxSize, cfg.Gate.Push)
	}
}

func TestGetString_BeforeLoadReturnsFalse(t *testing.T) {
	resetForTest()
	defer resetForTest()

	if v, ok := GetString("RedisAddr"); ok {
		t.Fatalf("快照建立前 GetString 应返回 false, 实际 %q", v)
	}
	if m := Snapshot(); len(m) != 0 {
		t.Fatalf("快照建立前 Snapshot 应为空 map, 实际 %d 项", len(m))
	}
}

func TestGetString_BoolCanonicalized(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"true", "1"}, {"TRUE", "1"}, {"yes", "1"}, {"y", "1"}, {"on", "1"}, {"1", "1"},
		{"false", "0"}, {"no", "0"}, {"off", "0"}, {"0", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			resetForTest()
			defer resetForTest()
			t.Setenv("RedisCluster", tc.raw)
			if _, err := Load(); err != nil {
				t.Fatalf("Load err: %v", err)
			}
			if v, ok := GetString("RedisCluster"); !ok || v != tc.want {
				t.Fatalf("RedisCluster=%q 规范化应为 %q, 实际 %q ok=%v", tc.raw, tc.want, v, ok)
			}
		})
	}
}

func TestSnapshot_UpdatedOnReload(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogLevel", "info")
	if _, err := Load(); err != nil {
		t.Fatalf("Load err: %v", err)
	}
	if v, _ := GetString("LogLevel"); v != "info" {
		t.Fatalf("Load 后快照应为 info, 实际 %q", v)
	}

	t.Setenv("LogLevel", "error")
	if _, err := Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if v, _ := GetString("LogLevel"); v != "error" {
		t.Fatalf("Reload 后快照应为 error, 实际 %q", v)
	}
}

func TestReload_EnvFileLoaderInvokedBeforeParse(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogLevel", "info")
	if _, err := Load(); err != nil {
		t.Fatalf("Load err: %v", err)
	}

	// loader 模拟"重读 .env 文件"——改写进程环境变量。
	// 若 loader 在解析之前执行，Reload 结果应反映 loader 写入的值。
	RegisterEnvFileLoader(func() error {
		return os.Setenv("LogLevel", "from-loader")
	})

	cfg, err := Reload()
	if err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if cfg.Logger.Level != "from-loader" {
		t.Fatalf("loader 应在解析前执行, Logger.Level=%v", cfg.Logger.Level)
	}
}

func TestReload_LoaderErrorDoesNotBlock(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogLevel", "info")
	if _, err := Load(); err != nil {
		t.Fatalf("Load err: %v", err)
	}

	RegisterEnvFileLoader(func() error {
		return errors.New("模拟 env 文件读取失败")
	})

	t.Setenv("LogLevel", "warn")
	cfg, err := Reload()
	if err != nil {
		t.Fatalf("loader 失败不应阻断 Reload, err: %v", err)
	}
	if cfg.Logger.Level != "warn" {
		t.Fatalf("loader 失败时应继续用进程环境变量, Logger.Level=%v", cfg.Logger.Level)
	}
}

func TestRegisterEnvFileLoader_NilUnregisters(t *testing.T) {
	resetForTest()
	defer resetForTest()

	called := false
	RegisterEnvFileLoader(func() error { called = true; return nil })
	RegisterEnvFileLoader(nil)

	if _, err := Reload(); err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if called {
		t.Fatal("注销后的 loader 不应再被调用")
	}
}

func TestReload_BadValueKeepsOldSnapshot(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("RedisDB", "5")
	t.Setenv("LogLevel", "info")
	if _, err := Load(); err != nil {
		t.Fatalf("Load err: %v", err)
	}

	t.Setenv("RedisDB", "boom")
	if _, err := Reload(); err == nil {
		t.Fatal("坏值 Reload 应返回错误")
	}
	// current 与快照都应保持旧值——坏配置不被热更吃进去
	if got := Current().Redis.DB; got != 5 {
		t.Errorf("Reload 失败后 Current 应保旧值 5, 实际 %v", got)
	}
	if v, _ := GetString("RedisDB"); v != "5" {
		t.Errorf("Reload 失败后快照应保旧值 \"5\", 实际 %q", v)
	}
}

func TestSecurityConfig_DefaultsAndOverride(t *testing.T) {
	resetForTest()
	defer resetForTest()

	_ = os.Unsetenv("LoginTokenSecret")
	_ = os.Unsetenv("ADMIN_TOKEN")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}
	if cfg.Security.LoginTokenSecret != "" || cfg.Security.AdminToken != "" {
		t.Errorf("凭据类配置默认值应为空(fail-closed), 实际 %+v", cfg.Security)
	}

	t.Setenv("LoginTokenSecret", "s3cret-value")
	t.Setenv("ADMIN_TOKEN", "admin-tok")
	cfg, err = Reload()
	if err != nil {
		t.Fatalf("Reload err: %v", err)
	}
	if cfg.Security.LoginTokenSecret != "s3cret-value" {
		t.Errorf("LoginTokenSecret 覆盖错: %q", cfg.Security.LoginTokenSecret)
	}
	if cfg.Security.AdminToken != "admin-tok" {
		t.Errorf("AdminToken 覆盖错: %q", cfg.Security.AdminToken)
	}
	// 快照中同样可读（旧 GetStrConfig 的读取源）
	if v, ok := GetString("LoginTokenSecret"); !ok || v != "s3cret-value" {
		t.Errorf("快照 LoginTokenSecret 错: %q ok=%v", v, ok)
	}
	if v, ok := GetString("ADMIN_TOKEN"); !ok || v != "admin-tok" {
		t.Errorf("快照 ADMIN_TOKEN 错: %q ok=%v", v, ok)
	}
}

// exportTestConfig 覆盖 canonicalString 的全部类型分支。
type exportTestConfig struct {
	S   string        `env:"TGF_E2_S" default:"hello"`
	B   bool          `env:"TGF_E2_B" default:"true"`
	I   int           `env:"TGF_E2_I" default:"-42"`
	U   uint32        `env:"TGF_E2_U" default:"7"`
	F   float64       `env:"TGF_E2_F" default:"3.5"`
	F32 float32       `env:"TGF_E2_F32" default:"1.25"`
	D   time.Duration `env:"TGF_E2_D" default:"1500ms"`
	L   []string      `env:"TGF_E2_L" default:"a, b ,c"`
}

func TestCollectEnvMap_CanonicalAllTypes(t *testing.T) {
	keys := []string{"TGF_E2_S", "TGF_E2_B", "TGF_E2_I", "TGF_E2_U", "TGF_E2_F", "TGF_E2_F32", "TGF_E2_D", "TGF_E2_L"}
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}

	var c exportTestConfig
	if err := fillStruct(reflect.ValueOf(&c).Elem()); err != nil {
		t.Fatalf("fillStruct err: %v", err)
	}
	out := make(map[string]string)
	collectEnvMap(reflect.ValueOf(&c).Elem(), out)

	want := map[string]string{
		"TGF_E2_S":   "hello",
		"TGF_E2_B":   "1",
		"TGF_E2_I":   "-42",
		"TGF_E2_U":   "7",
		"TGF_E2_F":   "3.5",
		"TGF_E2_F32": "1.25",
		"TGF_E2_D":   "1.5s",
		"TGF_E2_L":   "a,b,c",
	}
	for k, w := range want {
		if got := out[k]; got != w {
			t.Errorf("规范化 %v 期望 %q, 实际 %q", k, w, got)
		}
	}
}
