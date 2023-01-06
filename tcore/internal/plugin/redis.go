package plugin

import (
	"context"
	"encoding/json"
	"github.com/go-redis/redis/v8"
	tframework "tframework.com/rpc/tcore/interface"
	_interface "tframework.com/rpc/tcore/internal/interface"
	"time"
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

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type RedisPlugin struct {
	BasePlugin
	configService _interface.IServerConfigService
	client        *redis.Client
}

func (r *RedisPlugin) InitPlugin() {
	r.client = redis.NewClient(r.configService.GetRedisOptions())
}

func (r *RedisPlugin) StartPlugin() {

}

//***********************    struct_end    ****************************

func (r *RedisPlugin) Get(key string, instance interface{}) error {
	var (
		res string
		err error
	)
	if res, err = r.client.Get(context.Background(), key).Result(); err == nil {
		err = json.Unmarshal([]byte(res), instance)
	}
	return err
}

func (r *RedisPlugin) GetString(key string) string {
	var (
		res string
		err error
	)
	if res, err = r.client.Get(context.Background(), key).Result(); err != nil {
		res = ""
	}
	return res
}

func (r *RedisPlugin) Set(key string, instance interface{}, expires time.Duration) error {
	var (
		res string
		err error
	)
	if res, err = r.client.Set(context.Background(), key, instance, expires).Result(); err != nil {
		ErrorS("redis set error %v , err %v", res, err)
	}
	return err
}

func NewRedisClient(configService _interface.IServerConfigService) tframework.IRedisService {
	plugin := &RedisPlugin{
		configService: configService,
	}
	plugin.InitPlugin()
	return plugin
}
