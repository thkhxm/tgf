package internal

// F2 单测：buildRegistration 的字段映射 / 校验 / 默认值归一（纯函数，无网络）。
// 真实 Consul 行为（注册→passing→停止续约→critical→自动摘除、kill 进程摘除）
// 由 it_health_test.go 的 integration 用例覆盖。

import (
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
)

// TestBuildRegistration_Defaults 验证零值 options 的生产默认解析：
// interval=5s、TTL=3×interval、DeregisterCriticalAfter 归一到 Consul 硬下限 1m、
// BasePath 回退 "tgf"、tcp@ 前缀容忍。
func TestBuildRegistration_Defaults(t *testing.T) {
	reg, interval, err := buildRegistration(TTLHealthOptions{
		ServiceAddress: "tcp@192.168.1.10:8082",
		Modules:        []string{"gate", "hall"},
	})
	if err != nil {
		t.Fatalf("buildRegistration 失败: %v", err)
	}
	if interval != DefaultTTLHealthInterval {
		t.Errorf("interval = %v, want %v", interval, DefaultTTLHealthInterval)
	}
	if reg.Address != "192.168.1.10" || reg.Port != 8082 {
		t.Errorf("address/port = %v:%v, want 192.168.1.10:8082", reg.Address, reg.Port)
	}
	if reg.Name != "tgf" {
		t.Errorf("Name = %q, want 默认 basePath 回退 \"tgf\"", reg.Name)
	}
	if reg.ID != "tgf-192.168.1.10:8082" {
		t.Errorf("ID = %q, want \"tgf-192.168.1.10:8082\"", reg.ID)
	}
	if got, want := reg.Check.TTL, (DefaultTTLHealthInterval * ttlFactor).String(); got != want {
		t.Errorf("Check.TTL = %q, want %q (3×interval)", got, want)
	}
	if reg.Check.Status != api.HealthPassing {
		t.Errorf("Check.Status = %q, want passing（注册即就绪，F2 时序保证）", reg.Check.Status)
	}
	if got, want := reg.Check.DeregisterCriticalServiceAfter, minDeregisterCriticalAfter.String(); got != want {
		t.Errorf("DeregisterCriticalServiceAfter = %q, want %q (Consul 硬下限)", got, want)
	}
	if len(reg.Tags) != 2 || reg.Tags[0] != "gate" || reg.Tags[1] != "hall" {
		t.Errorf("Tags = %v, want [gate hall]", reg.Tags)
	}
	if reg.Check.CheckID != reg.ID+"-ttl" {
		t.Errorf("CheckID = %q, want %q", reg.Check.CheckID, reg.ID+"-ttl")
	}
}

// TestBuildRegistration_ExplicitOverrides 验证显式 TTL / interval / basePath /
// dereg 的覆盖路径与 1m 下限钳制。
func TestBuildRegistration_ExplicitOverrides(t *testing.T) {
	reg, interval, err := buildRegistration(TTLHealthOptions{
		ServiceAddress:          "127.0.0.1:19500",
		Interval:                time.Second,
		TTL:                     2 * time.Second,
		DeregisterCriticalAfter: 10 * time.Second, // < 1m → 钳制
		BasePath:                "/tgf_it/",
	})
	if err != nil {
		t.Fatalf("buildRegistration 失败: %v", err)
	}
	if interval != time.Second {
		t.Errorf("interval = %v, want 1s", interval)
	}
	if reg.Check.TTL != "2s" {
		t.Errorf("Check.TTL = %q, want \"2s\"", reg.Check.TTL)
	}
	if reg.Name != "tgf_it" {
		t.Errorf("Name = %q, want basePath 去斜杠 \"tgf_it\"", reg.Name)
	}
	if got, want := reg.Check.DeregisterCriticalServiceAfter, minDeregisterCriticalAfter.String(); got != want {
		t.Errorf("DeregisterCriticalServiceAfter = %q, want 钳制到 %q", got, want)
	}
}

// TestBuildRegistration_Invalid 验证非法输入的快速失败：
// 地址格式错误、端口非数字、TTL ≤ 续约周期（正常续约都来不及，必然误判 critical）。
func TestBuildRegistration_Invalid(t *testing.T) {
	cases := []struct {
		name string
		opt  TTLHealthOptions
	}{
		{"无端口地址", TTLHealthOptions{ServiceAddress: "127.0.0.1"}},
		{"端口非数字", TTLHealthOptions{ServiceAddress: "127.0.0.1:abc"}},
		{"TTL不大于interval", TTLHealthOptions{
			ServiceAddress: "127.0.0.1:8082",
			Interval:       5 * time.Second,
			TTL:            5 * time.Second,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := buildRegistration(tc.opt); err == nil {
				t.Errorf("buildRegistration(%+v) 应返回 error", tc.opt)
			}
		})
	}
}
