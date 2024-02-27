package admin

import (
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/26
//***************************************************

func Test_getMonitor(t *testing.T) {
	a := getMonitor("a", "b")
	t.Log(a)
}
