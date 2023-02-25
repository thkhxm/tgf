package _interface

import "github.com/go-redis/redis/v8"

//***************************************************
//author tim.huang
//2022/11/4
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

// ***********************    interface    ****************************
type IServerConfigService interface {
	GetConsulPath() (_path string)
	GetConsulAddressSlice() (_address []string)
	IsGateway() bool
	GetRedisOptions() *redis.Options
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func init() {
}
