package db_test

import (
	"fmt"
	"github.com/thkhxm/tgf/db"
	"reflect"
	"testing"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/14
//***************************************************

func TestDefaultAutoCacheManager(t *testing.T) {
	db.Run()
	cacheManager := db.NewDefaultAutoCacheManager[string, int64]("example")
	key := "1001"
	var setVal int64 = 10086
	val, err := cacheManager.Get(key)
	if err != nil {
		t.Errorf("[test] cache get error %v", err)
		return
	}
	//
	t.Logf("[test] cache first get key %v , val %v", key, val)

	cacheManager.Set(setVal, key)
	t.Logf("[test] cache set key %v , val %v ", key, setVal)
	val, err = cacheManager.Get(key)
	if err != nil {
		t.Errorf("[test] cache get error %v", err)
		return
	}
	t.Logf("[test] cache second get key %v , val %v", key, val)

	//first run
	//cache_test.go:26: [test] cache first get key 1001 , val 0
	//cache_test.go:29: [test] cache set key 1001 , val 0
	//cache_test.go:35: [test] cache second get key 1001 , val 10086

	//second run
	//cache_test.go:26: [test] cache first get key 1001 , val 10086
	//cache_test.go:29: [test] cache set key 1001 , val 10086
	//cache_test.go:35: [test] cache second get key 1001 , val 10086
}

func TestNewAutoCacheBuilder(t *testing.T) {
	db.Run()
	builder := db.NewAutoCacheBuilder[string, *ExampleUser]()
	builder.WithLongevityCache(time.Second * 5)
	builder.WithAutoCache("example", time.Minute*5)
	manager := builder.New()
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("%v", i)
		name := fmt.Sprintf("tim2-%v", i)
		data, _ := manager.Get(id)
		if data == nil {
			data = &ExampleUser{
				Id: id, NickName: name,
			}
			manager.Set(data, id)
		}
		data.NickName = name
		manager.Push(data.Id)
	}
	manager.Reset()
	time.Sleep(time.Second * 5)
	//var longevityManager = db.NewLongevityAutoCacheManager[string, *ExampleUser]("test:user")
	//data, _ := longevityManager.Get("1", "2")
	//t.Log("---->", *data)
}

type DemoUser struct {
	Name string
}

type ExampleUser struct {
	Id       string `orm:"pk"`
	NickName string
	ignore   string
}

func (e ExampleUser) GetTableName() string {
	return "t_user"
}

func TestGet(t *testing.T) {
	db.Run()
	key := "testKey"
	expectedVal := "testValue"
	db.Set(key, expectedVal, time.Minute)

	val, success := db.Get[string](key)
	if !success || val != expectedVal {
		t.Errorf("Get() = %v, want %v", val, expectedVal)
	}
}

func TestSet(t *testing.T) {
	db.Run()
	key := "testKey"
	val := "testValue"
	db.Set(key, val, time.Minute)

	retrievedVal, success := db.Get[string](key)
	if !success || retrievedVal != val {
		t.Errorf("Set() failed, retrieved value = %v, want %v", retrievedVal, val)
	}
}

func TestGetMap(t *testing.T) {
	db.Run()
	key := "testKey"
	expectedMap := map[string]string{"field1": "value1", "field2": "value2"}
	db.PutMap(key, "field1", "value1", time.Minute)
	db.PutMap(key, "field2", "value2", time.Minute)

	retrievedMap, success := db.GetMap[string, string](key)
	if !success || !reflect.DeepEqual(retrievedMap, expectedMap) {
		t.Errorf("GetMap() = %v, want %v", retrievedMap, expectedMap)
	}
}

func TestPutMap(t *testing.T) {
	db.Run()

	key := "testKey"
	field := "testField"
	val := "testValue"
	db.PutMap(key, field, val, time.Minute)

	retrievedMap, success := db.GetMap[string, string](key)
	if !success || retrievedMap[field] != val {
		t.Errorf("PutMap() failed, retrieved value = %v, want %v", retrievedMap[field], val)
	}
}

func TestGetList(t *testing.T) {
	db.Run()

	key := "testListKey"
	expectedList := []string{"value1", "value2", "value3"}
	db.DelNow(key)

	db.AddListItem(key, time.Minute, expectedList...)

	retrievedList := db.GetList[string](key)
	if !reflect.DeepEqual(retrievedList, expectedList) {
		t.Errorf("GetList() = %v, want %v", retrievedList, expectedList)
	}
}

func TestAddListItem(t *testing.T) {
	db.Run()
	key := "testKey"
	val := "testValue"
	db.AddListItem(key, time.Minute, val)

	retrievedList := db.GetList[string](key)
	if len(retrievedList) != 1 || retrievedList[0] != val {
		t.Errorf("AddListItem() failed, retrieved list = %v, want %v", retrievedList, []string{val})
	}
}

func TestDel(t *testing.T) {
	db.Run()
	key := "testKey"
	val := "testValue"
	db.Set(key, val, time.Minute)
	db.Del(key)

	_, success := db.Get[string](key)
	if success {
		t.Errorf("Del() failed, value still exists for key %v", key)
	}
}

func TestDelNow(t *testing.T) {
	db.Run()
	key := "testKey"
	val := "testValue"
	db.Set(key, val, time.Minute)
	db.DelNow(key)

	_, success := db.Get[string](key)
	if !success {
		t.Errorf("DelNow() failed, value still exists for key %v", key)
	}
}
