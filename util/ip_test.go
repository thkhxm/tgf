package util_test

import (
	"github.com/thkhxm/tgf/v2/util"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/9/3
//***************************************************

func TestGetLocalHost(t *testing.T) {
	t.Logf("%s", util.GetLocalHost())
}

func TestGetLocalHost2(t *testing.T) {
	t.Logf("%s", util.GetLocalHost2())
}
