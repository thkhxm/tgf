package db_test

import (
	"github.com/thkhxm/tgf/db"
	"reflect"
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
	cacheManager := db.NewAutoCacheManager[string, string]()
	key := "123"
	val := "321"
	cacheManager.Set(key, val)
	data, _ := cacheManager.Get(key)
	t.Log("get val ", data, reflect.DeepEqual(val, data))

}
