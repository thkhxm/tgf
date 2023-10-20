package util_test

import (
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/component"
	"github.com/thkhxm/tgf/db"
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

func TestExcelToJsonFull(t *testing.T) {
	//
	db.WithCacheModule(tgf.CacheModuleClose)
	//设置excel路径
	util.SetExcelPath("./excel")
	//设置excel导出的go文件路径
	util.SetExcelToGoPath("../common/conf")
	//设置excel导出的json文件路径
	util.SetExcelToJsonPath("../common/conf/js")
	//开始导出excel
	util.ExcelExport()

	//设置配置源数据路径
	component.WithConfPath("../common/conf/js")
	//初始化游戏配置到内存中
	component.InitGameConfToMem()

	////获取配置数据
	//codes := component.GetAllGameConf[*conf.ErrorCodeConf]()
	////初始化结构化kv数据源
	//data := make([]util.TemplateKeyValueData, len(codes), len(codes))
	//for i, code := range codes {
	//	data[i] = util.TemplateKeyValueData{
	//		FieldName: code.FieldName,
	//		Values:    code.Code,
	//	}
	//}
	////将数据源写入文件 生成kv结构文件
	//util.JsonToKeyValueGoFile("errorcodes", "error_code", "../common/define/errorcode", "int32", data)

}
