package rpc

import (
	"fmt"
	client2 "github.com/rpcxio/rpcx-consul/client"
	"github.com/smallnest/rpcx/client"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"golang.org/x/net/context"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

func TestStartRpcServer(t *testing.T) {
	rpcServer := NewRPCServer()
	service := new(DemoService)

	service2 := new(Demo2Service)
	rpcServer.WithConsulDiscovery().WithService(service).WithService(service2).Run()

	w := sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

func TestClientSender(t *testing.T) {
	service := new(Demo2Service)
	serviceName := fmt.Sprintf("%v", service.GetName())
	d, _ := client2.NewConsulDiscovery(tgf.GetStrConfig[string](tgf.EnvironmentConsulPath), serviceName, tgf.GetStrListConfig[string](tgf.EnvironmentConsulAddress), nil)
	xclient := client.NewXClient(serviceName, client.Failtry, client.RandomSelect, d, client.DefaultOption)
	defer xclient.Close()
	xclient.Call(context.Background(), "RPCSayHello", nil, nil)
	w := sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

type Demo2Service struct {
}

func (this *Demo2Service) GetName() string {
	return "example"
}

func (this *Demo2Service) GetVersion() string {
	return "v1.0"
}

func (this *Demo2Service) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) error {
	var ()
	log.Info("[test] rpcx2请求抵达 ")
	return nil
}

type DemoService struct {
}

func (this *DemoService) GetName() string {
	return "demo"
}

func (this *DemoService) GetVersion() string {
	return "v1.0"
}

func (this *DemoService) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) error {
	var ()
	log.Info("[test] rpcx请求抵达 ")
	return nil
}
