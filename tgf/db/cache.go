package db

import (
	"encoding/json"
	"fmt"
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

var cache iCacheService

var cacheModule = tgf.CacheModuleRedis

type iCacheService interface {
	Get(key string) (res string)
	Set(key string, val any, timeout time.Duration)
	GetMap(key string) map[string]string
	PutMap(key, filed, val string, timeout time.Duration)
	Del(key string)
	DelNow(key string)
	GetList(key string) (res []string, err error)
	SetList(key string, l []interface{}, timeout time.Duration)
	AddListItem(key string, val string)
}

type IAutoCacheService[Key cacheKey, Val any] interface {
	Get(key Key) (val Val, err error)
	Set(key Key, val Val) (success bool)
	Remove(key Key) (success bool)
	Reset() IAutoCacheService[Key, Val]
}

// Get [Res any]
// @Description: 通过二级缓存获取数据
// @param key
// @return res
func Get[Res any](key string) (res Res, success bool) {
	val := cache.Get(key)
	if val != "" {
		res, _ = util.StrToAny[Res](val)
		success = true
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

func GetMap[Key cacheKey, Val any](key string) (res map[Key]Val, success bool) {
	data := cache.GetMap(key)
	if data != nil && len(data) > 0 {
		res = make(map[Key]Val, len(data))
		for k, v := range data {
			kk, _ := util.StrToAny[Key](k)
			vv, _ := util.StrToAny[Val](v)
			res[kk] = vv
		}
		success = true
	}
	return
}

func PutMap[Key cacheKey, Val any](key string, field Key, val Val, timeout time.Duration) {
	f, _ := util.AnyToStr(field)
	v, _ := util.AnyToStr(val)
	cache.PutMap(key, f, v, timeout)
}

func GetList[Res any](key string) []Res {
	if res, err := cache.GetList(key); err == nil {
		data := make([]Res, len(res))
		for i, r := range res {
			data[i], _ = util.StrToAny[Res](r)
		}
		return data
	}
	return nil
}

func AddListItem(key string, val any) error {
	data, err := util.AnyToStr(val)
	if err != nil {
		return err
	}
	cache.AddListItem(key, data)
	return nil
}

func SetList[Val any](key string, l []Val, timeout time.Duration) {
	data := make([]interface{}, len(l))
	for i, val := range l {
		a, _ := util.AnyToStr(val)
		data[i] = a
	}
	cache.SetList(key, data, timeout)
}

func Del(key string) {
	cache.Del(key)
}

func DelNow(key string) {
	cache.DelNow(key)
}

// AutoCacheBuilder [Key comparable,Val any]
// @Description: 自动化缓存Builder
type AutoCacheBuilder[Key cacheKey, Val any] struct {

	//数据是否在本地存储
	mem bool

	//

	//数据是否缓存
	cache bool
	//获取唯一key的拼接函数
	keyFun func(key Key) string

	//

	//数据是否持久化
	longevity bool
	//持久化表名
	tableName string

	//
	//是否自动清除过期数据
	autoClear        bool
	cacheTimeOut     time.Duration
	memTimeOutSecond int64
}

func (this *AutoCacheBuilder[Key, Val]) New() IAutoCacheService[Key, Val] {
	var ()
	manager := &autoCacheManager[Key, Val]{}
	manager.builder = this
	manager.InitStruct()
	return manager
}

func (this *AutoCacheBuilder[Key, Val]) WithAutoCache(open bool) *AutoCacheBuilder[Key, Val] {
	var ()
	this.autoClear = open
	return this
}

// NewDefaultAutoCacheManager [Key comparable, Val any]
//
//	@Description: 创建一个默认的自动化数据管理，默认不包含持久化数据落地(mysql)，包含本地缓存，cache缓存(redis)
//	@param cacheKey cache缓存使用的组合key，例如user:1001 那么这里应该传入user即可，拼装方式为cacheKey:key
//	@return IAutoCacheService [Key comparable, Val any] 返回一个全新的自动化数据缓存管理对象
func NewDefaultAutoCacheManager[Key cacheKey, Val any](cacheKey string) IAutoCacheService[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.keyFun = func(key Key) string {
		return fmt.Sprintf("%v:%v", cacheKey, key)
	}
	builder.mem = true
	builder.cache = true
	builder.cacheTimeOut = time.Hour * 24 * 3
	builder.longevity = false
	builder.tableName = ""
	return builder.New()
}

// NewLongevityAutoCacheManager [Key comparable, Val any]
//
//	@Description: 创建一个持久化的自动化数据管理，包含持久化数据落地(mysql)，包含本地缓存，cache缓存(redis)
//	@param cacheKey
//	@param tableName
//	@return IAutoCacheService [Key comparable, Val any]
func NewLongevityAutoCacheManager[Key cacheKey, Val any](cacheKey, tableName string) IAutoCacheService[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.keyFun = func(key Key) string {
		return fmt.Sprintf("%v:%v", cacheKey, key)
	}
	builder.mem = true
	builder.cache = true
	builder.cacheTimeOut = time.Hour * 24 * 3
	builder.longevity = true
	builder.tableName = tableName

	return builder.New()
}

// NewAutoCacheManager [Key comparable, Val any]
// @Description: 创建一个持久化的自动化数据管理，包含本地缓存，不包含持久化数据落地(mysql)，cache缓存(redis)
func NewAutoCacheManager[Key cacheKey, Val any]() IAutoCacheService[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.keyFun = func(key Key) string {
		return ""
	}
	builder.mem = true
	builder.cache = false
	builder.longevity = false
	builder.tableName = ""
	return builder.New()
}

func NewAutoCacheBuilder[Key cacheKey, Val any]() *AutoCacheBuilder[Key, Val] {
	return &AutoCacheBuilder[Key, Val]{}
}

func WithCacheModule(module tgf.CacheModule) {
	cacheModule = module
}

func run() {
	switch cacheModule {
	case tgf.CacheModuleRedis:
		cache = newRedisService()
	}
	//初始化mysql
	initMySql()
}
