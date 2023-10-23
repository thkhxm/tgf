package db_test

import (
	"fmt"
	"github.com/thkhxm/tgf/db"
	"golang.org/x/net/context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/16
//***************************************************

func Test_autoCacheManager_Get(t *testing.T) {
	//cacheManager := db.NewDefaultAutoCacheManager[string, *CacheExampleData]("example:cachemanager")
	//key := "123"
	//val := &CacheExampleData{Name: "tim"}
	////cacheManager.Set(key, val)
	//data, _ := cacheManager.Get(key)
	////data.Name = "sam"
	//t.Log("get val ", data, reflect.DeepEqual(val, data))
	////data2, _ := cacheManager.Get(key)
	////t.Log("get val ", data2)

	//db.NewDefaultAutoCacheManager[string, int32]("example:cachemanager")
	//INSERT INTO table_name (id, name, value) VALUES (1, 'John', 10), (2, 'Peter', 20), (3, 'Mary', 30)
	//ON DUPLICATE KEY UPDATE name=VALUES(name), value=VALUES(value);

	con, _ := db.GetConn().PrepareContext(context.Background(), "INSERT INTO ... ON DUPLICATE KEY UPDATE")
	rs, err := con.Exec("1", "a1", 1, "1", "a1", 2)
	t.Log("err -> ", err)
	o, _ := rs.RowsAffected()
	t.Log("ok -> ", o)
	defer con.Close()
	w := &sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

func BenchmarkRef(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%v,%v", 1, 2)
	}
}

func BenchmarkComm(b *testing.B) {
	for i := 0; i < b.N; i++ {
		v := CacheExampleData{Age: int32(i), Name: "a"}
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
	Name string `orm:"pk"`
	Age  int32
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
