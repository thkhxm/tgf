package util_test

import (
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
	SetExcelPath("C:\\Users\\AUSA\\Desktop\\配置文件")
	SetExcelToJsonPath("./")
	ExcelExport()
}
