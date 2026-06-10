package internal

// A6 单元测试：discovery 单例的并发初始化 + GetDiscovery 的非懒加载语义。
//
// D5（v3）改造：RegisterDiscovery 相关测试不再依赖本机真实 Consul——
// 通过注入 newConsulDiscoveryFn 桩（fakeKVStore 驱动的真实 ConsulDiscovery 实例）
// 让测试在干净环境（无 Consul）下确定性通过，同时新增"创建失败不缓存 nil、
// 失败后可重试"的回归用例（这正是 v3 审计指出的 consul.go 吞错根因）。

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	libkvstore "github.com/rpcxio/libkv/store"
	"github.com/thkhxm/rpcx-consul/v2/client"
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

// ---- D5 测试基建：不依赖真实 Consul 的 ConsulDiscovery 构造 ----

// fakeKVStore 是 libkv store.Store 的最小桩实现。
// List 返回 ErrKeyNotFound（空目录），WatchTree 返回一个永不发数据的打开通道——
// 这样 client.NewConsulDiscoveryStore 能构造出**功能完整**（含 stopCh，Close 安全）
// 的 ConsulDiscovery 实例，watch goroutine 阻塞在 select 上、Close 时干净退出。
type fakeKVStore struct{}

func (fakeKVStore) Put(key string, value []byte, options *libkvstore.WriteOptions) error {
	return nil
}
func (fakeKVStore) Get(key string) (*libkvstore.KVPair, error) {
	return nil, libkvstore.ErrKeyNotFound
}
func (fakeKVStore) Delete(key string) error         { return nil }
func (fakeKVStore) Exists(key string) (bool, error) { return false, nil }
func (fakeKVStore) Watch(key string, stopCh <-chan struct{}) (<-chan *libkvstore.KVPair, error) {
	return make(chan *libkvstore.KVPair), nil
}
func (fakeKVStore) WatchTree(directory string, stopCh <-chan struct{}) (<-chan []*libkvstore.KVPair, error) {
	return make(chan []*libkvstore.KVPair), nil
}
func (fakeKVStore) NewLock(key string, options *libkvstore.LockOptions) (libkvstore.Locker, error) {
	return nil, errors.New("fakeKVStore: not implemented")
}
func (fakeKVStore) List(directory string) ([]*libkvstore.KVPair, error) {
	return nil, libkvstore.ErrKeyNotFound
}
func (fakeKVStore) DeleteTree(directory string) error { return nil }
func (fakeKVStore) AtomicPut(key string, value []byte, previous *libkvstore.KVPair, options *libkvstore.WriteOptions) (bool, *libkvstore.KVPair, error) {
	return false, nil, errors.New("fakeKVStore: not implemented")
}
func (fakeKVStore) AtomicDelete(key string, previous *libkvstore.KVPair) (bool, error) {
	return false, errors.New("fakeKVStore: not implemented")
}
func (fakeKVStore) Close() {}

// newFakeConsulDiscovery 构造一个基于 fakeKVStore 的真实 ConsulDiscovery 实例。
func newFakeConsulDiscovery(t *testing.T, basePath, servicePath string) (*client.ConsulDiscovery, error) {
	t.Helper()
	return client.NewConsulDiscoveryStore(basePath+"/"+servicePath, fakeKVStore{})
}

// withStubConsulFactory 临时替换 newConsulDiscoveryFn，返回恢复函数。
func withStubConsulFactory(t *testing.T, fn func(basePath, servicePath string, consulAddr []string, options *libkvstore.Config) (*client.ConsulDiscovery, error)) func() {
	t.Helper()
	orig := newConsulDiscoveryFn
	newConsulDiscoveryFn = fn
	return func() { newConsulDiscoveryFn = orig }
}

// TestConsulDiscovery_RegisterDiscoveryReuseByModuleName 验证 A6 幂等化：
// 同一个 moduleName 多次调用 RegisterDiscovery 返回同一个 *client.ConsulDiscovery 实例。
// D5: 改用桩工厂，干净环境（无 Consul）确定性通过，并额外断言工厂只被调用一次。
func TestConsulDiscovery_RegisterDiscoveryReuseByModuleName(t *testing.T) {
	var factoryCalls atomic.Int32
	defer withStubConsulFactory(t, func(basePath, servicePath string, _ []string, _ *libkvstore.Config) (*client.ConsulDiscovery, error) {
		factoryCalls.Add(1)
		return newFakeConsulDiscovery(t, basePath, servicePath)
	})()

	c := &ConsulDiscovery{}
	c.initStruct()

	first := c.RegisterDiscovery("module-a")
	second := c.RegisterDiscovery("module-a")

	if first == nil {
		t.Fatalf("RegisterDiscovery should succeed with stub factory")
	}
	if first != second {
		t.Errorf("RegisterDiscovery for same moduleName should return same instance: %p vs %p", first, second)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Errorf("factory should be called exactly once (idempotent reuse), got %d", got)
	}
	first.Close()
}

// TestConsulDiscovery_ConcurrentRegisterNoLeak 验证并发 RegisterDiscovery 的幂等性：
// 100 个 goroutine 并发注册同一个 moduleName，discoveryMap 里只能有一个条目，
// 且所有调用方拿到的都是同一个非 nil 实例（竞争 loser 实例被 Close 回收）。
// D5: 桩工厂让本测试在无 Consul 的干净环境下确定性通过——v3 审计中本测试 5/5 失败
// 的根因（consul.go 吞错把 nil 永久缓存）已修复并由下面的 FailureNotCached 用例覆盖。
func TestConsulDiscovery_ConcurrentRegisterNoLeak(t *testing.T) {
	defer withStubConsulFactory(t, func(basePath, servicePath string, _ []string, _ *libkvstore.Config) (*client.ConsulDiscovery, error) {
		return newFakeConsulDiscovery(t, basePath, servicePath)
	})()

	c := &ConsulDiscovery{}
	c.initStruct()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	results := make([]*client.ConsulDiscovery, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = c.RegisterDiscovery("same-module")
		}()
	}
	wg.Wait()

	// 所有并发调用方拿到的必须是同一个非 nil 实例
	winner := c.GetDiscovery("same-module")
	if winner == nil {
		t.Fatalf("GetDiscovery should find registered entry")
	}
	for i := 0; i < N; i++ {
		if results[i] != winner {
			t.Errorf("goroutine %d got different instance %p, want winner %p", i, results[i], winner)
		}
	}
	// map 中只允许一个条目
	if got := c.discoveryMap.Len(); got != 1 {
		t.Errorf("discoveryMap should contain exactly 1 entry, got %d", got)
	}
	winner.Close()
}

// TestConsulDiscovery_RegisterFailureNotCached 是 D5 的核心回归：
// 创建失败（Consul 不可达）时——
//  1. RegisterDiscovery 返回 nil 且 **不把 nil 写进 discoveryMap**；
//  2. GetDiscovery 返回 nil（没有被 nil 永久占位）；
//  3. 故障恢复后（工厂转为成功）再次 RegisterDiscovery 能成功创建并缓存（可重试）。
//
// 旧实现 `d, _ :=` 吞错 + 无条件 Insert：步骤 1 把 nil 占进 map，步骤 3 的新实例
// 因幂等检查只认"条目存在"而永远存不进 map——Consul 先挂后恢复场景永久故障。
func TestConsulDiscovery_RegisterFailureNotCached(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	defer withStubConsulFactory(t, func(basePath, servicePath string, _ []string, _ *libkvstore.Config) (*client.ConsulDiscovery, error) {
		if fail.Load() {
			return nil, errors.New("consul unreachable (injected)")
		}
		return newFakeConsulDiscovery(t, basePath, servicePath)
	})()

	c := &ConsulDiscovery{}
	c.initStruct()

	// 故障期：注册失败
	if d := c.RegisterDiscovery("module-x"); d != nil {
		t.Fatalf("RegisterDiscovery should return nil when factory fails, got %p", d)
	}
	if got := c.GetDiscovery("module-x"); got != nil {
		t.Fatalf("GetDiscovery should return nil after failed register (no nil-poisoning), got %p", got)
	}
	if got := c.discoveryMap.Len(); got != 0 {
		t.Fatalf("discoveryMap should be empty after failed register, got %d entries", got)
	}

	// 故障恢复：重试成功并缓存
	fail.Store(false)
	d := c.RegisterDiscovery("module-x")
	if d == nil {
		t.Fatalf("RegisterDiscovery should succeed after recovery (retryable)")
	}
	if got := c.GetDiscovery("module-x"); got != d {
		t.Errorf("GetDiscovery should return the recovered instance: got %p want %p", got, d)
	}
	d.Close()
}
