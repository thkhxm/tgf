package tcore

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
)

//***************************************************
//author tim.huang
//2022/8/22
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

type BaseModule struct {
	plugin    int64
	config    *config.ModuleConfig
	funcSlice []func(rpcType int32, args *interface{}, reply *interface{}) error
}

func (this *BaseModule) Rpc(f interface{}, rpcType tframework.TRPCType, args *interface{}, reply *interface{}) {
	va := reflect.ValueOf(f)
	fc := runtime.FuncForPC(va.Pointer()).Name()
	ix := strings.LastIndex(fc, ".")
	fc = fc[ix+1:]

}

func (b *BaseModule) GetPlugin() int64 {
	return b.plugin
}

func (b *BaseModule) AddPlugin(plugin tframework.TServerPlugin) int64 {
	b.plugin = b.plugin | int64(plugin)
	return b.plugin
}

func (this *BaseModule) InitRPCRequest(funcSlice []func(rpcType int32, args *interface{}, reply *interface{}) error) {
	this.funcSlice = funcSlice
}

func (b *BaseModule) GetModuleName() (moduleName string) {
	moduleName = b.config.ModuleName
	return
}

func (b *BaseModule) GetVersion() (_version string) {
	_version = b.config.ModuleVersion
	return
}
func (b *BaseModule) GetAddress() (_address string) {
	_address = b.config.Address
	return
}
func (b *BaseModule) GetPort() (_port int) {
	_port = b.config.Port
	return
}
func (b *BaseModule) GetFullAddress() (_address string) {
	_address = fmt.Sprintf("%v:%v", b.GetAddress(), b.GetPort())
	return
}

func (b *BaseModule) InitStruct(config *config.ModuleConfig) {
	b.config = config
	return
}

//***********************    struct_end    ****************************

func init() {
}
