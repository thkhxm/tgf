package rpc

import (
	"testing"

	"context"

	"github.com/thkhxm/rpcx/v2/share"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description
// 2024/2/23
// ***************************************************
type rpcTestData struct {
	DD string
}

func TestServiceAPI_NewRPC(t *testing.T) {
	api := (&ServiceAPI[*LoginReq, *rpcTestData]{}).NewRPC(&LoginReq{})
	if api.reply == nil {
		t.Fatal("NewRPC 应为指针响应分配可写实例")
	}
}

func TestServiceAPI_NewRPC_NonPointerResponseKeepsZeroValue(t *testing.T) {
	api := (&ServiceAPI[*LoginReq, rpcTestData]{}).NewRPC(&LoginReq{})
	if api.reply != (rpcTestData{}) {
		t.Fatalf("非指针响应应保持零值语义, got %+v", api.reply)
	}
}

type NewRPCRemoteReq struct {
	Value string
}

type NewRPCRemoteRes struct {
	Value string
}

type newRPCRemoteService struct{}

func (*newRPCRemoteService) Echo(_ context.Context, req *NewRPCRemoteReq, reply *NewRPCRemoteRes) error {
	reply.Value = "remote:" + req.Value
	return nil
}

func TestServiceAPI_NewRPC_RemoteRPCXPointerResponse(t *testing.T) {
	ResetLocalDispatcherForTest()
	t.Cleanup(ResetLocalDispatcherForTest)
	ClearMethodPolicies()
	t.Cleanup(ClearMethodPolicies)

	const moduleName = "newrpc-pointer-response"
	addr, shutdown := startTestRPCXServer(t, moduleName, &newRPCRemoteService{})
	t.Cleanup(shutdown)
	xc := newP2PXClient(t, moduleName, addr)
	t.Cleanup(func() { _ = xc.Close() })
	t.Cleanup(injectRPCClient(t, moduleName, xc))

	descriptor := &ServiceAPI[*NewRPCRemoteReq, *NewRPCRemoteRes]{
		ModuleName: moduleName,
		Name:       "Echo",
	}
	api := descriptor.NewRPC(&NewRPCRemoteReq{Value: "wire"})
	if api.reply == nil {
		t.Fatal("NewRPC 传给 rpcx 前的 reply 不应是 typed nil")
	}

	res, err := SendRPCMessage(share.NewContext(context.Background()), api)
	if err != nil {
		t.Fatalf("真实 rpcx 调用失败: %v", err)
	}
	if res == nil || res.Value != "remote:wire" {
		t.Fatalf("真实 rpcx 指针回包未正确解码: %+v", res)
	}
}
