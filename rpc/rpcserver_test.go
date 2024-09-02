package rpc_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	client2 "github.com/thkhxm/rpcx-consul/client"
	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	"golang.org/x/net/context"
	"net"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

func TestStartRpcServer(t *testing.T) {
	rpcServer := rpc.NewRPCServer()
	service := new(DemoService)

	service2 := new(Demo2Service)
	rpcServer.
		WithService(service).
		WithService(service2).
		WithGateway("8038").
		Run()

	w := sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

func TestWssServer(t *testing.T) {
	rpcServer := rpc.NewRPCServer()
	service := new(DemoService)

	rpcServer.
		WithService(service).
		WithGatewayWSS("8038", "/wss", "cert.pem", "key.pem").
		Run()

	w := sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

func TestTcpClientSender(t *testing.T) {

	// [1][1][2][2][n][n]
	// magic number|message type|request method name size|data size|method name|data
	//for i := 0; i < 10; i++ {
	//	go func() {
	add, err := net.ResolveTCPAddr("tcp", "127.0.0.1:8038")
	client, err := net.DialTCP("tcp", nil, add)
	if err != nil {
		t.Logf("client error: %v", err)
		return
	}
	//for i := 0; i < 100; i++ {
	//var msg = "say hello - " + strconv.Itoa(i)
	//Login
	loginBuff := LoginByteTest()
	cnt, er := client.Write(loginBuff.Bytes())
	t.Logf("send login message : %v", loginBuff.Bytes())
	//Logic
	buff := LogicByteTest()
	cnt, er = client.Write(buff.Bytes())
	if er != nil {
		t.Logf("write len %v error : %v", cnt, er)
	}
	t.Logf("send logic message : %v", buff.Bytes())

	//for {
	//	resBytes := make([]byte, 1024)
	//	client.Read(resBytes)
	//	t.Logf("response message : %v", resBytes)
	//}

	//	}
	//}()
	//time.Sleep(time.Second * 3)
	//buf := make([]byte, 1024)
	//rcnt, er2 := client.Read(buf)
	//if er2 != nil {
	//	t.Logf("write len %v error : %v", rcnt, er)
	//}
	//t.Logf("callback message : %v", string(buf))
	//}
	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}

func LoginByteTest() *bytes.Buffer {
	var token = "token-test2022"
	data := []byte(token)
	tmp := make([]byte, 0, 4+len(data))
	buff := bytes.NewBuffer(tmp)
	buff.WriteByte(250)
	reqSizeLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqSizeLenByte, uint16(len(data)))
	buff.Write(reqSizeLenByte)
	buff.Write(data)
	return buff
}

func LogicByteTest() *bytes.Buffer {
	service := new(Demo2Service)
	var msg = "say hello - "
	data := []byte(msg)
	reqName := []byte(fmt.Sprintf("%v.%v", service.GetName(), "RPCSayHello"))

	tmp := make([]byte, 0, 6+len(data)+len(reqName))
	buff := bytes.NewBuffer(tmp)
	buff.WriteByte(250)
	buff.WriteByte(byte(rpc.Logic))
	reqLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqLenByte, uint16(len(reqName)))
	buff.Write(reqLenByte)
	reqSizeLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqSizeLenByte, uint16(len(data)))
	buff.Write(reqSizeLenByte)
	buff.Write(reqName)
	buff.Write(data)
	return buff
}

func TestClientSender(t *testing.T) {
	service := new(Demo2Service)
	serviceName := fmt.Sprintf("%v", service.GetName())
	d, _ := client2.NewConsulDiscovery(tgf.GetStrConfig[string](tgf.EnvironmentConsulPath), serviceName, tgf.GetStrListConfig(tgf.EnvironmentConsulAddress), nil)
	xclient := client.NewXClient(serviceName, client.Failtry, client.RandomSelect, d, client.DefaultOption)
	defer xclient.Close()
	xclient.Call(context.Background(), "RPCSayHello", nil, nil)
	w := sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

type Demo2Service struct {
	rpc.Module
}

func (this *Demo2Service) GetName() string {
	return "example"
}

func (this *Demo2Service) GetVersion() string {
	return "v1.0"
}

func (this *Demo2Service) Startup() (bool, error) {
	var ()
	return true, nil
}

func (this *Demo2Service) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) error {
	var ()
	log.Info("[test] rpcx2请求抵达 ")
	return nil
}

type DemoService struct {
	rpc.Module
}

func (this *DemoService) GetName() string {
	return "demo"
}

func (this *DemoService) GetVersion() string {
	return "v1.0"
}

func (this *DemoService) Startup() (bool, error) {
	var ()
	return true, nil
}
func (this *DemoService) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) error {
	var ()
	log.Info("[test] rpcx请求抵达 ")
	return nil
}
