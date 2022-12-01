package trpcservice

import (
	"fmt"
	client2 "github.com/smallnest/rpcx/client"
	"golang.org/x/net/context"
	"reflect"
	"runtime"
	"strings"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/plugin"
	tserver "tframework.com/rpc/tcore/internal/server"
)

//***************************************************
//author tim.huang
//2022/11/9
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

// ***********************    struct    ****************************

type TRPCService struct {
	funcMapping  map[string][]client2.XClient
	moduleMapper map[string]config.APIConfig
}

//***********************    struct_end    ****************************

func (this *TRPCService) Send(f interface{}, rpcType int32, args interface{}, reply interface{}) {
	va := reflect.ValueOf(f)
	ty := reflect.TypeOf(f)
	//inf := ty.Elem()
	//va := reflect.ValueOf(it)
	//tserver.RegisterRPCService(it, "demo", "0.0.1")
	fc := runtime.FuncForPC(va.Pointer()).Name()
	ix := strings.LastIndex(fc, ".")
	fc = fc[ix+1:]
	msg := fmt.Sprintf("%v-------%v", ty, fc)
	plugin.InfoS("%v", msg)
	for _, d := range this.funcMapping[fc] {
		d.Call(context.Background(), fc, args, reply)
	}
}

func (this *TRPCService) RegisterRPCService(f interface{}, moduleName, version string) {
	tserver.ConsulDiscovery.RegisterClient(f, moduleName, version, this.funcMapping)
}

func (this *TRPCService) InitStruct(apiConfigs []*config.APIConfig) {
	this.funcMapping = make(map[string][]client2.XClient)
	this.moduleMapper = make(map[string]config.APIConfig)
	for _, config := range apiConfigs {
		this.moduleMapper[config.ModuleName] = *config
	}
}

func NewRPCService(apiConfigs []*config.APIConfig) tframework.IRPCService {
	source := new(TRPCService)
	source.InitStruct(apiConfigs)
	return source
}

func init() {
}
