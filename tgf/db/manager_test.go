package db_test

import (
	"github.com/thkhxm/tgf/db"
	"golang.org/x/net/context"
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

	con, _ := db.GetConn().PrepareContext(context.Background(), "insert into t_user(id,nick_name) values(?,?)")
	rs, _ := con.Exec("5", "a5", "6", "a6")
	o, _ := rs.RowsAffected()
	t.Log("ok -> ", o)
	defer con.Close()
	w := &sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

type CacheExampleData struct {
	Name string `orm:"pk"`
	Age  int32
}
