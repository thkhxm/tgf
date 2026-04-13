package log

// log 包配置项的测试。
//
// 验证 v2 新增的 LogMaxSize / LogMaxAge / LogMaxBackups / LogCompress /
// LogLocalTime / LogTimeFormat 这些环境变量能被 loadLumberjackConfig 正确读取
// 并落到包级 runtime* 变量上。

import (
	"os"
	"testing"
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

func TestLumberjackConfig_Defaults(t *testing.T) {
	defer restoreLumberjack(snapshotLumberjack())

	// 清掉所有相关环境变量
	envs := []string{
		"LogMaxSize", "LogMaxAge", "LogMaxBackups",
		"LogCompress", "LogLocalTime", "LogTimeFormat",
	}
	saved := make(map[string]string)
	for _, k := range envs {
		saved[k] = os.Getenv(k)
		_ = os.Unsetenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v != "" {
				_ = os.Setenv(k, v)
			}
		}
	}()

	// 重置为初始默认值
	runtimeMaxSize = 512
	runtimeMaxAge = 0
	runtimeMaxBackups = 100
	runtimeCompress = false
	runtimeLocalTime = true
	runtimeTimeFormat = "2006-01-02 15:04:05.000"

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
}

func TestLumberjackConfig_EnvOverrides(t *testing.T) {
	defer restoreLumberjack(snapshotLumberjack())

	t.Setenv("LogMaxSize", "256")
	t.Setenv("LogMaxAge", "7")
	t.Setenv("LogMaxBackups", "50")
	t.Setenv("LogCompress", "true")
	t.Setenv("LogLocalTime", "false")
	t.Setenv("LogTimeFormat", "2006/01/02 15:04:05")

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

func TestLumberjackConfig_BoolParseTolerance(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true}, {"True", true}, {"TRUE", true},
		{"1", true}, {"yes", true}, {"y", true}, {"on", true},
		{"false", false}, {"0", false}, {"no", false},
		{"n", false}, {"off", false}, {"  ", false}, // " " trim 后为空
	}
	for _, c := range cases {
		got := parseBoolEnv(c.in, false)
		if got != c.want {
			t.Errorf("parseBoolEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	// 默认值 fallback
	if parseBoolEnv("garbage", true) != true {
		t.Error("无法识别的值应返回默认值")
	}
}
