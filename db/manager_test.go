package db_test

import (
	"fmt"
	"github.com/thkhxm/tgf/db"
	"reflect"
	"strings"
	"testing"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/16
//***************************************************

type Cc struct {
	CacheExampleData
}

func (c *Cc) GetTableName() string {
	return "user"
}

func Test_autoCacheManager_Get(t *testing.T) {
	db.Run()
	cacheManager := db.NewLongevityAutoCacheManager[string, *Cc]("example:cachemanager")
	key := "123"
	val := &Cc{
		CacheExampleData: CacheExampleData{Name: "tim", Model: db.NewModel()},
	}
	cacheManager.Set(val, key)
	//time.Sleep(time.Second * 2)
	//val.Remove()
	//cacheManager.Remove(key)
	//time.Sleep(time.Second * 1)
	if cc, e := cacheManager.Get(key); e == nil {
		t.Log("get val ", cc)
	} else {
		t.Log("get val err ", e)
	}
	select {}
	//data, _ := cacheManager.Get(key)
	//data.Name = "sam"
	//t.Log("get val ", data, reflect.DeepEqual(val, data))
	//data2, _ := cacheManager.Get(key)
	//t.Log("get val ", data2)

	//con, _ := db.GetConn().PrepareContext(context.Background(), "INSERT INTO ... ON DUPLICATE KEY UPDATE")
	//rs, err := con.Exec("1", "a1", 1, "1", "a1", 2)
	//t.Log("err -> ", err)
	//o, _ := rs.RowsAffected()
	//t.Log("ok -> ", o)
	//defer con.Close()
	//w := &sync.WaitGroup{}
	//w.Add(1)
	//w.Wait()
}

func BenchmarkRef(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%v,%v", 1, 2)
	}
}

func BenchmarkComm(b *testing.B) {
	for i := 0; i < b.N; i++ {
		v := CacheExampleData{Name: "a"}
		StructToString(v)
	}
}

func StructToString(s interface{}) string {
	v := reflect.ValueOf(s)
	t := v.Type()
	var fields []string
	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		// 如果字段是零值则跳过
		if field.IsZero() {
			continue
		}
		// 将字段值转换为字符串
		fieldValue := field.Interface()
		str := fmt.Sprintf("%v", fieldValue)
		// 添加到 fields 列表中
		fields = append(fields, str)
	}
	// 使用逗号连接所有字段的字符串值
	return strings.Join(fields, ",")
}

type CacheExampleData struct {
	db.Model
	Uid  string `orm:"pk"`
	Name string
}

func Test_convertCamelToSnake(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "UserId", args: args{"UserId"}, want: "user_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := db.ConvertCamelToSnake(tt.args.s); got != tt.want {
				t.Errorf("convertCamelToSnake() = %v, want %v", got, tt.want)
			}
		})
	}
}

type Item struct {
	db.Model
	UserId string `orm:"pk;pkList"`
	PropId string `orm:"pk"`
	Amount uint64
}

func (i *Item) GetTableName() string {
	return "game_prop"
}

func (i *Item) HashCachePkKey(key ...string) string {
	return key[0]
}

func (i *Item) HashCacheFieldByVal() string {
	return i.PropId
}

func (i *Item) HashCacheFieldByKeys(key ...string) string {
	return key[1]
}

func NewTestHashCacheManager() db.IHashCacheService[*Item] {
	db.Run()
	builder := db.NewHashAutoCacheBuilder[*Item]()
	return builder.WithLongevityCache(time.Second * 5).
		WithMemCache(5).
		New()
}

func Test_hashAutoCacheManager_Get(t *testing.T) {
	type args struct {
		key []string
	}
	type testCase[Val db.IHashModel] struct {
		name    string
		h       db.IHashCacheService[Val]
		args    args
		wantVal db.IHashModel
		wantErr bool
	}
	tests := []testCase[*Item]{
		{name: "example1", h: NewTestHashCacheManager(),
			args:    args{key: []string{"123", "2"}},
			wantVal: &Item{UserId: "123", PropId: "2", Amount: 1},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVal, err := tt.h.Get(tt.args.key...)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotVal, tt.wantVal) {
				t.Errorf("Get() gotVal = %v, want %v", gotVal, tt.wantVal)
			}
		})
	}
}

func Test_hashAutoCacheManager_GetAll(t *testing.T) {

	type args struct {
		key []string
	}
	type testCase[Val db.IHashModel] struct {
		name    string
		h       db.IHashCacheService[Val]
		args    args
		wantVal []db.IHashModel
		wantErr bool
	}
	tests := []testCase[*Item]{
		{name: "example1", h: NewTestHashCacheManager(), args: args{key: []string{"123"}}, wantVal: []db.IHashModel{&Item{
			UserId: "123",
			PropId: "1",
			Amount: 1,
		}, &Item{
			UserId: "123",
			PropId: "2",
			Amount: 1,
		}},
			wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVal, err := tt.h.GetAll(tt.args.key...)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAll() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotVal, tt.wantVal) {
				t.Errorf("GetAll() gotVal = %v, want %v", gotVal, tt.wantVal)
			}
		})
	}
}

func Test_hashAutoCacheManager_Push(t *testing.T) {
	type args struct {
		key []string
	}
	type testCase[Val db.IHashModel] struct {
		name string
		h    db.IHashCacheService[Val]
		args args
	}
	tests := []testCase[*Item]{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.h.Push(tt.args.key...)
		})
	}
}

func Test_hashAutoCacheManager_Remove(t *testing.T) {
	type args struct {
		key []string
	}
	type testCase[Val db.IHashModel] struct {
		name        string
		h           db.IHashCacheService[Val]
		args        args
		wantSuccess bool
	}
	tests := []testCase[*Item]{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotSuccess := tt.h.Remove(tt.args.key...); gotSuccess != tt.wantSuccess {
				t.Errorf("Remove() = %v, want %v", gotSuccess, tt.wantSuccess)
			}
		})
	}
}

func Test_hashAutoCacheManager_Set(t *testing.T) {
	type args[Val db.IHashModel] struct {
		val Val
		key []string
	}
	type testCase[Val db.IHashModel] struct {
		name        string
		h           db.IHashCacheService[Val]
		args        args[Val]
		wantSuccess bool
	}
	tests := []testCase[*Item]{
		{name: "add item", h: NewTestHashCacheManager(), args: args[*Item]{val: &Item{
			UserId: "123",
			PropId: "2",
			Amount: 1,
		}, key: []string{"123", "2"}}, wantSuccess: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotSuccess := tt.h.Set(tt.args.val, tt.args.key...); gotSuccess != tt.wantSuccess {
				t.Errorf("Set() = %v, want %v", gotSuccess, tt.wantSuccess)
			}
		})
	}
	select {}
}
