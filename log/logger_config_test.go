package log

// log 包配置项的测试。
//
// E3 收敛后：loadLumberjackConfig 不再裸读 os.Getenv，而是从统一配置系统
// （tgf/config，CONFIG 契约）读 Current().Logger 的类型化字段。本测试通过
// 设置环境变量 + tgfconfig.Load() 建立快照，再调用 loadLumberjackConfig
// 验证 v2 新增的 LogMaxSize / LogMaxAge / ... 能正确落到包级 runtime* 变量。

import (
	"os"
	"testing"

	tgfconfig "github.com/thkhxm/tgf/v2/config"
)

// 保存/恢复 runtime 变量，让多个测试之间不互相干扰
type lumberjackSnapshot struct {
	maxSize    int
	maxAge     int
	maxBackups int
	compress   bool
	localTime  bool
	timeFormat string
}

func snapshotLumberjack() lumberjackSnapshot {
	return lumberjackSnapshot{
		maxSize:    runtimeMaxSize,
		maxAge:     runtimeMaxAge,
		maxBackups: runtimeMaxBackups,
		compress:   runtimeCompress,
		localTime:  runtimeLocalTime,
		timeFormat: runtimeTimeFormat,
	}
}

func restoreLumberjack(s lumberjackSnapshot) {
	runtimeMaxSize = s.maxSize
	runtimeMaxAge = s.maxAge
	runtimeMaxBackups = s.maxBackups
	runtimeCompress = s.compress
	runtimeLocalTime = s.localTime
	runtimeTimeFormat = s.timeFormat
}

// loggerEnvKeys 是本测试组会改动的所有 log 相关环境变量；测试前后保存/恢复，
// 避免污染包级单例（log 包 init 已建立过一份快照）。
var loggerEnvKeys = []string{
	"LogMaxSize", "LogMaxAge", "LogMaxBackups",
	"LogCompress", "LogLocalTime", "LogTimeFormat",
	"LogLevel", "LogIgnoredTags",
}

// withLoggerEnv 保存现有 env、应用 set、Load 建立快照，返回的 cleanup 恢复一切。
func withLoggerEnv(t *testing.T, set map[string]string) {
	t.Helper()
	saved := make(map[string]*string, len(loggerEnvKeys))
	for _, k := range loggerEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			vv := v
			saved[k] = &vv
		} else {
			saved[k] = nil
		}
		_ = os.Unsetenv(k)
	}
	for k, v := range set {
		_ = os.Setenv(k, v)
	}
	// 重新解析，建立 tgfconfig 快照（loadLumberjackConfig 的读取源）
	if _, err := tgfconfig.Load(); err != nil {
		t.Fatalf("tgfconfig.Load 失败: %v", err)
	}
	t.Cleanup(func() {
		for k, v := range saved {
			if v == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *v)
			}
		}
		// 恢复后再 Load 一次，让快照回到与进程 env 一致的状态
		_, _ = tgfconfig.Load()
	})
}

func TestLumberjackConfig_Defaults(t *testing.T) {
	defer restoreLumberjack(snapshotLumberjack())

	// 全部清空相关环境变量 → 走 struct tag 默认值
	withLoggerEnv(t, nil)

	// 故意把 runtime 变量设成异常值，验证 loadLumberjackConfig 会用默认值覆盖
	runtimeMaxSize = -1
	runtimeMaxAge = -1
	runtimeMaxBackups = -1
	runtimeCompress = true
	runtimeLocalTime = false
	runtimeTimeFormat = "x"

	loadLumberjackConfig()

	if runtimeMaxSize != 512 {
		t.Errorf("默认 MaxSize 错: %d", runtimeMaxSize)
	}
	if runtimeMaxAge != 0 {
		t.Errorf("默认 MaxAge 错: %d", runtimeMaxAge)
	}
	if runtimeMaxBackups != 100 {
		t.Errorf("默认 MaxBackups 错: %d", runtimeMaxBackups)
	}
	if runtimeCompress {
		t.Errorf("默认 Compress 应为 false")
	}
	if !runtimeLocalTime {
		t.Errorf("默认 LocalTime 应为 true")
	}
	if runtimeTimeFormat != "2006-01-02 15:04:05.000" {
		t.Errorf("默认 TimeFormat 错: %q", runtimeTimeFormat)
	}
}

func TestLumberjackConfig_EnvOverrides(t *testing.T) {
	defer restoreLumberjack(snapshotLumberjack())

	withLoggerEnv(t, map[string]string{
		"LogMaxSize":    "256",
		"LogMaxAge":     "7",
		"LogMaxBackups": "50",
		"LogCompress":   "true",
		"LogLocalTime":  "false",
		"LogTimeFormat": "2006/01/02 15:04:05",
	})

	loadLumberjackConfig()

	if runtimeMaxSize != 256 {
		t.Errorf("MaxSize 覆盖错: %d", runtimeMaxSize)
	}
	if runtimeMaxAge != 7 {
		t.Errorf("MaxAge 覆盖错: %d", runtimeMaxAge)
	}
	if runtimeMaxBackups != 50 {
		t.Errorf("MaxBackups 覆盖错: %d", runtimeMaxBackups)
	}
	if !runtimeCompress {
		t.Errorf("Compress 应被设为 true")
	}
	if runtimeLocalTime {
		t.Errorf("LocalTime 应被设为 false")
	}
	if runtimeTimeFormat != "2006/01/02 15:04:05" {
		t.Errorf("TimeFormat 覆盖错: %q", runtimeTimeFormat)
	}
}

// TestLumberjackConfig_BoolParseTolerance 验证 bool 类配置（LogCompress）的宽松
// 解析在 CONFIG 契约下依然成立——解析由 tgf/config 包统一负责，规范化快照把
// "true"/"yes"/"1"/"on" 统一为 true。
func TestLumberjackConfig_BoolParseTolerance(t *testing.T) {
	defer restoreLumberjack(snapshotLumberjack())

	trueCases := []string{"true", "True", "TRUE", "1", "yes", "y", "on"}
	for _, c := range trueCases {
		withLoggerEnv(t, map[string]string{"LogCompress": c})
		loadLumberjackConfig()
		if !runtimeCompress {
			t.Errorf("LogCompress=%q 应解析为 true", c)
		}
	}

	falseCases := []string{"false", "0", "no", "n", "off"}
	for _, c := range falseCases {
		withLoggerEnv(t, map[string]string{"LogCompress": c})
		loadLumberjackConfig()
		if runtimeCompress {
			t.Errorf("LogCompress=%q 应解析为 false", c)
		}
	}
}

// TestSanitizeLoggerConfig_ZeroValueFallback 验证 sanitizeLoggerConfig 对全零值
// LoggerConfig（典型场景：未导入 tgf 包导致 Current() 返回空 Config）回退到硬编码
// 默认，保证零配置行为不变。
func TestSanitizeLoggerConfig_ZeroValueFallback(t *testing.T) {
	lc := sanitizeLoggerConfig(tgfconfig.LoggerConfig{})
	if lc.MaxSize != 512 {
		t.Errorf("零值 MaxSize 应回退 512, got %d", lc.MaxSize)
	}
	if lc.MaxBackups != 100 {
		t.Errorf("零值 MaxBackups 应回退 100, got %d", lc.MaxBackups)
	}
	if lc.TimeFormat != "2006-01-02 15:04:05.000" {
		t.Errorf("零值 TimeFormat 应回退默认, got %q", lc.TimeFormat)
	}
	if lc.ServiceFile != "service/service.log" {
		t.Errorf("零值 ServiceFile 应回退默认, got %q", lc.ServiceFile)
	}
	if lc.DBFile != "db/db.log" {
		t.Errorf("零值 DBFile 应回退默认, got %q", lc.DBFile)
	}
	if lc.Path != "./log/tgf.log" {
		t.Errorf("零值 Path 应回退默认, got %q", lc.Path)
	}
}
