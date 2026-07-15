package db

import (
	"context"
	"errors"
	"github.com/bsm/redislock"
	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
	"github.com/thkhxm/tgf/v2"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/util"
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
	// HDel E4：删除 hash 结构中的指定 field（HDEL，立即删除）。
	// hashAutoCacheManager.Remove 依赖它命中真正的 hash field——
	// 旧实现 Del 的是不存在的字符串 key，删除等于没发生（数据复活缺陷）。
	HDel(key string, fields ...string)
	Del(key string)
	DelNow(key string)
	GetList(key string, start, end int64) (res []string, err error)
	SetList(key string, l []interface{}, timeout time.Duration)
	GetSet(key string) (res []string, err error)
	AddSetItem(key string, val interface{}, timeout time.Duration)
	AddListItem(key string, val string, timeout time.Duration)
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
	if len(data) > 0 {
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

func AddListItemL[Val any](key string, timeout time.Duration, val Val) (err error) {
	if cache == nil {
		return errors.New("cache is nil")
	}
	a, e := util.AnyToStr(val)
	if e != nil {
		err = e
		return
	}
	cache.AddListItem(key, a, timeout)
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

// DelMapField E4
// @Description: 删除 hash 结构（GetMap/PutMap 写入的结构）中的指定 field。
// 与 Del（对整个 key 设短过期）不同，hash field 没有独立 TTL，HDEL 立即删除。
// hashAutoCacheManager.Remove 通过它删除真正的 hash field。
func DelMapField(key string, fields ...string) {
	if cache == nil {
		return
	}
	cache.HDel(key, fields...)
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

func GetRedisClient() redis.UniversalClient {
	if cache == nil {
		return nil
	}
	if r, ok := cache.(*redisService); ok {
		return r.GetClient()
	}
	return nil
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
	// 单批落库的最大条数，0 表示使用 defaultUpdateGroupSize
	longevityGroupSize int
	// 单批落库失败时的最大重试次数（含首次），<=0 表示使用 defaultLongevityRetry
	longevityRetry int
	// C4/A1b：落库失败补偿队列。零值（nil）自动 fallback 到 NoopFailureQueue，
	// 保持 A1 行为不变。业务可通过 WithLongevityFailureQueue 注入 file / redis 等持久化实现。
	longevityFailureQueue FailureQueue
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

// WithLongevityGroupSize
//
//	@Description: 指定单批落库的最大条数，用于在脏数据较多时把一次事务切小。size<=0 时回退到默认值。
func (a *AutoCacheBuilder[Key, Val]) WithLongevityGroupSize(size int) *AutoCacheBuilder[Key, Val] {
	if size > 0 {
		a.longevityGroupSize = size
	}
	return a
}

// WithLongevityRetry
//
//	@Description: 指定单批落库失败时的最大重试次数（含首次），<=0 时回退到默认值。
//	E6 接线：该值经 InitStruct 透传给 sqlBuilder.flushBatch 真实生效
//	（旧版本字段从未被读取，配置静默无效——V3 审计 P2 修复）。
func (a *AutoCacheBuilder[Key, Val]) WithLongevityRetry(attempts int) *AutoCacheBuilder[Key, Val] {
	if attempts > 0 {
		a.longevityRetry = attempts
	}
	return a
}

// WithLongevityFailureQueue C4 / A1b / E4
//
//	@Description: 注入一个落库失败补偿队列。toLongevity 里 flushBatch 在内部
//	重试耗尽后会把当次 batch 序列化进 queue；longevity 管理器创建时框架自动
//	重放（wireFailureReplay），业务也可手动调 db.ReplayFailureQueue /
//	db.ReplayPayload 重放。
//	E4 起默认（nil）回落到进程级 FileFailureQueue（./longevity_failures.log，
//	路径可经 db.SetDefaultFailureQueuePath 定制）；显式传 NoopFailureQueue{}
//	可关闭补偿队列（恢复 A1 行为）。
func (a *AutoCacheBuilder[Key, Val]) WithLongevityFailureQueue(q FailureQueue) *AutoCacheBuilder[Key, Val] {
	a.longevityFailureQueue = q
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
	builder.cacheTimeOut = defaultCacheTimeOut
	builder.memTimeOutSecond = defaultMemTimeOutSecond
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
	builder.cacheTimeOut = defaultCacheTimeOut
	builder.memTimeOutSecond = defaultMemTimeOutSecond
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
	builder.memTimeOutSecond = defaultMemTimeOutSecond
	return builder
}

func NewHashAutoCacheBuilder[Val IHashModel]() *HashAutoCacheBuilder[Val] {
	builder := &HashAutoCacheBuilder[Val]{}
	builder.plugins = make([]IAutoCacheClearPlugin, 0)
	builder.mem = true
	builder.memTimeOutSecond = defaultMemTimeOutSecond
	return builder
}

func WithCacheModule(module tgf.CacheModule) {
	cacheModule = module
}

// ---- v2 默认 cache 超时（启动时从统一配置系统读取，未配置时回落到历史硬编码） ----
//
// 业务可通过：
//
//	DBCacheTimeoutSec=86400   # Redis 缓存默认 TTL，秒
//	DBMemTimeoutSec=3600      # 内存缓存默认 TTL，秒
//
// 调整。零配置时为 Redis 3 天 / 内存 3 小时（v1 行为一致）。
var (
	defaultCacheTimeOut     = time.Hour * 24 * 3 // 3 天
	defaultMemTimeOutSecond = int64(60 * 60 * 3) // 3 小时
)

func init() {
	// E4/E2 配置读点迁移：旧 tgf.GetStrConfig 改为统一配置系统 tgfconfig.Current()
	// （两者读同一份解析结果；tgf 包 init 已保证 Current() 非零值）。
	applyDefaultCacheTimeouts(tgfconfig.Current())
}

// applyDefaultCacheTimeouts 从统一配置应用 DB 层默认 TTL。
// 启动期一次性项（builder 构造时拷贝该值），<=0 时保持历史硬编码默认。
// 抽成独立函数便于表驱动单测。
func applyDefaultCacheTimeouts(cfg *tgfconfig.Config) {
	if v := cfg.DB.CacheTimeoutSec; v > 0 {
		defaultCacheTimeOut = time.Duration(v) * time.Second
	}
	if v := cfg.DB.MemTimeoutSec; v > 0 {
		defaultMemTimeOutSecond = v
	}
}

// setCacheService D5: 只有初始化成功才把 *redisService 装进包级 cache 接口。
// 失败时让 cache 保持"真 nil"——绝不能把 typed-nil 指针塞进接口，
// 否则所有 `cache == nil` 防御全部失效，首次缓存操作即 nil receiver panic。
// 抽成独立函数是为了单测能锁住这条 typed-nil 防线。
func setCacheService(svc *redisService, err error) {
	if err != nil || svc == nil {
		log.ErrorTag("init", "redis 初始化失败,二级缓存降级为关闭(cache=nil) err=%v", err)
		return
	}
	cache = svc
}

func run() {
	switch cacheModule {
	case tgf.CacheModuleRedis:
		setCacheService(newRedisService())
	case tgf.CacheModuleClose:
		return
	}
	//初始化mysql
	initMySql()
}
