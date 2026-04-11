package component

// B6 · component 并发访问回归
//
// 目标：
//   1. 覆盖 InitGameConfToMem 的文件加载路径（依赖纯内存 cache，不需要 Redis）
//   2. 覆盖 GetGameConf / RangeGameConf 在并发读场景下 `-race` 下的行为
//   3. 覆盖首次访问时 `getCacheGameConfData` 的 double-check 分支
//
// 因为 game_config.go 里的 contextDataManager / cacheDataManager 是包私有，
// 测试文件也用 package component 做内部白盒测试（对比 `db/manager_internal_test.go`
// 的做法）。

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// heroConf 是一个极简测试类型：
//   - 第一个字段必须是 ID（LoadGameConf 用 reflect Field(0) 取 id）
//   - 类型名必须和"文件名去掉 .json 后 + Conf"一致，这是现有约定：
//     文件名 "hero.json" → key "heroConf" → 对应 heroConf 类型
type heroConf struct {
	Id   string
	Name string
}

// writeJSONConf 写一份测试 JSON 到指定目录。内容故意简单到可以手写序列化，
// 避免依赖 encoding/json 之类的包级 init 副作用。
func writeJSONConf(t *testing.T, dir string) {
	t.Helper()
	payload := `[{"Id":"h1","Name":"Arthur"},{"Id":"h2","Name":"Lancelot"},{"Id":"h1","Name":"Arthur2"}]`
	path := filepath.Join(dir, "hero.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("写测试 JSON 失败: %v", err)
	}
}

// TestInitGameConfToMem_LoadsFile 验证加载路径：
//
//	WithConfPath → InitGameConfToMem → contextDataManager 能读出原始字节
func TestInitGameConfToMem_LoadsFile(t *testing.T) {
	dir := t.TempDir()
	writeJSONConf(t, dir)

	WithConfPath(dir)
	InitGameConfToMem()

	if contextDataManager == nil {
		t.Fatal("InitGameConfToMem 后 contextDataManager 不应为 nil")
	}
	raw, _ := contextDataManager.Get("heroConf")
	if len(raw) == 0 {
		t.Fatal("期望 heroConf key 有原始字节")
	}
	if string(raw[0]) != "[" {
		t.Fatalf("期望以 [ 开头的 JSON, 实际 %q", string(raw))
	}
}

// TestGetGameConf_FirstAccessLoadsLazy 验证 getCacheGameConfData 的懒加载路径：
// 第一次 Get 会触发 LoadGameConf 把 contextDataManager 里的字节反序列化后写到
// cacheDataManager。
func TestGetGameConf_FirstAccessLoadsLazy(t *testing.T) {
	dir := t.TempDir()
	writeJSONConf(t, dir)
	WithConfPath(dir)
	InitGameConfToMem()

	// 第一次 Get —— 触发 LoadGameConf
	v, ok := GetGameConf[*heroConf]("h2")
	if !ok {
		t.Fatal("期望找到 h2")
	}
	if v == nil || v.Name != "Lancelot" {
		t.Fatalf("期望 Lancelot, 实际 %+v", v)
	}

	// 同 ID 有两条记录时 GetGameConf 只返回第一条（接口约定）
	v1, ok := GetGameConf[*heroConf]("h1")
	if !ok {
		t.Fatal("期望找到 h1")
	}
	if v1.Name != "Arthur" {
		t.Fatalf("期望第一条 Arthur, 实际 %v", v1.Name)
	}

	// GetGameConfBySlice 应返回全部两条
	list, ok := GetGameConfBySlice[*heroConf]("h1")
	if !ok || len(list) != 2 {
		t.Fatalf("期望 h1 有 2 条, 实际 ok=%v len=%d", ok, len(list))
	}
}

// TestGetGameConf_ConcurrentRead 在 -race 下验证：
// 多个 goroutine 并发 Get 同一个 key，不会出现数据竞争。
// 这个用例刻意命中 getCacheGameConfData 的懒加载分支——第一次进入时 data==nil
// 会加锁调 LoadGameConf，后续并发读走 fast path。
func TestGetGameConf_ConcurrentRead(t *testing.T) {
	dir := t.TempDir()
	writeJSONConf(t, dir)
	WithConfPath(dir)
	InitGameConfToMem()

	const (
		goroutines = 32
		perRoutine = 200
	)
	var (
		wg      sync.WaitGroup
		failMu  sync.Mutex
		failMsg string
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				v, ok := GetGameConf[*heroConf]("h2")
				if !ok || v == nil || v.Name != "Lancelot" {
					failMu.Lock()
					if failMsg == "" {
						failMsg = "并发读返回错误数据"
					}
					failMu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	if failMsg != "" {
		t.Fatal(failMsg)
	}
}

// TestRangeGameConf_IteratesAllEntries 确认 RangeGameConf 能遍历所有 id。
func TestRangeGameConf_IteratesAllEntries(t *testing.T) {
	dir := t.TempDir()
	writeJSONConf(t, dir)
	WithConfPath(dir)
	InitGameConfToMem()

	// 先触发一次懒加载
	_, _ = GetGameConf[*heroConf]("h1")

	seen := map[string]int{}
	RangeGameConf[*heroConf](func(key string, h *heroConf) bool {
		seen[h.Id]++
		return true
	})
	if seen["h1"] != 2 || seen["h2"] != 1 {
		t.Fatalf("期望 h1=2 h2=1, 实际 %+v", seen)
	}
}
