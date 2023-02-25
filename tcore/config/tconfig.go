package config

import (
	"fmt"
	"github.com/go-redis/redis/v8"
	"time"
)

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

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type TConfig struct {
	Server *ServerConfig //服务器相关配置
}

type ServerConfig struct {
	Modules   []*ModuleConfig  //模块配置
	Discovery *DiscoveryConfig //服务发现配置
	API       []*APIConfig     //客户端服务
	TCP       *TCPServerConfig //TCP服务
	Redis     *RedisConfig     //Redis服务
	Gateway   bool             //是否网关
}

type ModuleConfig struct {
	ModuleName    string //模块名称
	ModuleVersion string //模块版本
	Address       string //服务器监听地址
	Port          int    //服务器监听端口
}

type DiscoveryConfig struct {
	Consul     []*ConsulConfig
	ConsulPath string //consul环境路径
}

type APIConfig struct {
	ModuleName    string //模块名称
	ModuleVersion string //模块版本
}

type ConsulConfig struct {
	Address string //consul地址
	Port    int    //端口
}

type RedisConfig struct {
	Address  string //consul地址
	Port     int    //端口
	Password string //密码
	DB       int    //db
}

type TCPServerConfig struct {
	Address      string //地址
	Port         int    //端口
	DeadLineTime time.Duration
}

// ***********************    struct_end    ****************************
func (this *TConfig) GetAPIServices() []*APIConfig {
	if this.Server == nil {
		return nil
	}
	return this.Server.API
}

func (this *TConfig) GetModules() []*ModuleConfig {
	return this.Server.Modules
}

func (this *TConfig) GetDiscovery() *DiscoveryConfig {
	return this.Server.Discovery
}
func (this *TConfig) GetTCPServer() *TCPServerConfig {
	return this.Server.TCP
}
func (this *ConsulConfig) getFullAddress() (_address string) {
	_address = fmt.Sprintf("%v:%v", this.Address, this.Port)
	return
}

func (this *TConfig) GetConsulPath() (_path string) {
	_path = this.Server.Discovery.ConsulPath
	return
}

func (this *TConfig) GetConsulAddressSlice() (_address []string) {
	discovery := this.GetDiscovery()
	_address = make([]string, len(discovery.Consul))
	for i, consul := range discovery.Consul {
		_address[i] = consul.getFullAddress()
	}
	return
}

func (this *TConfig) IsGateway() bool {
	return this.Server.Gateway
}

func (this *TConfig) GetRedisOptions() *redis.Options {
	return &redis.Options{
		//Network:            "",
		Addr: fmt.Sprintf("%v:%v", this.Server.Redis.Address, this.Server.Redis.Port),
		//Dialer:             nil,
		//OnConnect:          nil,
		//Username:           "",
		Password: this.Server.Redis.Password,
		DB:       this.Server.Redis.DB,
		//MaxRetries:         0,
		//MinRetryBackoff:    0,
		//MaxRetryBackoff:    0,
		//DialTimeout:        0,
		//ReadTimeout:        0,
		//WriteTimeout:       0,
		//PoolFIFO:           false,
		//PoolSize:           0,
		//MinIdleConns:       0,
		//MaxConnAge:         0,
		//PoolTimeout:        0,
		//IdleTimeout:        0,
		//IdleCheckFrequency: 0,
		//TLSConfig:          nil,
		//Limiter:            nil,
	}
}

func init() {

}
