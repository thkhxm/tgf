package util_test

import (
	"testing"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description
// 2023/4/27
// ***************************************************
//type IExampleService interface {
//	Login(ctx context.Context, args *rpc.Args[*hallpb.HallSayRequest], reply *rpc.Reply[*hallpb.HallSayRequest]) (err error)
//	Login2(ctx context.Context, args *rpc.Args[*hallpb.HallSayRequest], reply *rpc.Reply[*hallpb.HallSayRequest]) (err error)
//}

func TestGeneratorAPI(x *testing.T) {
	////generate client api
	//util.SetAutoGenerateAPICodePath("./generated/user_api")
	//util.GeneratorAPI[user.IUserService](internal.ModuleName, internal.Version, "user_api")
	////generate rpc api
	//util.SetAutoGenerateAPICodePath("./")
	//util.SetGenerateFileNameSuffix("rpc")
	//util.GeneratorRPC[user.IUserRPCService](internal.ModuleName, internal.Version, internal.ModuleName, "")
	////generate cs api
	//util.SetAutoGenerateAPICSCode("E:\\unity\\project\\t2\\Assets\\HotFix\\Code", "HotFix.Code")
	//util.GenerateCSApiService()

}
