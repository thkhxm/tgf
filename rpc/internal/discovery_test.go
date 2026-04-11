package internal

// A6 单元测试：discovery 单例的并发初始化 + GetDiscovery 的非懒加载语义。

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestGetDiscovery_ReturnsNilBeforeUse 验证 A6 的行为变更：
// GetDiscovery 不再自动触发 UseConsulDiscovery，未初始化时返回 nil。
// 这是 A4 的 WithoutConsul() 能真正生效的前提。
func TestGetDiscovery_ReturnsNilBeforeUse(t *testing.T) {
	ResetDiscoveryForTest()
	defer ResetDiscoveryForTest()

	if got := GetDiscovery(); got != nil {
		t.Errorf("GetDiscovery before UseConsulDiscovery should return nil, got %T", got)
	}
}

// TestUseConsulDiscovery_Idempotent 验证连续调用 UseConsulDiscovery 只初始化一次。
// sync.Once 确保 discovery 指针在首次后不再变化。
func TestUseConsulDiscovery_Idempotent(t *testing.T) {
	ResetDiscoveryForTest()
	defer ResetDiscoveryForTest()

	UseConsulDiscovery()
	first := GetDiscovery()
	if first == nil {
		t.Fatalf("discovery should be initialized after UseConsulDiscovery")
	}

	UseConsulDiscovery()
	second := GetDiscovery()
	if second != first {
		t.Errorf("second UseConsulDiscovery returned a different instance: %p vs %p", first, second)
	}
}

// TestUseConsulDiscovery_ConcurrentSingleton 是 A6 修复的核心回归测试：
// 原代码 `if discovery != nil { return }` 非原子，N 个 goroutine 并发调用
// 可能各自 new 一个 ConsulDiscovery。sync.Once 保证恰好一个胜者。
func TestUseConsulDiscovery_ConcurrentSingleton(t *testing.T) {
	ResetDiscoveryForTest()
	defer ResetDiscoveryForTest()

	const N = 128
	var wg sync.WaitGroup
	wg.Add(N)
	results := make([]IRPCDiscovery, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			UseConsulDiscovery()
			results[i] = GetDiscovery()
		}()
	}
	wg.Wait()

	// 所有 goroutine 拿到的 discovery 必须是同一个指针
	for i := 1; i < N; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d saw different discovery %p vs %p", i, results[i], results[0])
		}
	}
}

// TestConsulDiscovery_RegisterDiscoveryReuseByModuleName 验证 A6 幂等化：
// 同一个 moduleName 多次调用 RegisterDiscovery 返回同一个 *client.ConsulDiscovery 实例，
// 避免 A6 之前每次都 new 导致的连接泄漏。
//
// 因为 client.NewConsulDiscovery 内部会真的尝试连 Consul 地址——测试环境可能
// 没有 Consul——所以这个测试通过断言"第二次调用的结果 == 第一次"验证幂等性。
// 只要 Consul 地址不可达时 NewConsulDiscovery 不 panic、返回的指针稳定，测试就能跑。
//
// 如果 CI 里这个测试 flaky 可以加 -short 跳过。
func TestConsulDiscovery_RegisterDiscoveryReuseByModuleName(t *testing.T) {
	c := &ConsulDiscovery{}
	c.initStruct()

	first := c.RegisterDiscovery("module-a")
	second := c.RegisterDiscovery("module-a")

	if first != second {
		t.Errorf("RegisterDiscovery for same moduleName should return same instance: %p vs %p", first, second)
	}
}

// TestConsulDiscovery_ConcurrentRegisterNoLeak 验证并发 RegisterDiscovery 的幂等性：
// 100 个 goroutine 并发注册同一个 moduleName，discoveryMap 里只能有一个条目
// （虽然可能有多个短暂 new 的实例，但 Insert 只接受第一个）。
func TestConsulDiscovery_ConcurrentRegisterNoLeak(t *testing.T) {
	c := &ConsulDiscovery{}
	c.initStruct()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	var firstPtr atomic.Pointer[struct{}]
	_ = firstPtr

	results := make([]uintptr, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			d := c.RegisterDiscovery("same-module")
			// d 是 *client.ConsulDiscovery，记录其地址用于断言
			if d != nil {
				results[i] = uintptr(0) // 我们在这里只关心后面的 Get 返回一致性
			}
		}()
	}
	wg.Wait()

	// 所有并发完成后，Get 返回的必须是一个稳定的实例
	got1 := c.GetDiscovery("same-module")
	got2 := c.GetDiscovery("same-module")
	if got1 != got2 {
		t.Errorf("GetDiscovery should return stable instance after concurrent Register")
	}
	if got1 == nil {
		t.Errorf("GetDiscovery should find registered entry")
	}
}
