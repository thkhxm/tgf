package trpcservice

import (
	client2 "github.com/smallnest/rpcx/client"
	"golang.org/x/net/context"
	"reflect"
	"runtime"
	"strings"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	tserver "tframework.com/rpc/tcore/internal/server"
	"time"
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

type RPCCallback struct {
	call  *client2.Call
	start int64
	end   int64
}

//***********************    struct_end    ****************************

func (this *RPCCallback) Done() (reply interface{}) {
	if this.call == nil {
		return nil
	}
	call := <-this.call.Done
	this.end = time.Now().UnixMilli()
	return call.Reply
}

func (this *RPCCallback) Time() (mill int64) {
	return this.end - this.start
}

func (this *TRPCService) SendOne(f interface{}, rpcType int32, args interface{}, reply interface{}) (tframework.IRPCCallBack, error) {
	var (
		err error
	)
	va := reflect.ValueOf(f)
	fc := runtime.FuncForPC(va.Pointer()).Name()
	ix := strings.LastIndex(fc, ".")
	fc = fc[ix+1:]
	callback := &RPCCallback{
		start: time.Now().UnixMilli(),
	}
	for _, d := range this.funcMapping[fc] {
		callback.call, err = d.Go(context.Background(), fc, args, reply, nil)
		//d.Call(context.Background(), fc, args, reply)
		if err != nil {
			continue
		}
		break
	}
	return callback, nil
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
