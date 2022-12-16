package chat

import (
	"golang.org/x/net/context"
	"math/rand"
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/server/common"
	"tframework.com/server/common/rpc"
	"time"
)

//***************************************************
//author tim.huang
//2022/8/11
//
//
//***************************************************

// Module
// @Description: 聊天模块
type Module struct {
	tcore.BaseModule
}

func (c *Module) GetModuleName() (moduleName string) {
	return string(common.Chat)
}

func (c *Module) RPCSayHello(ctx context.Context, args *rpc.RPCSayHelloRequest, reply *rpc.RPCSayHelloResponse) error {
	tcore.Log.Debug("chat rpc chat test %v", c.GetFullAddress())
	reply.Message = "ok"
	reply.Data = new(rpc.RPCResponseData)
	reply.Data.Friends = []int32{int32(rand.Intn(10)), int32(rand.Intn(10)), int32(rand.Intn(10))}
	time.Sleep(time.Second * 5)
	return nil
}

func (this *Module) StartFightService(ctx context.Context, args *[]byte, reply *[]byte) error {
	tcore.Log.Debug("StartFightService test %v", args)
	dd := []byte("reply data")
	*reply = dd
	//reflect.Indirect(reflect.ValueOf(reply)).SetBytes(dd)
	//reply = &dd
	return nil
}

func (c *Module) StartUp() {

}

func Create(config *config.ModuleConfig) tframework.ITModule {
	m := &Module{}
	m.AddPlugin(tframework.Log)
	m.AddPlugin(tframework.Consul)
	m.InitStruct(config)
	return m
}
