package db

import (
	"context"
	"errors"
	"github.com/bsm/redislock"
	"github.com/bytedance/sonic"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"reflect"
	"strings"
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
	GetList(key string, start, end int64) (res []string, err error)
	SetList(key string, l []interface{}, timeout time.Duration)
	GetSet(key string) (res []string, err error)
	AddSetItem(key string, val interface{}, timeout time.Duration)
	AddListItem(key string, val string)
}

type iRedisCacheService interface {
	TryLock(key string) (*redislock.Lock, error)
	TryUnLock(l *redislock.Lock, ctx context.Context)
	Incr(key string, timeout time.Duration) (res int64, err error)
	IncrBy(key string, val float64, timeout time.Duration) (res float64, err error)
	LLen(key string) (res int64, err error)
}

type IAutoCacheService[Key cacheKey, Val any] interface {
	Get(key ...Key) (val Val, err error)
	TryGet(key ...Key) (val Val, err error)
	Set(val Val, key ...Key) (success bool)
	Push(key ...Key)
	Remove(key ...Key) (success bool)
	Reset() IAutoCacheService[Key, Val]
	Range(f func(Key, Val) bool)
}

type IAutoCacheClearPlugin interface {
	PreClear(key string)
	PostClear(key string)
}

type IHashCacheService[Val any] interface {
	IAutoCacheService[string, Val]
	GetAll(key ...string) (val []Val, err error)
}

type IHashModel interface {
	//主键key
	HashCachePkKey(key ...string) string

	//单项Key,调用Get,Set等操作函数的时候,需要保证这里的值有跟主键一起传进来
	HashCacheFieldByVal() string
	HashCacheFieldByKeys(key ...string) string
}

// Get [Res any]
// @Description: 通过二级缓存获取数据
// @param key
// @return res
func Get[Res any](key string) (res Res, success bool) {
	if cache == nil {
		return
	}
	val := cache.Get(key)
	if val != "" {
		res, _ = util.StrToAny[Res](val)
		success = true
	}
	return
}

// FormatKey
// @Description: format redis key,拼接key.
// @example: FormatKey("user",1001) => user:1001
// @param args
// @return string
func FormatKey(args ...string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.Join(args, ":")
}
func Set(key string, val any, timeout time.Duration) {
	if cache == nil {
		return
	}
	switch val.(type) {
	case string:
		cache.Set(key, val, timeout)
	case interface{}:
		data, _ := sonic.Marshal(val)
		cache.Set(key, data, timeout)
	default:
		cache.Set(key, val, timeout)
	}
}

func GetMap[Key cacheKey, Val any](key string) (res map[Key]Val, success bool) {
	if cache == nil {
		return
	}
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
	if cache == nil {
		return
	}
	f, _ := util.AnyToStr(field)
	v, _ := util.AnyToStr(val)
	cache.PutMap(key, f, v, timeout)
}

func GetList[Res any](key string) []Res {
	if cache == nil {
		return nil
	}
	if res, err := cache.GetList(key, 0, -1); err == nil {
		data := make([]Res, len(res))
		for i, r := range res {
			data[i], _ = util.StrToAny[Res](r)
		}
		return data
	}
	return nil
}

func GetListLimit[Res any](key string, start, end int64) []Res {
	if cache == nil {
		return nil
	}
	if res, err := cache.GetList(key, start, end); err == nil {
		data := make([]Res, len(res))
		for i, r := range res {
			data[i], _ = util.StrToAny[Res](r)
		}
		return data
	}
	return nil
}

func AddListItem[Val any](key string, timeout time.Duration, val ...Val) (err error) {
	if cache == nil {
		return errors.New("cache is nil")
	}
	data := make([]interface{}, len(val))
	for i, v := range val {
		a, e := util.AnyToStr(v)
		if e != nil {
			err = e
			return
		}
		data[i] = a
	}
	cache.SetList(key, data, timeout)
	return
}

func GetAllSet[Res any](key string) []Res {
	if cache == nil {
		return nil
	}
	if res, err := cache.GetSet(key); err == nil {
		data := make([]Res, len(res))
		for i, r := range res {
			data[i], _ = util.StrToAny[Res](r)
		}
		return data
	}
	return nil
}

func AddSetItem[Val any](key string, timeout time.Duration, val Val) (err error) {
	if cache == nil {
		return errors.New("cache is nil")
	}
	a, e := util.AnyToStr(val)
	if e != nil {
		err = e
		return
	}
	cache.AddSetItem(key, a, timeout)
	return
}

func Del(key string) {
	if cache == nil {
		return
	}
	cache.Del(key)
}

func DelNow(key string) {
	if cache == nil {
		return
	}
	cache.DelNow(key)
}

// NewLock
// @Description: 创建一个redis锁,用于分布式锁,需要在redis环境下使用,使用完毕后需要调用UnLock释放锁
// @param key
// @return *redislock.Lock
// @return error
func NewLock(key string) (*redislock.Lock, error) {
	if cache == nil {
		return nil, errors.New("cache is nil")
	}
	if r, ok := cache.(iRedisCacheService); ok {
		return r.TryLock(key)
	}
	return nil, errors.New("cache is not redis")
}

// UnLock
// @Description: 释放锁
// @param l
func UnLock(l *redislock.Lock) {
	if cache == nil {
		return
	}
	if r, ok := cache.(iRedisCacheService); ok {
		r.TryUnLock(l, context.Background())
	}
}

// Incr
// @Description: 释放锁
// @param l
func Incr(key string, timeout time.Duration) (res int64, err error) {
	if cache == nil {
		return
	}
	if r, ok := cache.(iRedisCacheService); ok {
		res, err = r.Incr(key, timeout)
	}
	return
}

// IncrBy
// @Description: 释放锁
// @param l
func IncrBy(key string, val float64, timeout time.Duration) (res float64, err error) {
	if cache == nil {
		return
	}
	if r, ok := cache.(iRedisCacheService); ok {
		res, err = r.IncrBy(key, val, timeout)
	}
	return
}

// LLen
// @Description: 获取list长度
// @param l
func LLen(key string) (res int64, err error) {
	if cache == nil {
		return
	}
	if r, ok := cache.(iRedisCacheService); ok {
		res, err = r.LLen(key)
	}
	return
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
	keyFun string

	//

	//数据是否持久化
	longevity         bool
	longevityInterval time.Duration
	//
	//是否自动清除过期数据
	autoClear        bool
	cacheTimeOut     time.Duration
	memTimeOutSecond int64

	plugins []IAutoCacheClearPlugin
}

type HashAutoCacheBuilder[Val IHashModel] struct {
	AutoCacheBuilder[string, Val]
	image Val
}

func (h *HashAutoCacheBuilder[Val]) New() IHashCacheService[Val] {
	manager := &hashAutoCacheManager[Val]{}
	manager.builder = &h.AutoCacheBuilder
	manager.builder.WithCloseAutoClearCache()

	// Use reflection to create a new instance of Val
	valType := reflect.TypeOf(h.image).Elem()
	newVal := reflect.New(valType).Interface()

	// Cast the newVal to the type Val
	h.image = newVal.(Val)

	manager.InitStruct(h.image)
	return manager
}
func (h *HashAutoCacheBuilder[Val]) WithAutoCache(cacheKey string, cacheTimeOut time.Duration) *HashAutoCacheBuilder[Val] {
	h.AutoCacheBuilder.WithAutoCache(cacheKey, cacheTimeOut)
	return h
}
func (h *HashAutoCacheBuilder[Val]) WithMemCache(memTimeOutSecond uint32) *HashAutoCacheBuilder[Val] {
	h.AutoCacheBuilder.WithMemCache(memTimeOutSecond)
	return h
}
func (h *HashAutoCacheBuilder[Val]) WithLongevityCache(updateInterval time.Duration) *HashAutoCacheBuilder[Val] {
	h.AutoCacheBuilder.WithLongevityCache(updateInterval)
	return h
}
func (h *HashAutoCacheBuilder[Val]) WithCloseAutoClearCache() *HashAutoCacheBuilder[Val] {
	h.AutoCacheBuilder.WithCloseAutoClearCache()
	return h
}
func (h *HashAutoCacheBuilder[Val]) WithAutoClearPlugins(plugin IAutoCacheClearPlugin) *HashAutoCacheBuilder[Val] {
	h.AutoCacheBuilder.WithAutoClearPlugins(plugin)
	return h
}

func (a *AutoCacheBuilder[Key, Val]) New() IAutoCacheService[Key, Val] {
	var ()
	manager := &autoCacheManager[Key, Val]{}
	manager.builder = a
	manager.InitStruct()
	return manager
}
func (a *AutoCacheBuilder[Key, Val]) WithAutoCache(cacheKey string, cacheTimeOut time.Duration) *AutoCacheBuilder[Key, Val] {
	var ()
	a.cache = true
	a.keyFun = cacheKey

	if cacheTimeOut > 0 {
		a.cacheTimeOut = cacheTimeOut
	}

	return a
}
func (a *AutoCacheBuilder[Key, Val]) WithCloseAutoClearCache() *AutoCacheBuilder[Key, Val] {
	a.autoClear = false
	return a
}
func (a *AutoCacheBuilder[Key, Val]) WithMemCache(memTimeOutSecond uint32) *AutoCacheBuilder[Key, Val] {
	var ()
	a.mem = true
	if memTimeOutSecond>>31 == 1 {
		memTimeOutSecond = 0
	}
	if memTimeOutSecond != 0 {
		a.autoClear = true
	}
	a.memTimeOutSecond = int64(memTimeOutSecond)

	return a
}
func (a *AutoCacheBuilder[Key, Val]) WithAutoClearPlugins(plugin IAutoCacheClearPlugin) *AutoCacheBuilder[Key, Val] {
	var ()
	a.plugins = append(a.plugins, plugin)
	return a
}

func (a *AutoCacheBuilder[Key, Val]) WithLongevityCache(updateInterval time.Duration) *AutoCacheBuilder[Key, Val] {
	a.longevity = true
	if updateInterval < time.Second {
		log.WarnTag("orm", "updateInterval minimum is 1 second")
		updateInterval = time.Second
	}
	a.longevityInterval = updateInterval
	return a
}

// NewDefaultAutoCacheManager [Key comparable, Val any]
//
//	@Description: 创建一个默认的自动化数据管理，默认不包含持久化数据落地(mysql)，包含本地缓存，cache缓存(redis)
//	@param cacheKey cache缓存使用的组合key，例如user:1001 那么这里应该传入user即可，拼装方式为cacheKey:key
//	@return IAutoCacheService [Key comparable, Val any] 返回一个全新的自动化数据缓存管理对象
func NewDefaultAutoCacheManager[Key cacheKey, Val any](cacheKey string) IAutoCacheService[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.keyFun = cacheKey
	builder.mem = true
	builder.plugins = make([]IAutoCacheClearPlugin, 0)
	builder.autoClear = true
	builder.cache = true
	builder.cacheTimeOut = time.Hour * 24 * 3
	builder.memTimeOutSecond = 60 * 60 * 3
	builder.longevity = false
	return builder.New()
}

// NewLongevityAutoCacheManager [Key comparable, Val any]
//
//	@Description: 创建一个持久化的自动化数据管理，包含持久化数据落地(mysql)，包含本地缓存，cache缓存(redis)
//	@param cacheKey
//	@param tableName
//	@return IAutoCacheService [Key comparable, Val any]
func NewLongevityAutoCacheManager[Key cacheKey, Val IModel](cacheKey string) IAutoCacheService[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.keyFun = cacheKey
	builder.mem = true
	builder.plugins = make([]IAutoCacheClearPlugin, 0)
	builder.autoClear = true
	builder.cache = true
	builder.cacheTimeOut = time.Hour * 24 * 3
	builder.memTimeOutSecond = 60 * 60 * 3
	builder.longevity = true
	return builder.New()
}

// NewAutoCacheManager [Key comparable, Val any]
// @Description: 创建一个持久化的自动化数据管理，包含本地缓存，不包含持久化数据落地(mysql)，cache缓存(redis)
func NewAutoCacheManager[Key cacheKey, Val any](memTimeOutSecond int64) IAutoCacheService[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.plugins = make([]IAutoCacheClearPlugin, 0)
	builder.keyFun = ""
	builder.mem = true
	builder.cache = false
	builder.longevity = false
	builder.memTimeOutSecond = memTimeOutSecond
	return builder.New()
}

func NewAutoCacheBuilder[Key cacheKey, Val any]() *AutoCacheBuilder[Key, Val] {
	builder := &AutoCacheBuilder[Key, Val]{}
	builder.plugins = make([]IAutoCacheClearPlugin, 0)
	builder.mem = true
	builder.memTimeOutSecond = 60 * 60 * 3
	return builder
}

func NewHashAutoCacheBuilder[Val IHashModel]() *HashAutoCacheBuilder[Val] {
	builder := &HashAutoCacheBuilder[Val]{}
	builder.plugins = make([]IAutoCacheClearPlugin, 0)
	builder.mem = true
	builder.memTimeOutSecond = 60 * 60 * 3
	return builder
}

func WithCacheModule(module tgf.CacheModule) {
	cacheModule = module
}

func run() {
	switch cacheModule {
	case tgf.CacheModuleRedis:
		cache = newRedisService()
	case tgf.CacheModuleClose:
		return
	}
	//初始化mysql
	initMySql()
}
