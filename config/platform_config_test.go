package config

// H1 PlatformConfig 配置组测试：
//   1. 默认值（全空，未配置的平台保持零值）；
//   2. env 覆盖（9 个字段逐项断言，表驱动）；
//   3. 规范化快照包含全部平台 env key（旧 GetStrConfig 适配层可读）。

import (
	"os"
	"testing"
)

// platformEnvKeys 是 PlatformConfig 的全部 env key（与 struct tag 一致）。
var platformEnvKeys = []string{
	"WechatAppID", "WechatAppSecret",
	"TiktokAppID", "TiktokAppSecret",
	"AppleTeamID", "AppleKeyID", "ApplePrivateKey",
	"FacebookAppID", "FacebookAppSecret",
}

func clearPlatformEnv(t *testing.T) {
	t.Helper()
	for _, k := range platformEnvKeys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestPlatformConfig_Defaults(t *testing.T) {
	resetForTest()
	defer resetForTest()
	clearPlatformEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}
	p := cfg.Platform
	fields := []struct {
		name string
		got  string
	}{
		{"WechatAppID", p.WechatAppID},
		{"WechatAppSecret", p.WechatAppSecret},
		{"TiktokAppID", p.TiktokAppID},
		{"TiktokAppSecret", p.TiktokAppSecret},
		{"AppleTeamID", p.AppleTeamID},
		{"AppleKeyID", p.AppleKeyID},
		{"ApplePrivateKey", p.ApplePrivateKey},
		{"FacebookAppID", p.FacebookAppID},
		{"FacebookAppSecret", p.FacebookAppSecret},
	}
	for _, f := range fields {
		if f.got != "" {
			t.Errorf("默认 %v 应为空, got %q", f.name, f.got)
		}
	}
}

func TestPlatformConfig_EnvOverrides(t *testing.T) {
	resetForTest()
	defer resetForTest()
	clearPlatformEnv(t)

	tests := []struct {
		envKey string
		value  string
		get    func(*Config) string
	}{
		{"WechatAppID", "wx_app_1", func(c *Config) string { return c.Platform.WechatAppID }},
		{"WechatAppSecret", "wx_secret_1", func(c *Config) string { return c.Platform.WechatAppSecret }},
		{"TiktokAppID", "tt_app_1", func(c *Config) string { return c.Platform.TiktokAppID }},
		{"TiktokAppSecret", "tt_secret_1", func(c *Config) string { return c.Platform.TiktokAppSecret }},
		{"AppleTeamID", "TEAM123456", func(c *Config) string { return c.Platform.AppleTeamID }},
		{"AppleKeyID", "KEY1234567", func(c *Config) string { return c.Platform.AppleKeyID }},
		{"ApplePrivateKey", "-----BEGIN PRIVATE KEY-----xxx", func(c *Config) string { return c.Platform.ApplePrivateKey }},
		{"FacebookAppID", "fb_app_1", func(c *Config) string { return c.Platform.FacebookAppID }},
		{"FacebookAppSecret", "fb_secret_1", func(c *Config) string { return c.Platform.FacebookAppSecret }},
	}
	for _, tt := range tests {
		t.Setenv(tt.envKey, tt.value)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.envKey, func(t *testing.T) {
			if got := tt.get(cfg); got != tt.value {
				t.Errorf("%v = %q, want %q", tt.envKey, got, tt.value)
			}
		})
	}

	// 规范化快照必须包含全部平台 key（旧 GetStrConfig 适配层的读取源）。
	snap := Snapshot()
	for _, k := range platformEnvKeys {
		if _, ok := snap[k]; !ok {
			t.Errorf("Snapshot 缺少平台配置 key %v", k)
		}
	}
}
