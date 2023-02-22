package util

import (
	"github.com/panjf2000/ants/v2"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

var goroutinePool *ants.Pool
var defaultPoolSize int = 2e5

// Go
// @Description: 无序运行投递的任务,
// @param f
func Go(f func()) {
	goroutinePool.Submit(f)
}

func InitGoroutinePool() {
	goroutinePool, _ = ants.NewPool(defaultPoolSize, ants.WithExpiryDuration(time.Minute*3))
	//a, _ := ants.NewPoolWithFunc(10, func(i interface{}) {
	//
	//}, ants.WithPanicHandler(func(d interface{}) {
	//	log.Warn("[goroutine] ants线程池异常 %v", d)
	//}))
	// ants will pre-malloc the whole capacity of pool when you invoke this function
	//p, _ := ants.NewPool(100000, ants.WithPreAlloc(true))
}
