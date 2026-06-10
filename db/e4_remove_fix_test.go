package db

// E4 · "删除数据双路复活" / tombstone 时间单位 / 脏数据驱逐保护 的白盒回归。
// 不依赖真实 Redis（用 recordingCacheService 替换包级 cache）与真实 MySQL
// （flushBatch 用捕获桩）。
//
// 覆盖：
//  1. hash Remove 删的是真正的 hash field（HDEL key field），而不是不存在的
//     字符串 key "keyFun:mKey:fieldKey"
//  2. Remove 把模型 State 置 0，落库 values 中携带 state=0（删除事实进 DB）
//  3. tombstone 过期时间按"秒"计（旧 bug：Duration 当秒用，放大 1e9 倍 ≈ 792 年）
//  4. autoClear 不驱逐 dirty（data_update 未清）条目

import (
	"sync"
	"testing"
	"time"

	"github.com/cornelk/hashmap"
	"golang.org/x/sync/singleflight"
)

// ---- 记录型 cache 桩 ----

type recordingCacheService struct {
	mu    sync.Mutex
	hdels [][2]string // {key, field}
	dels  []string
	maps  map[string]map[string]string
}

func newRecordingCacheService() *recordingCacheService {
	return &recordingCacheService{maps: map[string]map[string]string{}}
}

func (r *recordingCacheService) Get(key string) string { return "" }
func (r *recordingCacheService) Set(key string, val any, timeout time.Duration) {
}
func (r *recordingCacheService) GetMap(key string) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for k, v := range r.maps[key] {
		out[k] = v
	}
	return out
}
func (r *recordingCacheService) PutMap(key, field, val string, timeout time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maps[key] == nil {
		r.maps[key] = map[string]string{}
	}
	r.maps[key][field] = val
}
func (r *recordingCacheService) HDel(key string, fields ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range fields {
		r.hdels = append(r.hdels, [2]string{key, f})
		if m := r.maps[key]; m != nil {
			delete(m, f)
		}
	}
}
func (r *recordingCacheService) Del(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dels = append(r.dels, key)
}
func (r *recordingCacheService) DelNow(key string) { r.Del(key) }
func (r *recordingCacheService) GetList(key string, start, end int64) ([]string, error) {
	return nil, nil
}
func (r *recordingCacheService) SetList(key string, l []interface{}, timeout time.Duration)    {}
func (r *recordingCacheService) GetSet(key string) ([]string, error)                           { return nil, nil }
func (r *recordingCacheService) AddSetItem(key string, val interface{}, timeout time.Duration) {}
func (r *recordingCacheService) AddListItem(key string, val string, timeout time.Duration)     {}

func swapCacheService(t *testing.T, svc iCacheService) {
	t.Helper()
	old := cache
	cache = svc
	t.Cleanup(func() { cache = old })
}

// ---- 带 State 的 hash 模型 ----

type e4HashItem struct {
	Model
	UserId string `orm:"pk;pkList"`
	PropId string `orm:"pk"`
	Amount int64
}

func (i *e4HashItem) GetTableName() string                { return "e4_hash_item" }
func (i *e4HashItem) HashCachePkKey(key ...string) string { return "e4hash:" + key[0] }
func (i *e4HashItem) HashCacheFieldByVal() string         { return i.PropId }
func (i *e4HashItem) HashCacheFieldByKeys(key ...string) string {
	if len(key) > 1 {
		return key[1]
	}
	return ""
}

func newE4HashManager() *hashAutoCacheManager[*e4HashItem] {
	h := &hashAutoCacheManager[*e4HashItem]{}
	h.builder = &AutoCacheBuilder[string, *e4HashItem]{
		longevity:         true,
		longevityInterval: time.Second,
		cache:             true,
		keyFun:            "prop",
		cacheTimeOut:      time.Minute,
	}
	h.cacheMap = hashmap.New[string, *cacheData[*e4HashItem]]()
	h.longevityLock = &sync.Mutex{}
	h.sf = &singleflight.Group{}
	h.sb = &sqlBuilder[*e4HashItem]{
		tableName:      "e4_hash_item",
		modelFieldName: []string{"State", "UserId", "PropId", "Amount"},
		queryListSql:   "select `state`,`user_id`,`prop_id`,`amount` from e4_hash_item where user_id = ? and state = 1",
	}
	h.flushBatch = h.sb.flushBatch
	h.image = &e4HashItem{}
	h.groupAutoCacheManager = NewAutoCacheBuilder[string, []string]().WithMemCache(60).New()
	return h
}

// 1+2. hash Remove：HDEL 命中真正的 hash field + 模型 State 置 0 并随 flush 落库
func TestHashRemove_DeletesRealHashFieldAndMarksDBDelete(t *testing.T) {
	withDownMySQL(t) // loadCache 的 DB 分支直接失败，全部走本地路径
	rec := newRecordingCacheService()
	swapCacheService(t, rec)
	h := newE4HashManager()

	item := &e4HashItem{Model: NewModel(), UserId: "u1", PropId: "sword", Amount: 7}
	if !h.Set(item, "u1", "sword") {
		t.Fatalf("Set 失败")
	}
	if item.State != 1 {
		t.Fatalf("前置：新模型 State 应为 1")
	}

	// 捕获后续 flush 的 values（替换掉真实 flushBatch）
	var flushed [][]any
	h.flushBatch = func(values []any, count int) error {
		cp := make([]any, len(values))
		copy(cp, values)
		flushed = append(flushed, cp)
		return nil
	}

	if !h.Remove("u1", "sword") {
		t.Fatalf("Remove 失败")
	}

	// Redis 侧：必须 HDEL 真正的 hash key + field
	rec.mu.Lock()
	hdels := append([][2]string{}, rec.hdels...)
	dels := append([]string{}, rec.dels...)
	rec.mu.Unlock()
	wantKey, wantField := "prop:e4hash:u1", "sword"
	found := false
	for _, hd := range hdels {
		if hd[0] == wantKey && hd[1] == wantField {
			found = true
		}
	}
	if !found {
		t.Errorf("Remove 应 HDel(%q, %q)，实际 hdels=%v", wantKey, wantField, hdels)
	}
	for _, d := range dels {
		if d == "prop:e4hash:u1:sword" {
			t.Errorf("Remove 不应再 Del 不存在的字符串 key %q（旧 bug：删错 key）", d)
		}
	}

	// DB 侧：模型 State 必须被置 0，且删除事实随 flush 进入落库 values
	if item.State != 0 {
		t.Errorf("Remove 后模型 State 应为 0（删除态），实际 %d", item.State)
	}
	h.toLongevity()
	if len(flushed) == 0 {
		t.Fatalf("Remove 后应有一轮 flush（data_update 置位）")
	}
	// modelFieldName[0] == State
	if got, ok := flushed[0][0].(uint8); !ok || got != 0 {
		t.Errorf("落库 values 的 state 应为 uint8(0)，实际 %T(%v)", flushed[0][0], flushed[0][0])
	}
}

// 3. tombstone 时间单位：按秒计而不是把 Duration 当秒（792 年 bug）
func TestRemove_TombstoneExpiresInSecondsNotCenturies(t *testing.T) {
	m := newTestManager(10)
	m.builder.longevityInterval = time.Second // 5*1s → tombstone 5 秒
	cd := &cacheData[*fakeModel]{data: &fakeModel{Uid: "u", Name: "n"}}
	m.cacheMap.Set("k", cd)

	if !m.Remove("k") {
		t.Fatalf("Remove 失败")
	}

	now := time.Now().Unix()
	ct := cd.clearTime.Load()
	if ct <= now {
		t.Fatalf("tombstone clearTime 应在未来，实际 %d (now=%d)", ct, now)
	}
	if diff := ct - now; diff > 60 {
		t.Fatalf("tombstone 过期应为秒级（约 5s），实际 %d 秒后（旧 bug：约 792 年）", diff)
	}
	if !cd.checkState(data_del) || !cd.checkState(data_update) {
		t.Errorf("Remove 后应同时置 data_del 与 data_update")
	}
	// 过期判定可被 autoClear 命中（flush 清脏后）
	cd.removeState(data_update) // 模拟删除事实已成功落库
	if !cd.checkTimeOut(now + 61) {
		t.Errorf("tombstone 应在秒级窗口后过期")
	}
}

// 2b. 非 hash Remove 同样把删除事实写进落库数据
func TestRemove_MarksModelStateZero(t *testing.T) {
	type e4User struct {
		Model
		Uid string `orm:"pk"`
	}
	m := &autoCacheManager[string, *e4User]{
		cacheMap:      hashmap.New[string, *cacheData[*e4User]](),
		longevityLock: &sync.Mutex{},
		builder: &AutoCacheBuilder[string, *e4User]{
			longevity:         true,
			longevityInterval: time.Second,
		},
		sb: &sqlBuilder[*e4User]{
			tableName:      "e4_user",
			modelFieldName: []string{"State", "Uid"},
		},
	}
	u := &e4User{Model: NewModel(), Uid: "u1"}
	cd := newCacheData[*e4User](u, 0)
	m.cacheMap.Set("u1", cd)

	m.Remove("u1")

	if u.State != 0 {
		t.Fatalf("Remove 后模型 State 应为 0，实际 %d", u.State)
	}
	var flushed [][]any
	m.flushBatch = func(values []any, count int) error {
		cp := make([]any, len(values))
		copy(cp, values)
		flushed = append(flushed, cp)
		return nil
	}
	m.toLongevity()
	if len(flushed) != 1 {
		t.Fatalf("Remove 后应触发一轮 flush，实际 %d", len(flushed))
	}
	if got, ok := flushed[0][0].(uint8); !ok || got != 0 {
		t.Errorf("落库 values 的 state 应为 uint8(0)，实际 %T(%v)", flushed[0][0], flushed[0][0])
	}
}

// 4. autoClear 不驱逐 dirty 条目
func TestAutoClear_KeepsDirtyEvictsClean(t *testing.T) {
	m := newTestManager(10)

	past := time.Now().Unix() - 100

	dirty := &cacheData[*fakeModel]{data: &fakeModel{Uid: "d"}}
	dirty.update()
	dirty.clearTime.Store(past)
	m.cacheMap.Set("dirty", dirty)

	clean := &cacheData[*fakeModel]{data: &fakeModel{Uid: "c"}}
	clean.clearTime.Store(past)
	m.cacheMap.Set("clean", clean)

	m.autoClear()

	if _, ok := m.cacheMap.Get("dirty"); !ok {
		t.Errorf("dirty（未落库）条目不应被 autoClear 驱逐——驱逐即丢未落库修改")
	}
	if _, ok := m.cacheMap.Get("clean"); ok {
		t.Errorf("过期且干净的条目应被驱逐")
	}
}
