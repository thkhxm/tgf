package util_test

import (
	"context"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/util"
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

type IGenerateCodeRPCTest interface {
	RPCTest(ctx context.Context, args *rpc.EmptyReply, reply *rpc.EmptyReply) (err error)
}

func TestGeneratorAPI(x *testing.T) {
	////generate client api
	//util.GeneratorAPI[chat_module.IChatService](internal.ModuleName, internal.Version,
	//	"ChatPush")
	////generate rpc api
	util.GeneratorRPC[IGenerateCodeRPCTest]("code_test", "1.0.0")
	////generate cs api
	//util.SetAutoGenerateAPICSCode("E:\\unity\\project\\t2\\Assets\\HotFix\\Code", "HotFix.Code")
	//util.GenerateCSApiService()
}
