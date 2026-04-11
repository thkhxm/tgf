package db

// 白盒测试：直接构造 autoCacheManager，绕过 InitStruct 对真实 MySQL 的依赖，
// 只覆盖 A1 重点关心的 toLongevity 正确性：
//   1. 单批/多批全部成功时所有脏标志被清
//   2. 某批落库失败时仅该批保留脏标志，其余被清
//   3. TryLock 被占用时直接返回、不动 cacheMap
//   4. groupSize 切分按配置生效

import (
	"errors"
	"sync"
	"testing"

	"github.com/cornelk/hashmap"
)

type fakeModel struct {
	Uid  string
	Name string
}

// newTestManager 构造一个最小的 autoCacheManager，longevity 开启但跳过真实 SQL。
// fieldCount 决定 flushBatch 内部的 count 推导，这里固定为 2（uid + name）。
func newTestManager(groupSize int) *autoCacheManager[string, *fakeModel] {
	m := &autoCacheManager[string, *fakeModel]{
		cacheMap:      hashmap.New[string, *cacheData[*fakeModel]](),
		longevityLock: &sync.Mutex{},
		builder: &AutoCacheBuilder[string, *fakeModel]{
			longevity:          true,
			longevityGroupSize: groupSize,
		},
		sb: &sqlBuilder[*fakeModel]{
			tableName:      "fake",
			modelFieldName: []string{"Uid", "Name"},
		},
	}
	return m
}

// seedDirty 往 cacheMap 塞 n 条 dirty 数据并返回它们的指针，便于逐条断言 state。
func seedDirty(m *autoCacheManager[string, *fakeModel], n int) []*cacheData[*fakeModel] {
	items := make([]*cacheData[*fakeModel], n)
	for i := 0; i < n; i++ {
		cd := &cacheData[*fakeModel]{data: &fakeModel{Uid: "u", Name: "n"}}
		cd.update()
		key := "k" + string(rune('0'+i))
		m.cacheMap.Set(key, cd)
		items[i] = cd
	}
	return items
}

func TestToLongevity_AllSuccess(t *testing.T) {
	m := newTestManager(2)
	items := seedDirty(m, 5)

	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		return nil
	}

	m.toLongevity()

	if calls == 0 {
		t.Fatalf("flushBatch should be called at least once")
	}
	for i, cd := range items {
		if cd.checkState(data_update) {
			t.Errorf("item %d still has data_update flag after successful flush", i)
		}
	}
}

func TestToLongevity_PartialFailureKeepsDirty(t *testing.T) {
	m := newTestManager(2) // groupSize=2 → 5 条会切成 3 批 (2,2,1)
	items := seedDirty(m, 5)

	// 让第 2 次调用失败，其它成功
	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		if calls == 2 {
			return errors.New("boom")
		}
		return nil
	}

	m.toLongevity()

	if calls != 3 {
		t.Fatalf("expected 3 batches, got %d", calls)
	}

	// 统计还有多少条保留了脏标志——应当恰好等于失败那一批的大小（groupSize=2）
	dirtyLeft := 0
	for _, cd := range items {
		if cd.checkState(data_update) {
			dirtyLeft++
		}
	}
	if dirtyLeft != 2 {
		t.Errorf("expected 2 items keeping dirty flag (failed batch), got %d", dirtyLeft)
	}
}

func TestToLongevity_TryLockBusySkipsWithoutSideEffect(t *testing.T) {
	m := newTestManager(10)
	items := seedDirty(m, 3)

	// 先持锁，让 toLongevity 的 TryLock 失败
	m.longevityLock.Lock()
	defer m.longevityLock.Unlock()

	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		return nil
	}

	m.toLongevity()

	if calls != 0 {
		t.Errorf("flushBatch should not be called when TryLock fails, got calls=%d", calls)
	}
	for i, cd := range items {
		if !cd.checkState(data_update) {
			t.Errorf("item %d lost data_update flag even though flush was skipped", i)
		}
	}
}

func TestToLongevity_GroupSizeFromBuilder(t *testing.T) {
	m := newTestManager(3) // 7 条 → 切成 3 批 (3,3,1)
	seedDirty(m, 7)

	var batchSizes []int
	m.flushBatch = func(values []any, count int) error {
		batchSizes = append(batchSizes, count)
		return nil
	}

	m.toLongevity()

	// 注意：cacheMap 遍历是无序的，但每批的大小应当满足 size<=3 且总和==7
	total := 0
	for _, s := range batchSizes {
		if s <= 0 || s > 3 {
			t.Errorf("invalid batch size %d", s)
		}
		total += s
	}
	if total != 7 {
		t.Errorf("batch total = %d, want 7", total)
	}
	if len(batchSizes) != 3 {
		t.Errorf("expected 3 batches, got %d", len(batchSizes))
	}
}

func TestToLongevity_NoDirtyNoCall(t *testing.T) {
	m := newTestManager(2)
	// 不标脏，只 set 数据
	cd := &cacheData[*fakeModel]{data: &fakeModel{Uid: "u", Name: "n"}}
	m.cacheMap.Set("k", cd)

	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		return nil
	}

	m.toLongevity()

	if calls != 0 {
		t.Errorf("flushBatch should not be called when nothing is dirty, got calls=%d", calls)
	}
}
