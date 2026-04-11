package rpc

// A6 单测：WithHealthCheck 骨架的启停和状态观察。

import (
	"testing"
	"time"
)

// TestHealthCheck_DisabledByDefault 验证未调 WithHealthCheck 时心跳默认不启动。
func TestHealthCheck_DisabledByDefault(t *testing.T) {
	s := newBareServer()
	// 构造后没调 WithHealthCheck，IsHealthy 应当返回 false
	if s.IsHealthy() {
		t.Errorf("IsHealthy should be false before WithHealthCheck + startHealthCheckLoop")
	}
	// 直接调 startHealthCheckLoop 也应当不启动（因为 interval=0）
	s.startHealthCheckLoop()
	if s.healthCheckStop != nil {
		t.Errorf("startHealthCheckLoop should be noop when healthInterval <= 0")
	}
	if s.IsHealthy() {
		t.Errorf("IsHealthy should remain false when loop is noop")
	}
}

// TestHealthCheck_StartStop 验证开启心跳后 IsHealthy 为 true，
// Destroy 后心跳停止 IsHealthy 回到 false。
func TestHealthCheck_StartStop(t *testing.T) {
	s := newBareServer()
	s.WithHealthCheck(10 * time.Millisecond)
	s.startHealthCheckLoop()
	// 心跳 goroutine 启动后立即把 healthy 置 true
	if !s.IsHealthy() {
		t.Errorf("IsHealthy should be true immediately after start")
	}
	if s.healthCheckStop == nil {
		t.Errorf("healthCheckStop chan should be non-nil after start")
	}
	// 等几个 tick 确保心跳 goroutine 真的在跑
	time.Sleep(30 * time.Millisecond)
	if !s.IsHealthy() {
		t.Errorf("IsHealthy should still be true after some ticks")
	}
	// 停止
	s.stopHealthCheckLoop()
	// 给 goroutine 一点时间响应 stop
	for i := 0; i < 50 && s.IsHealthy(); i++ {
		time.Sleep(2 * time.Millisecond)
	}
	if s.IsHealthy() {
		t.Errorf("IsHealthy should return false after stop")
	}
	if s.healthCheckStop != nil {
		t.Errorf("stopHealthCheckLoop should clear healthCheckStop")
	}
}

// TestHealthCheck_StartIdempotent 验证连续 startHealthCheckLoop 不会启动多个 goroutine。
func TestHealthCheck_StartIdempotent(t *testing.T) {
	s := newBareServer()
	s.WithHealthCheck(50 * time.Millisecond)
	s.startHealthCheckLoop()
	firstStop := s.healthCheckStop
	s.startHealthCheckLoop()
	secondStop := s.healthCheckStop
	if firstStop != secondStop {
		t.Errorf("second start should not create a new stop channel: %p vs %p", firstStop, secondStop)
	}
	s.stopHealthCheckLoop()
}

// TestHealthCheck_StopIdempotent 验证连续 stopHealthCheckLoop 不会 panic（close closed channel）。
func TestHealthCheck_StopIdempotent(t *testing.T) {
	s := newBareServer()
	s.WithHealthCheck(50 * time.Millisecond)
	s.startHealthCheckLoop()
	s.stopHealthCheckLoop()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second stop should not panic, got: %v", r)
		}
	}()
	s.stopHealthCheckLoop()
}

// TestWithHealthCheck_IgnoresNonPositiveInterval 验证传 0 或负数时 healthInterval 不变。
func TestWithHealthCheck_IgnoresNonPositiveInterval(t *testing.T) {
	s := newBareServer()
	s.WithHealthCheck(100 * time.Millisecond)
	if s.healthInterval != 100*time.Millisecond {
		t.Errorf("healthInterval = %v, want 100ms", s.healthInterval)
	}
	s.WithHealthCheck(0)
	if s.healthInterval != 100*time.Millisecond {
		t.Errorf("WithHealthCheck(0) should be noop, but healthInterval changed to %v", s.healthInterval)
	}
	s.WithHealthCheck(-1)
	if s.healthInterval != 100*time.Millisecond {
		t.Errorf("WithHealthCheck(-1) should be noop, but healthInterval changed to %v", s.healthInterval)
	}
}
