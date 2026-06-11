package util_test

import (
	"github.com/thkhxm/tgf/v2/util"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

// TestGo 是一份 pre-existing 的空壳用例：它用 WaitGroup 永久 Wait 把测试进程
// 挂住，显然是用来手动观察 ants 日志的，不适合自动化 CI。B2 阶段启用 CI 时
// 直接 Skip；`util/pool_fallback_test.go` 里有正经的 Go/GoE 覆盖率测试。
func TestGo(t *testing.T) {
	t.Skip("pre-existing manual observation test, replaced by pool_fallback_test.go")
	_ = sync.WaitGroup{}
	_ = util.Go
}

func TestInitGoroutinePool(t *testing.T) {
	tests := []struct {
		name string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			util.InitGoroutinePool()
		})
	}
}
