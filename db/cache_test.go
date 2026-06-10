//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description E5 重写：Redis 缓存导出 API 的真实集成测试。
//
// 原版（2023/3/14）的问题（V3 审计 P3，db/cache_test.go:187-198）：
//   - TestDelNow 断言写反：DelNow 后断言 Get 成功，错误消息却说 "value still exists"；
//   - TestDel 误解 Del 语义：Del 是"设 1 秒过期"而非立即删除，删后立读必然仍命中；
//   - 所有用例依赖外部手工起的 Redis/MySQL，从未在 CI 真正跑过；
//   - TestNewAutoCacheBuilder 以 sleep 5s 收尾且无任何断言。
//
// 现版本全部走 testcontainers 真实 Redis（见 it_env_test.go harness），
// 断言修正为真实语义，并以 waitUntil 轮询替代裸 sleep。
// write-behind / hash 管理器的真实链路测试见 it_writebehind_test.go。
//
//2023/3/14（E5 重写 2026/6/10）
//***************************************************

import (
	"reflect"
	"testing"
	"time"
)

func TestDefaultAutoCacheManager(t *testing.T) {
	ensureIT(t)
	cacheManager := NewDefaultAutoCacheManager[string, int64]("it:example")
	key := "1001"
	var setVal int64 = 10086

	// 首读：无数据（本地 miss → Redis miss，且该管理器无 DB 层）。
	if val, err := cacheManager.Get(key); err == nil {
		t.Fatalf("首读不应命中, got %v", val)
	}

	if ok := cacheManager.Set(setVal, key); !ok {
		t.Fatal("Set 失败")
	}
	val, err := cacheManager.Get(key)
	if err != nil {
		t.Fatalf("Set 后 Get 失败: %v", err)
	}
	if val != setVal {
		t.Fatalf("Get = %v, want %v", val, setVal)
	}

	// Redis 层真实写入：绕过本地缓存直接读 Redis key。
	if rv, ok := Get[int64]("it:example:1001"); !ok || rv != setVal {
		t.Fatalf("Redis 层未真实写入 ok=%v rv=%v", ok, rv)
	}
}

func TestGet(t *testing.T) {
	ensureIT(t)
	key := "it:cache:get"
	expectedVal := "testValue"
	DelNow(key)
	Set(key, expectedVal, time.Minute)

	val, success := Get[string](key)
	if !success || val != expectedVal {
		t.Errorf("Get() = %v(success=%v), want %v", val, success, expectedVal)
	}
}

func TestSet(t *testing.T) {
	ensureIT(t)
	key := "it:cache:set"
	val := "testValue"
	DelNow(key)
	Set(key, val, time.Minute)

	retrievedVal, success := Get[string](key)
	if !success || retrievedVal != val {
		t.Errorf("Set() failed, retrieved value = %v, want %v", retrievedVal, val)
	}
}

func TestGetMap(t *testing.T) {
	ensureIT(t)
	key := "it:cache:map"
	expectedMap := map[string]string{"field1": "value1", "field2": "value2"}
	DelNow(key)
	PutMap(key, "field1", "value1", time.Minute)
	PutMap(key, "field2", "value2", time.Minute)

	retrievedMap, success := GetMap[string, string](key)
	if !success || !reflect.DeepEqual(retrievedMap, expectedMap) {
		t.Errorf("GetMap() = %v, want %v", retrievedMap, expectedMap)
	}
}

func TestPutMap(t *testing.T) {
	ensureIT(t)
	key := "it:cache:putmap"
	field := "testField"
	val := "testValue"
	DelNow(key)
	PutMap(key, field, val, time.Minute)

	retrievedMap, success := GetMap[string, string](key)
	if !success || retrievedMap[field] != val {
		t.Errorf("PutMap() failed, retrieved value = %v, want %v", retrievedMap[field], val)
	}
}

func TestGetList(t *testing.T) {
	ensureIT(t)
	key := "it:cache:list"
	expectedList := []string{"value1", "value2", "value3"}
	DelNow(key)

	if err := AddListItem(key, time.Minute, expectedList...); err != nil {
		t.Fatalf("AddListItem error: %v", err)
	}
	retrievedList := GetList[string](key)
	if !reflect.DeepEqual(retrievedList, expectedList) {
		t.Errorf("GetList() = %v, want %v", retrievedList, expectedList)
	}
}

func TestAddListItem(t *testing.T) {
	ensureIT(t)
	key := "it:cache:listitem"
	val := "testValue"
	DelNow(key)
	if err := AddListItem(key, time.Minute, val); err != nil {
		t.Fatalf("AddListItem error: %v", err)
	}

	retrievedList := GetList[string](key)
	if len(retrievedList) != 1 || retrievedList[0] != val {
		t.Errorf("AddListItem() failed, retrieved list = %v, want %v", retrievedList, []string{val})
	}
}

// TestDel 修正原版语义错误：Del 是"设 1 秒过期"（延迟删除），
// 删后立读仍命中是符合设计的；1 秒后必须不可见。
func TestDel(t *testing.T) {
	ensureIT(t)
	key := "it:cache:del"
	val := "testValue"
	DelNow(key)
	Set(key, val, time.Minute)
	Del(key)

	// 立即读：仍命中（延迟删除语义）。
	if _, success := Get[string](key); !success {
		t.Errorf("Del() 是延迟删除(1s 过期), 删后立读应仍命中")
	}
	// 过期窗口之后必须消失。
	waitUntil(t, 5*time.Second, "Del 过期后 key 消失", func() bool {
		_, success := Get[string](key)
		return !success
	})
}

// TestDelNow 修正原版写反的断言（V3 审计 P3）：DelNow 后 Get 必须失败。
func TestDelNow(t *testing.T) {
	ensureIT(t)
	key := "it:cache:delnow"
	val := "testValue"
	Set(key, val, time.Minute)
	DelNow(key)

	if _, success := Get[string](key); success {
		t.Errorf("DelNow() failed, value still exists for key %v", key)
	}
}
