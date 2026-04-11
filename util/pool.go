package util

import (
	"fmt"
	"os"
	"time"

	"github.com/panjf2000/ants/v2"
)

// 说明：util 包不能 import tgf/log（循环依赖：tgf → util → log → tgf）。
// fallback 路径低频而且是错误现场，直接写 os.Stderr 足够——既不会沉默也不引入
// 新的依赖。未来 B4 接入 metrics 包后可以把这里换成 counter 上报。

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

var goroutinePool *ants.Pool

// 默认通用协程池大小
var defaultPoolSize = 2e5

// Go
//
//	@Description: 无序投递任务到 ants 协程池。
//
// A5 修复：原实现忽略 Submit 的返回值——池满（ErrPoolOverload）或池已关闭时
// 任务会被静默丢弃。现在改为池满时 fallback 到裸 goroutine（带 recover 保护），
// 并打一条 WARN 日志，便于运维监控。
//
// 快路径：99.9% 情况下走 ants.Submit 成功。fallback 只在容量用尽的应急场景触发。
// 如果调用方希望精确感知入队结果，请改用 GoE。
func Go(f func()) {
	if f == nil {
		return
	}
	if goroutinePool == nil {
		goSafe(f)
		return
	}
	if err := goroutinePool.Submit(f); err != nil {
		fmt.Fprintf(os.Stderr, "[tgf/util] ants pool submit failed, fallback to bare goroutine: %v\n", err)
		goSafe(f)
	}
}

// GoE 是 Go 的精确感知版本：返回 Submit 的 error，调用方可以自己决定是重试还是 drop。
// 用于那些"任务丢失后果严重"的场景，例如 A1 的补偿队列 replay。
func GoE(f func()) error {
	if f == nil {
		return nil
	}
	if goroutinePool == nil {
		return ants.ErrPoolClosed
	}
	return goroutinePool.Submit(f)
}

// goSafe 起一个裸 goroutine 跑 f，外层 recover 避免 panic 击穿整个进程。
// 仅供 Go 的 fallback 路径使用；正常业务代码应该走 Go/GoE。
func goSafe(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "[tgf/util] bare goroutine panic recovered: %v\n", r)
			}
		}()
		f()
	}()
}

func InitGoroutinePool() {
	goroutinePool, _ = ants.NewPool(int(defaultPoolSize), ants.WithExpiryDuration(time.Minute*3))
}
