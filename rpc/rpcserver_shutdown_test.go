package rpc

// D3 / P0-3 优雅停机编排单测：
// 覆盖 Server.Destroy 的编排顺序（Consul 反注册 → 网关停 accept → drain →
// 终末 flush → 业务 Destroy）、幂等性、错误/panic 隔离、Consul 反注册超时兜底、
// drain 阶段超时配置，以及 rpc 包向 tgf 注册的终末 flush 桥接。

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thkhxm/rpcx/v2/server"
	"github.com/thkhxm/tgf"
)

// shutdownRecorder 按序记录停机各步骤的调用。
type shutdownRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *shutdownRecorder) add(step string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, step)
}

func (r *shutdownRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// fakeRegistryPlugin 模拟 Consul 注册插件（rpcx server.Plugin 是空接口，
// unregisterFromConsul 通过 Stop() error 形状断言识别它）。
type fakeRegistryPlugin struct {
	rec   *shutdownRecorder
	err   error
	block time.Duration // >0 时 Stop 阻塞该时长，模拟 Consul 不可达
}

func (p *fakeRegistryPlugin) Stop() error {
	if p.block > 0 {
		time.Sleep(p.block)
	}
	p.rec.add("consul-stop")
	return p.err
}

// shutdownGateService 模拟带 StopAccept 钩子的网关 service。
type shutdownGateService struct {
	rec     *shutdownRecorder
	name    string
	stopErr error
}

func (s *shutdownGateService) GetName() string            { return s.name }
func (s *shutdownGateService) GetVersion() string         { return "1.0" }
func (s *shutdownGateService) Startup() (bool, error)     { return true, nil }
func (s *shutdownGateService) LogicSyncMethods() []string { return nil }
func (s *shutdownGateService) Destroy(sub IService)       { s.rec.add("destroy:" + s.name) }
func (s *shutdownGateService) StopAccept() error {
	s.rec.add("stop-accept:" + s.name)
	return s.stopErr
}

// plainShutdownService 模拟不带 StopAccept 的普通业务 service。
type plainShutdownService struct {
	rec            *shutdownRecorder
	name           string
	panicOnDestroy bool
}

func (s *plainShutdownService) GetName() string            { return s.name }
func (s *plainShutdownService) GetVersion() string         { return "1.0" }
func (s *plainShutdownService) Startup() (bool, error)     { return true, nil }
func (s *plainShutdownService) LogicSyncMethods() []string { return nil }
func (s *plainShutdownService) Destroy(sub IService) {
	s.rec.add("destroy:" + s.name)
	if s.panicOnDestroy {
		panic(s.name + " destroy boom")
	}
}

// swapShutdownFlush 把 Destroy 第 5 步的 flush 注入点替换为记录桩，cleanup 时还原。
func swapShutdownFlush(t *testing.T, rec *shutdownRecorder) {
	t.Helper()
	old := shutdownFlushFn
	shutdownFlushFn = func() { rec.add("flush") }
	t.Cleanup(func() { shutdownFlushFn = old })
}

// TestServerDestroy_OrchestrationOrder 验证停机编排的完整顺序：
// Consul 反注册 → 网关停 accept → (drain，rpcServer 为 nil 时跳过) →
// 终末 flush → 业务 service Destroy（按注册顺序）。
func TestServerDestroy_OrchestrationOrder(t *testing.T) {
	rec := &shutdownRecorder{}
	swapShutdownFlush(t, rec)

	s := newBareServer()
	s.registryPlugin = &fakeRegistryPlugin{rec: rec}
	gate := &shutdownGateService{rec: rec, name: "gate"}
	biz := &plainShutdownService{rec: rec, name: "biz"}
	s.service = []IService{gate, biz}

	s.Destroy()

	want := []string{"consul-stop", "stop-accept:gate", "flush", "destroy:gate", "destroy:biz"}
	got := rec.snapshot()
	if len(got) != len(want) {
		t.Fatalf("停机序列 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("停机序列第 %d 步 = %v, want %v (完整序列 %v)", i, got[i], want[i], got)
		}
	}
}

// TestServerDestroy_Idempotent 验证 Destroy 幂等：信号编排与业务手动调用并存时只执行一次。
func TestServerDestroy_Idempotent(t *testing.T) {
	rec := &shutdownRecorder{}
	swapShutdownFlush(t, rec)

	s := newBareServer()
	s.registryPlugin = &fakeRegistryPlugin{rec: rec}
	s.service = []IService{&plainShutdownService{rec: rec, name: "biz"}}

	s.Destroy()
	first := len(rec.snapshot())
	s.Destroy()
	second := len(rec.snapshot())

	if first != second {
		t.Errorf("第二次 Destroy 不应重复执行: 第一次后 %v 步, 第二次后 %v 步", first, second)
	}
}

// TestServerDestroy_StopAcceptErrorDoesNotAbort 验证网关停 accept 失败不中断后续
// flush 与业务清理（停机路径上的局部失败只告警）。
func TestServerDestroy_StopAcceptErrorDoesNotAbort(t *testing.T) {
	rec := &shutdownRecorder{}
	swapShutdownFlush(t, rec)

	s := newBareServer()
	gate := &shutdownGateService{rec: rec, name: "gate", stopErr: errors.New("listener already closed")}
	biz := &plainShutdownService{rec: rec, name: "biz"}
	s.service = []IService{gate, biz}

	s.Destroy()

	got := rec.snapshot()
	want := []string{"stop-accept:gate", "flush", "destroy:gate", "destroy:biz"}
	if len(got) != len(want) {
		t.Fatalf("停机序列 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("停机序列第 %d 步 = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestServerDestroy_ServiceDestroyPanicIsolated 验证单个业务 Destroy panic
// 不会中断其余 service 的清理，也不会把 panic 抛给停机编排。
func TestServerDestroy_ServiceDestroyPanicIsolated(t *testing.T) {
	rec := &shutdownRecorder{}
	swapShutdownFlush(t, rec)

	s := newBareServer()
	s.service = []IService{
		&plainShutdownService{rec: rec, name: "biz1", panicOnDestroy: true},
		&plainShutdownService{rec: rec, name: "biz2"},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("service Destroy 的 panic 不应穿透停机编排: %v", r)
		}
	}()
	s.Destroy()

	got := rec.snapshot()
	var biz2Destroyed bool
	for _, step := range got {
		if step == "destroy:biz2" {
			biz2Destroyed = true
		}
	}
	if !biz2Destroyed {
		t.Errorf("biz1 Destroy panic 后 biz2 未被清理, 序列 = %v", got)
	}
}

// TestServerDestroy_ConsulStopTimeoutDoesNotBlock 验证 Consul 反注册超时兜底：
// 插件 Stop 卡住（模拟 Consul 不可达）时，停机序列在超时后继续推进，不被卡死。
func TestServerDestroy_ConsulStopTimeoutDoesNotBlock(t *testing.T) {
	rec := &shutdownRecorder{}
	swapShutdownFlush(t, rec)

	oldTimeout := consulUnregisterTimeout
	consulUnregisterTimeout = 50 * time.Millisecond
	t.Cleanup(func() { consulUnregisterTimeout = oldTimeout })

	s := newBareServer()
	s.registryPlugin = &fakeRegistryPlugin{rec: rec, block: 2 * time.Second}
	s.service = []IService{&plainShutdownService{rec: rec, name: "biz"}}

	start := time.Now()
	s.Destroy()
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("Consul 反注册卡死时停机序列阻塞了 %v, 超时兜底未生效", elapsed)
	}
	got := rec.snapshot()
	var flushed, bizDestroyed bool
	for _, step := range got {
		switch step {
		case "flush":
			flushed = true
		case "destroy:biz":
			bizDestroyed = true
		}
	}
	if !flushed || !bizDestroyed {
		t.Errorf("Consul 反注册超时后未继续执行 flush/业务清理, 序列 = %v", got)
	}
}

// TestServerDestroy_DrainWithIdleRPCServer 验证 drain 阶段对接真实 rpcx Server：
// 空闲 server（无 in-flight 请求）的 Shutdown 立即完成，停机序列不被拖住，
// 且 flush 仍发生在 drain 之后、业务 Destroy 之前。
func TestServerDestroy_DrainWithIdleRPCServer(t *testing.T) {
	rec := &shutdownRecorder{}
	swapShutdownFlush(t, rec)

	s := newBareServer()
	s.rpcServer = server.NewServer()
	s.shutdownDrainTimeout = 3 * time.Second
	s.service = []IService{&plainShutdownService{rec: rec, name: "biz"}}

	start := time.Now()
	s.Destroy()
	elapsed := time.Since(start)

	// rpcx Shutdown 的轮询间隔是 1s，空闲 server 首轮检查即通过，不应等满超时。
	if elapsed > 2*time.Second {
		t.Errorf("空闲 rpcx server 的 drain 耗时 %v, 不应接近/超过超时", elapsed)
	}
	got := rec.snapshot()
	want := []string{"flush", "destroy:biz"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("drain 后续序列 = %v, want %v", got, want)
	}
}

// TestWithShutdownDrainTimeout_Builder 验证 drain 超时的 builder 配置与默认值。
func TestWithShutdownDrainTimeout_Builder(t *testing.T) {
	s := newBareServer()
	if got := s.drainTimeout(); got != defaultShutdownDrainTimeout {
		t.Errorf("未配置时 drainTimeout = %v, want 默认 %v", got, defaultShutdownDrainTimeout)
	}
	s.WithShutdownDrainTimeout(7 * time.Second)
	if got := s.drainTimeout(); got != 7*time.Second {
		t.Errorf("WithShutdownDrainTimeout 后 drainTimeout = %v, want 7s", got)
	}
	s.WithShutdownDrainTimeout(0)
	if got := s.drainTimeout(); got != 7*time.Second {
		t.Errorf("WithShutdownDrainTimeout(0) 应被忽略, 但 drainTimeout 变成了 %v", got)
	}
	s.WithShutdownDrainTimeout(-time.Second)
	if got := s.drainTimeout(); got != 7*time.Second {
		t.Errorf("WithShutdownDrainTimeout(负数) 应被忽略, 但 drainTimeout 变成了 %v", got)
	}
}

// TestFinalFlushBridgeRegistered 验证 rpc 包 init 已把 db.FlushAll 桥接为
// tgf 的终末 flush 钩子（tgf 根包因循环依赖不能直接调 db.FlushAll）。
func TestFinalFlushBridgeRegistered(t *testing.T) {
	if got := tgf.FinalFlushHookCount(); got < 1 {
		t.Errorf("终末 flush 钩子未注册(FinalFlushHookCount = %v), SIGTERM 后 db.FlushAll 不会被触发", got)
	}
}

// TestFlushAllOnShutdown_EmptyRegistryNoop 验证无任何 longevity 管理器时
// 终末 flush 是安全的空操作（不 panic、不依赖外部 Redis/MySQL）。
func TestFlushAllOnShutdown_EmptyRegistryNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("空注册表下 flushAllOnShutdown 不应 panic: %v", r)
		}
	}()
	flushAllOnShutdown()
}
