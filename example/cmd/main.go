package main

import (
	"github.com/thkhxm/tgf/component"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/service/conf"
	"github.com/thkhxm/tgf/util"
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
	//
	component.WithConfPath("./cmd/json")
	component.InitGameConfToMem()
	heroConf := component.GetGameConf[*conf.HeroConf]("f_01")
	log.Debug("--->%v", heroConf.Attack)
	heroList := component.GetAllGameConf[*conf.HeroConf]()
	for _, c := range heroList {
		log.Debug("for --->%v", c.Attack)
	}
	groupList := component.GetGameConfBySlice[*conf.HeroConf]("f_01")
	for _, c := range groupList {
		log.Debug("for group --->%v", c.Attack)
	}

	component.RangeGameConf[*conf.HeroConf](func(s string, i *conf.HeroConf) bool {
		msg, _ := util.AnyToStr(i)
		log.Debug("print hero conf ->%v", msg)
		return true
	})
}
