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
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/14
//***************************************************

func TestDefaultAutoCacheManager(t *testing.T) {
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

func TestGetList(t *testing.T) {
	type args struct {
		key string
	}
	type testCase[Res any] struct {
		name string
		args args
		want []Res
	}
	tests := []testCase[string]{
		{"1", args{key: "k1"}, []string{"4", "3", "2", "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := db.GetList[string](tt.args.key); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDel(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.Del(tt.args.key)
		})
	}
}

func TestDelNow(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.DelNow(tt.args.key)
		})
	}
}

func TestGetMap(t *testing.T) {
	//type args struct {
	//	key string
	//}
	//type testCase[Key cacheKey, Val any] struct {
	//	name        string
	//	args        args
	//	wantRes     map[Key]Val
	//	wantSuccess bool
	//}
	//tests := []testCase[ /* TODO: Insert concrete types here */ ]{
	//	// TODO: Add test cases.
	//}
	//for _, tt := range tests {
	//	t.Run(tt.name, func(t *testing.T) {
	//		gotRes, gotSuccess := db.GetMap(tt.args.key)
	//		if !reflect.DeepEqual(gotRes, tt.wantRes) {
	//			t.Errorf("GetMap() gotRes = %v, want %v", gotRes, tt.wantRes)
	//		}
	//		if gotSuccess != tt.wantSuccess {
	//			t.Errorf("GetMap() gotSuccess = %v, want %v", gotSuccess, tt.wantSuccess)
	//		}
	//	})
	//}
}

func TestPutMap(t *testing.T) {
	//type args[Key cacheKey, Val any] struct {
	//	key     string
	//	field   db.Key
	//	val     db.Val
	//	timeout time.Duration
	//}
	//type testCase[Key cacheKey, Val any] struct {
	//	name string
	//	args args[Key, Val]
	//}
	//tests := []testCase[ /* TODO: Insert concrete types here */ ]{
	//	// TODO: Add test cases.
	//}
	//for _, tt := range tests {
	//	t.Run(tt.name, func(t *testing.T) {
	//		db.PutMap(tt.args.key, tt.args.field, tt.args.val, tt.args.timeout)
	//	})
	//}
}

func TestSet(t *testing.T) {
	type args struct {
		key     string
		val     any
		timeout time.Duration
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db.Set(tt.args.key, tt.args.val, tt.args.timeout)
		})
	}
}

func TestAddListItem(t *testing.T) {
	type args[Val any] struct {
		key     string
		l       []Val
		timeout time.Duration
	}
	type testCase[Val any] struct {
		name string
		args args[Val]
	}
	tests := []testCase[string]{
		{"1", args[string]{
			key:     "k1",
			l:       []string{"1", "1", "1", "1"},
			timeout: 0,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = db.AddListItem[string](tt.args.key, tt.args.timeout, tt.args.l...)
		})
	}
}

func TestNewAutoCacheBuilder(t *testing.T) {
	builder := db.NewAutoCacheBuilder[string, *ExampleUser]()
	builder.WithLongevityCache()
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
