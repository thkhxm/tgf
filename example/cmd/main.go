package main

import (
	"github.com/thkhxm/tgf/component"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/service/conf"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/10
//***************************************************

func main() {
	//util.SetExcelPath("./cmd/excel")
	//util.SetExcelToJsonPath("./cmd/json")
	//util.SetExcelToGoPath("./conf")
	//util.ExcelExport()
	component.WithConfPath("./cmd/json")
	component.InitGameConfToMem()
	component.LoadGameConf[*conf.HeroConf]()
	heroConf := component.GetGameConf[*conf.HeroConf]("f_01")
	log.Debug("--->%v", heroConf.Attack)
}
