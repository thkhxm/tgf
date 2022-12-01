package test

import (
	"fmt"
	"github.com/rpcxio/rpcx-consul/client"
	client2 "github.com/smallnest/rpcx/client"
	"golang.org/x/net/context"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"tframework.com/rpc/tcore"
)

type TestServer struct {
	tcore.BaseModule `json:"tcore_._base_module"`
	DD               int `cnf:"123"`
}

func (receiver *TestServer) GetModuleName() (moduleName string) {
	return "test"
}

func (receiver *TestServer) RPCFindBooks() {

}

func (receiver *TestServer) RPcFindApple() {

}

func (receiver *TestServer) SayRed(ctx context.Context, args *interface{}, reply *interface{}) error {
	println("11111111")
	return nil
}

type TestInterface interface {
	SayRed(ctx context.Context, args *interface{}, reply *interface{}) error
}

var m map[interface{}]string

func TestRef(t *testing.T) {
	m = make(map[interface{}]string)
	GetRefObj(TestInterface.SayRed)
}

func GetRefObj(it interface{}) {
	//
	va := reflect.ValueOf(it)
	ty := reflect.TypeOf(it)
	//inf := ty.Elem()
	//va := reflect.ValueOf(it)
	//tserver.RegisterRPCService(it, "demo", "0.0.1")
	fc := runtime.FuncForPC(va.Pointer()).Name()
	ix := strings.LastIndex(fc, ".")
	fc = fc[ix+1:]
	msg := fmt.Sprintf("%v-------%v", ty, fc)
	println(msg)
}

func BenchmarkFuncForPC(b *testing.B) {
	for i := 0; i < b.N; i++ {
		va := reflect.ValueOf(TestInterface.SayRed)
		fc := runtime.FuncForPC(va.Pointer()).Name()
		ix := strings.LastIndex(fc, ".")
		fc = fc[ix+1:]
	}
}

func TestTag(t *testing.T) {
	// #1
	d, _ := client.NewConsulDiscovery("/tframework/Chat", "Chat@1.0.0", []string{"127.0.0.1:8500"}, nil)
	// #2
	xclient := client2.NewXClient("Chat@1.0.0", client2.Failtry, client2.RandomSelect, d, client2.DefaultOption)
	defer xclient.Close()
	//time.Sleep(time.Second * 10)
	for i := 0; i < 3; i++ {
		// #5
		err := xclient.Call(context.Background(), "RPCSayHello", i, nil)

		if err != nil {
			tcore.Log.Debug("failed to call: %v", err)
		}
	}

}
