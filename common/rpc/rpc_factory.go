package rpc

import (
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/utils"
	"tframework.com/server/common"
)

//***************************************************
//author tim.huang
//2022/11/29
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

type IRPCFactory interface {
	StoreRPCServiceImp(moduleName common.ModuleName, service interface{})

	InitFactory()
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

var RPCFactory *Factory

// Factory
// @Description:
type Factory struct {
	cacheManager *FactoryDataManager
}

type FactoryDataManager struct {
	utils.BaseDataManager
}

//***********************    struct_end    ****************************

func (this *Factory) InitStruct() {
	this.cacheManager = new(FactoryDataManager)
	this.cacheManager.InitStruct(this.cacheManager)
}

func (factory *Factory) StoreRPCServiceImp(moduleName common.ModuleName, service interface{}) {
	factory.cacheManager.Store(string(moduleName), service)
}

func (factory *Factory) InitFactory() {
	for _, config := range tcore.Config.GetAPIServices() {
		if data, ok := factory.cacheManager.Get(config.ModuleName, false); ok {
			tcore.RPCService.RegisterRPCService(data, config.ModuleName, config.ModuleVersion)
		}
	}
}

func (manager *FactoryDataManager) GetRedisKey(in interface{}) string {
	return ""
}

func (manager *FactoryDataManager) GetDBName() string {
	return "FactoryDataManager"
}

func (manager *FactoryDataManager) InstanceEmptyData() interface{} {
	return nil
}
