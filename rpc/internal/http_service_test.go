package internal

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G2/G3 单测：HTTP service 的 Consul 注册体构造
//（buildHTTPServiceRegistration 纯函数）——字段映射 / 默认值 / 校验。
// 真实 Consul 行为由 rpc/it_g_http_test.go integration 覆盖。
//2026/6/10
//***************************************************

import (
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/thkhxm/tgf/v2"
)

// TestBuildHTTPServiceRegistration_TTLDefaults TTL 模式（默认）的字段映射与默认值。
func TestBuildHTTPServiceRegistration_TTLDefaults(t *testing.T) {
	reg, interval, err := buildHTTPServiceRegistration(HTTPServiceOptions{
		ServiceName: "user-api",
		Address:     "10.0.0.5:8090",
		Tags:        []string{"v1"},
		Meta:        map[string]string{"zone": "a"},
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if interval != DefaultTTLHealthInterval {
		t.Errorf("interval = %v, want 默认 %v", interval, DefaultTTLHealthInterval)
	}
	if reg.ID != "user-api-10.0.0.5:8090" || reg.Name != "user-api" {
		t.Errorf("ID/Name = %q/%q", reg.ID, reg.Name)
	}
	if reg.Address != "10.0.0.5" || reg.Port != 8090 {
		t.Errorf("Address/Port = %q/%d, want 10.0.0.5/8090", reg.Address, reg.Port)
	}
	if reg.Check.TTL != (DefaultTTLHealthInterval * ttlFactor).String() {
		t.Errorf("TTL = %q, want %q", reg.Check.TTL, (DefaultTTLHealthInterval * ttlFactor).String())
	}
	if reg.Check.HTTP != "" {
		t.Errorf("TTL 模式不应设置 HTTP 探测 URL, got %q", reg.Check.HTTP)
	}
	if reg.Check.Status != api.HealthPassing {
		t.Errorf("TTL 模式注册即 passing, got %q", reg.Check.Status)
	}
	if reg.Check.DeregisterCriticalServiceAfter != minDeregisterCriticalAfter.String() {
		t.Errorf("DeregisterCriticalAfter = %q, want 硬下限 %q",
			reg.Check.DeregisterCriticalServiceAfter, minDeregisterCriticalAfter.String())
	}
	if len(reg.Tags) != 1 || reg.Tags[0] != "v1" {
		t.Errorf("Tags = %v", reg.Tags)
	}
	// Meta：框架字段 + 用户字段合并。
	if reg.Meta["nodeId"] != tgf.NodeId || reg.Meta["protocol"] != "http" ||
		reg.Meta["healthPath"] != "/health" || reg.Meta["zone"] != "a" {
		t.Errorf("Meta = %v", reg.Meta)
	}
}

// TestBuildHTTPServiceRegistration_HTTPCheck HTTP check 模式：探测 URL / 间隔 / 超时。
func TestBuildHTTPServiceRegistration_HTTPCheck(t *testing.T) {
	reg, interval, err := buildHTTPServiceRegistration(HTTPServiceOptions{
		ServiceName: "user-api",
		Address:     "10.0.0.5:8090",
		HealthPath:  "healthz", // 缺前导 / 应被补齐
		CheckMode:   HTTPCheckModeHTTP,
		Interval:    3 * time.Second,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if interval != 3*time.Second {
		t.Errorf("interval = %v, want 3s", interval)
	}
	if reg.Check.HTTP != "http://10.0.0.5:8090/healthz" {
		t.Errorf("探测 URL = %q, want http://10.0.0.5:8090/healthz", reg.Check.HTTP)
	}
	if reg.Check.Interval != "3s" || reg.Check.Timeout != "1s" {
		t.Errorf("Interval/Timeout = %q/%q, want 3s/1s", reg.Check.Interval, reg.Check.Timeout)
	}
	if reg.Check.TTL != "" {
		t.Errorf("HTTP 模式不应设置 TTL, got %q", reg.Check.TTL)
	}
	if reg.Check.Status != "" {
		t.Errorf("HTTP 模式初始状态交给首轮探测裁定, got %q", reg.Check.Status)
	}
	if reg.Meta["healthPath"] != "/healthz" {
		t.Errorf("Meta.healthPath = %q, want /healthz", reg.Meta["healthPath"])
	}
}

// TestBuildHTTPServiceRegistration_HTTPCheck_DefaultTimeout 超时默认值。
func TestBuildHTTPServiceRegistration_HTTPCheck_DefaultTimeout(t *testing.T) {
	reg, _, err := buildHTTPServiceRegistration(HTTPServiceOptions{
		ServiceName: "x",
		Address:     "10.0.0.5:80",
		CheckMode:   HTTPCheckModeHTTP,
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if reg.Check.Timeout != DefaultHTTPCheckTimeout.String() {
		t.Errorf("Timeout = %q, want 默认 %q", reg.Check.Timeout, DefaultHTTPCheckTimeout.String())
	}
}

// TestBuildHTTPServiceRegistration_Validation 校验拒绝表。
func TestBuildHTTPServiceRegistration_Validation(t *testing.T) {
	cases := []struct {
		name    string
		opt     HTTPServiceOptions
		wantErr string
	}{
		{"缺 ServiceName", HTTPServiceOptions{Address: "1.2.3.4:80"}, "ServiceName 必填"},
		{"非法地址", HTTPServiceOptions{ServiceName: "x", Address: "no-port"}, "host:port"},
		{"通配 host", HTTPServiceOptions{ServiceName: "x", Address: "0.0.0.0:80"}, "通配地址"},
		{"IPv6 通配 host", HTTPServiceOptions{ServiceName: "x", Address: "[::]:80"}, "通配地址"},
		{"端口非数字", HTTPServiceOptions{ServiceName: "x", Address: "1.2.3.4:abc"}, "端口非数字"},
		{"非法 CheckMode", HTTPServiceOptions{ServiceName: "x", Address: "1.2.3.4:80", CheckMode: "tcp"}, "CheckMode 非法"},
		{"TTL 不大于续约周期", HTTPServiceOptions{
			ServiceName: "x", Address: "1.2.3.4:80",
			Interval: 5 * time.Second, TTL: 5 * time.Second,
		}, "必须大于续约周期"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildHTTPServiceRegistration(tc.opt)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want 包含 %q", err, tc.wantErr)
			}
		})
	}
}
