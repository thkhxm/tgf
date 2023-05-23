package util_test

import (
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/util"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/27
//***************************************************

func TestGeneratorAPI(x *testing.T) {
	util.SetAutoGenerateAPICodePath("./api")
	util.GeneratorAPI[rpc.IExampleService]("Hall", "1.0", "api")
	//var t IExampleService
	//v := reflect.ValueOf(&t)
	//ty := v.Type().Elem()
	//for i := 0; i < ty.NumMethod(); i++ {
	//	m := ty.Method(i)
	//	fmt.Sprintf("method %v,%v,%v", m.Name, m.Type, m.PkgPath)
	//}
}
