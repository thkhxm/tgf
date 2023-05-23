package util_test

import (
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf/util"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/5/17
//***************************************************

func TestGenerateSnowflakeId(t *testing.T) {
	wg := &sync.WaitGroup{}
	m := hashmap.New[string, struct{}]()
	size := 10000
	wg.Add(size)
	for i := 0; i < size; i++ {
		go func(i int, wg *sync.WaitGroup) {
			defer wg.Done()
			id := util.GenerateSnowflakeId()
			m.Set(id, struct{}{})
		}(i, wg)
	}
	wg.Wait()
	fmt.Println(m.Len())
}
