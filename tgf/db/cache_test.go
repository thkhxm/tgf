package db_test

import (
	"github.com/thkhxm/tgf/db"
	"testing"
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

	cacheManager.Set(key, setVal)
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
