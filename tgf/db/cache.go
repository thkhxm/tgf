package db

import (
	"encoding/json"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/util"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

var cache ICacheService

var cacheModule = tgf.CacheModuleRedis

type ICacheService interface {
	Get(key string) (res string)
	Set(key string, val any, timeout time.Duration)
	GetMap(key string) map[string]string
	PutMap(key, filed, val string, timeout time.Duration)
}

type IAutoCacheService[Key comparable, Val any] interface {
	Get(key Key) (val Val, err error)
	Set(key Key, val Val) (success bool)
	Remove(key Key) (success bool)
	RemoveAll() (success bool)
}

// Get [Res any]
// @Description: 通过二级缓存获取数据
// @param key
// @return res
func Get[Res any](key string) (res Res) {
	val := cache.Get(key)
	if val != "" {
		res, _ = util.StrToAny[Res](val)
	}
	return
}

func Set(key string, val any, timeout time.Duration) {
	switch val.(type) {
	case interface{}:
		data, _ := json.Marshal(val)
		cache.Set(key, data, timeout)
	default:
		cache.Set(key, val, timeout)
	}
}

func GetMap[Key comparable, Val any](key string) map[Key]Val {
	data := cache.GetMap(key)

	res := make(map[Key]Val, len(data))
	for k, v := range data {
		kk, _ := util.StrToAny[Key](k)
		vv, _ := util.StrToAny[Val](v)
		res[kk] = vv
	}
	return res
}

func PutMap[Key comparable, Val any](key string, field Key, val Val, timeout time.Duration) {
	f, _ := util.AnyToStr(field)
	v, _ := util.AnyToStr(val)
	cache.PutMap(key, f, v, timeout)
}

// NewAutoCacheManager
// @Description: 返回一个自动管理的缓存管理
func NewAutoCacheManager[Key comparable, Val any]() IAutoCacheService[Key, Val] {
	manager := &autoCacheManager[Key, Val]{}
	return manager
}

func WithCacheModule(module tgf.CacheModule) {
	cacheModule = module
}

func run() {
	switch cacheModule {
	case tgf.CacheModuleRedis:
		cache = newRedisService()
	}
}
