package rpc

// D5 回归测试：SendRPCMessage 在 xclient.Go 返回 (nil, err) 时（selector 选不到
// 节点 / client 已 shutdown——滚动发布常态场景）必须返回干净 error 而非 nil 指针
// panic。v3 审计：原实现把解引用 call.Error 的 defer 注册在 err 检查之前，
// call==nil 时 return 触发 defer 即 panic（rpcserver.go:806-816）。

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/cornelk/hashmap"
	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/rpcx/v2/protocol"
)

// nilCallXClient 是 client.XClient 的最小桩：Go 返回 (nil, goErr)，
// 模拟 rpcx 的 ErrXClientNoServer 路径。
type nilCallXClient struct {
	goErr error
}

func (n *nilCallXClient) SetPlugins(plugins client.PluginContainer)     {}
func (n *nilCallXClient) GetPlugins() client.PluginContainer            { return nil }
func (n *nilCallXClient) SetSelector(s client.Selector)                 {}
func (n *nilCallXClient) ConfigGeoSelector(latitude, longitude float64) {}
func (n *nilCallXClient) Auth(auth string)                              {}

func (n *nilCallXClient) Go(ctx context.Context, serviceMethod string, args interface{}, reply interface{}, done chan *client.Call) (*client.Call, error) {
	return nil, n.goErr
}

func (n *nilCallXClient) Call(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) error {
	return n.goErr
}

func (n *nilCallXClient) Oneshot(ctx context.Context, serviceMethod string, args interface{}) error {
	return n.goErr
}

func (n *nilCallXClient) Broadcast(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) error {
	return n.goErr
}

func (n *nilCallXClient) Fork(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) error {
	return n.goErr
}

func (n *nilCallXClient) Inform(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) ([]client.Receipt, error) {
	return nil, n.goErr
}

func (n *nilCallXClient) SendRaw(ctx context.Context, r *protocol.Message) (map[string]string, []byte, error) {
	return nil, nil, n.goErr
}

func (n *nilCallXClient) SendFile(ctx context.Context, fileName string, rateInBytesPerSecond int64, meta map[string]string) error {
	return n.goErr
}

func (n *nilCallXClient) DownloadFile(ctx context.Context, requestFileName string, saveTo io.Writer, meta map[string]string) error {
	return n.goErr
}

func (n *nilCallXClient) Stream(ctx context.Context, meta map[string]string) (net.Conn, error) {
	return nil, n.goErr
}

func (n *nilCallXClient) Close() error { return nil }

// withStubRPCClient 把全局 rpcClient 临时替换为只含指定模块桩的实例，返回恢复函数。
func withStubRPCClient(t *testing.T, moduleName string, xc client.XClient) func() {
	t.Helper()
	orig := rpcClient
	stub := &Client{
		clients:     hashmap.New[string, client.XClient](),
		whiteMethod: make([]string, 0),
	}
	stub.clients.Set(moduleName, xc)
	rpcClient = stub
	return func() { rpcClient = orig }
}

// TestSendRPCMessage_NoAvailableNode_NoPanic 验证 selector 选不到节点时
// SendRPCMessage 返回错误而非 panic（D5 / 滚动发布常态场景）。
func TestSendRPCMessage_NoAvailableNode_NoPanic(t *testing.T) {
	ResetLocalDispatcherForTest()
	noServer := errors.New("xclient: no available server")
	defer withStubRPCClient(t, "ghostmod", &nilCallXClient{goErr: noServer})()

	api := &ServiceAPI[*DefaultArgs, *DefaultReply]{
		ModuleName: "ghostmod",
		Name:       "Foo",
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendRPCMessage panicked (P1 regression): %v", r)
		}
	}()
	_, err := SendRPCMessage(context.Background(), api.New(&DefaultArgs{C: "x"}, &DefaultReply{}))
	if err == nil {
		t.Fatalf("expected error when no node available, got nil")
	}
	if !strings.Contains(err.Error(), "no available server") {
		t.Errorf("err should wrap underlying cause, got: %v", err)
	}
}

// TestSendRPCMessage_NilCallNilErr_Defensive 验证防御分支：即使 Go 异常地返回
// (nil, nil)，也返回"无可用服务节点"错误而不是 panic。
func TestSendRPCMessage_NilCallNilErr_Defensive(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer withStubRPCClient(t, "ghostmod", &nilCallXClient{goErr: nil})()

	api := &ServiceAPI[*DefaultArgs, *DefaultReply]{
		ModuleName: "ghostmod",
		Name:       "Foo",
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendRPCMessage panicked: %v", r)
		}
	}()
	_, err := SendRPCMessage(context.Background(), api.New(&DefaultArgs{C: "x"}, &DefaultReply{}))
	if err == nil || !strings.Contains(err.Error(), "无可用服务节点") {
		t.Fatalf("err = %v, want 无可用服务节点", err)
	}
}
