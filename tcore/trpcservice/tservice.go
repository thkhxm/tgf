package trpcservice

import (
	"sync"
	tframework "tframework.com/rpc/tcore/interface"
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
	funcMapping *sync.Map
}

//***********************    struct_end    ****************************

func (this *TRPCService) Send(f interface{}, rpcType int32, args *interface{}, reply *interface{}) {

}

func (this *TRPCService) RegisterRPCService(f interface{}, moduleName, version string) {
	_, funcSlice := tserver.ConsulDiscovery.RegisterClient(f, moduleName, version)
	key := moduleName + "@" + version
	this.funcMapping.Store(key, funcSlice)
}

func (this *TRPCService) InitStruct() {
	this.funcMapping = new(sync.Map)
}

func NewRPCService() tframework.IRPCService {
	source := new(TRPCService)
	source.InitStruct()
	return source
}

func init() {
}
