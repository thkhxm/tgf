package component

// C5 · GameConfig 热更单测。
//
// 覆盖：
//   1. ReloadGameConf 在文件改变后能把新数据送到 getCacheGameConfData
//   2. OnReload 钩子按注册顺序触发
//   3. StartConfigWatcher 的 fsnotify 路径能在文件写入后自动 reload
//   4. 反序列化失败时旧数据保留（故意喂坏 JSON）
//   5. ReloadGameConf 的并发调用安全

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rewriteJSON 覆盖 dir/hero.json 的内容。
func rewriteJSON(t *testing.T, dir, payload string) {
	t.Helper()
	path := filepath.Join(dir, "hero.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("写 JSON 失败: %v", err)
	}
}

func TestReloadGameConf_ReplacesContent(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"Arthur"}]`)
	WithConfPath(dir)
	InitGameConfToMem()

	// 第一次读触发懒加载 + registerLoader
	v, ok := GetGameConf[*heroConf]("h1")
	if !ok || v.Name != "Arthur" {
		t.Fatalf("初始读期望 Arthur, 实际 %+v ok=%v", v, ok)
	}

	// 改 JSON 内容后手动 Reload
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"Galahad"},{"Id":"h2","Name":"Percival"}]`)
	if err := ReloadGameConf(); err != nil {
		t.Fatalf("ReloadGameConf 错误: %v", err)
	}

	v, ok = GetGameConf[*heroConf]("h1")
	if !ok || v.Name != "Galahad" {
		t.Fatalf("reload 后期望 Galahad, 实际 %+v ok=%v", v, ok)
	}
	v2, ok := GetGameConf[*heroConf]("h2")
	if !ok || v2.Name != "Percival" {
		t.Fatalf("reload 后期望 h2=Percival, 实际 %+v ok=%v", v2, ok)
	}
}

func TestOnReload_HooksFireInOrder(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"A"}]`)
	WithConfPath(dir)
	InitGameConfToMem()
	_, _ = GetGameConf[*heroConf]("h1") // 注册 loader

	var order []string
	var mu sync.Mutex
	OnReload(func() { mu.Lock(); order = append(order, "a"); mu.Unlock() })
	OnReload(func() { mu.Lock(); order = append(order, "b"); mu.Unlock() })
	OnReload(func() { mu.Lock(); order = append(order, "c"); mu.Unlock() })

	if err := ReloadGameConf(); err != nil {
		t.Fatalf("reload err: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("期望钩子按 a,b,c 顺序触发, 实际 %v", order)
	}
}

// TestOnReload_PanicRecovered 验证 OnReload 的回调 panic 不会影响其它回调
// 也不会影响 ReloadGameConf 本身。
func TestOnReload_PanicRecovered(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"A"}]`)
	WithConfPath(dir)
	InitGameConfToMem()
	_, _ = GetGameConf[*heroConf]("h1")

	var survived atomic.Bool
	OnReload(func() { panic("boom") })
	OnReload(func() { survived.Store(true) })

	if err := ReloadGameConf(); err != nil {
		t.Fatalf("reload err: %v", err)
	}
	if !survived.Load() {
		t.Fatal("panic 钩子后续钩子应当继续执行")
	}
}

// TestReloadGameConf_BadJSONKeepsOldData 验证反序列化失败时旧数据被保留。
func TestReloadGameConf_BadJSONKeepsOldData(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"Origin"}]`)
	WithConfPath(dir)
	InitGameConfToMem()
	_, _ = GetGameConf[*heroConf]("h1")

	// 改成坏 JSON
	rewriteJSON(t, dir, `not a valid json`)
	_ = ReloadGameConf()

	v, ok := GetGameConf[*heroConf]("h1")
	if !ok || v.Name != "Origin" {
		t.Fatalf("坏 JSON reload 后期望保留 Origin, 实际 %+v ok=%v", v, ok)
	}
}

// TestReloadGameConf_Concurrent 验证并发 Reload 不出 race。
// reloadMu 负责序列化，理论上不会 data race。
func TestReloadGameConf_Concurrent(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"A"}]`)
	WithConfPath(dir)
	InitGameConfToMem()
	_, _ = GetGameConf[*heroConf]("h1")

	var wg sync.WaitGroup
	wg.Add(16)
	for i := 0; i < 16; i++ {
		go func() {
			defer wg.Done()
			_ = ReloadGameConf()
			_, _ = GetGameConf[*heroConf]("h1")
		}()
	}
	wg.Wait()
}

// TestStartConfigWatcher_AutoReload 验证 fsnotify 路径。
// 缩短 debounce 到 50ms 加速测试；修改文件后等待 debounce + 余量。
func TestStartConfigWatcher_AutoReload(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	// 测试专用 debounce：默认 200ms 太慢，压到 30ms 让测试在 200ms 内结束。
	oldDebounce := configWatcherDebounce
	configWatcherDebounce = 30 * time.Millisecond
	defer func() { configWatcherDebounce = oldDebounce }()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"Before"}]`)
	WithConfPath(dir)
	InitGameConfToMem()
	_, _ = GetGameConf[*heroConf]("h1")

	reloaded := make(chan struct{}, 1)
	OnReload(func() {
		select {
		case reloaded <- struct{}{}:
		default:
		}
	})

	if err := StartConfigWatcher(); err != nil {
		t.Fatalf("StartConfigWatcher err: %v", err)
	}
	defer StopConfigWatcher()

	// 写新内容触发 fsnotify
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"After"}]`)

	select {
	case <-reloaded:
	case <-time.After(3 * time.Second):
		t.Fatal("fsnotify 事件后 3s 内未触发 OnReload")
	}

	v, ok := GetGameConf[*heroConf]("h1")
	if !ok || v.Name != "After" {
		t.Fatalf("watcher 触发 reload 后期望 After, 实际 %+v ok=%v", v, ok)
	}
}

// TestStartConfigWatcher_Idempotent 验证重复 Start/Stop 的幂等性。
func TestStartConfigWatcher_Idempotent(t *testing.T) {
	resetGameConfForTest()
	defer resetGameConfForTest()

	dir := t.TempDir()
	rewriteJSON(t, dir, `[{"Id":"h1","Name":"A"}]`)
	WithConfPath(dir)
	InitGameConfToMem()

	if err := StartConfigWatcher(); err != nil {
		t.Fatalf("第一次 Start err: %v", err)
	}
	if err := StartConfigWatcher(); err != nil {
		t.Fatalf("第二次 Start 应 no-op, 实际 err: %v", err)
	}
	StopConfigWatcher()
	StopConfigWatcher() // 重复 stop 应当安全
}
