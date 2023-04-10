package util_test

import (
	"github.com/thkhxm/tgf/util"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/10
//***************************************************

func TestExcelToJson(t *testing.T) {
	util.SetExcelPath("C:\\Users\\AUSA\\Desktop\\配置文件")
	util.SetExcelToJsonPath("./")
	util.ExcelExport()
}
