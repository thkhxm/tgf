package db

import (
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

type cacheKey interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr | ~float32 | ~float64 | ~string
}

type cacheData[Val any] struct {
	data      Val
	clearTime int64
}

func newCacheData[Val any](data Val, second int64) *cacheData[Val] {
	res := &cacheData[Val]{}
	res.data = data
	if second > 0 {
		res.clearTime = time.Now().Unix() + second
	}
	return res
}

func (this *cacheData[Val]) checkTimeOut(now int64) bool {
	var ()
	return this.clearTime != 0 && now > this.clearTime
}

func (this *cacheData[Val]) getData(second int64) Val {
	var ()
	if second > 0 {
		this.clearTime = time.Now().Unix() + second
	}
	return this.data
}

type autoCacheManager[Key cacheKey, Val any] struct {
	builder *AutoCacheBuilder[Key, Val]
	//
	cacheMap *hashmap.Map[Key, *cacheData[Val]]
	//
	clearTimer *time.Ticker
}

func (this *autoCacheManager[Key, Val]) get(key Key) (Val, bool) {
	var ()
	if data, suc := this.cacheMap.Get(key); suc {
		return data.getData(this.memTimeOutSecond()), true
	}
	return *new(Val), false
}

func (this *autoCacheManager[Key, Val]) Get(key Key) (val Val, err error) {
	var suc bool
	//先从本地缓存获取
	if this.mem() {
		if val, suc = this.get(key); suc {
			return
		}
	}
	//从cache缓存中获取
	if this.cache() {
		if val, suc = Get[Val](this.getCacheKey(key)); suc {
			this.cacheMap.Set(key, newCacheData[Val](val, this.memTimeOutSecond()))
		}
	}
	//TODO 从db获取
	return
}

func (this *autoCacheManager[Key, Val]) Set(key Key, val Val) (success bool) {
	this.cacheMap.Set(key, newCacheData[Val](val, this.memTimeOutSecond()))
	if this.cache() {
		Set(this.getCacheKey(key), val, this.cacheTimeOut())
	}
	success = true
	return
}

func (this *autoCacheManager[Key, Val]) Push(key Key) {
	var ()
	if !this.cache() {
		return
	}
	if val, err := this.Get(key); err == nil {
		Set(this.getCacheKey(key), val, this.cacheTimeOut())
	}
}

func (this *autoCacheManager[Key, Val]) Remove(key Key) (success bool) {
	this.cacheMap.Del(key)
	//设置过期时间，不直接删除
	if this.cache() {
		Del(this.getCacheKey(key))
	}
	success = true
	return
}

func (this *autoCacheManager[Key, Val]) Reset() IAutoCacheService[Key, Val] {
	util.Go(func() {
		this.Destroy()
	})
	return this.builder.New()
}

func (this *autoCacheManager[Key, Val]) Destroy() {
	var ()
	//TODO 缓存之前的列表
	this.toLongevity()
}

func (this *autoCacheManager[Key, Val]) autoClear() {
	var ()
	now := time.Now().Unix()
	//初始化1/5的容量
	removeKeys := make([]Key, 0, this.cacheMap.Len()/5)
	this.cacheMap.Range(func(k Key, c *cacheData[Val]) bool {
		if c.checkTimeOut(now) {
			removeKeys = append(removeKeys, k)
		}
		return true
	})
	//
	for _, key := range removeKeys {
		this.cacheMap.Del(key)
	}
	log.DebugTag("cache", "remove timeout keys len: %v", len(removeKeys))
}

//TODO 使用定时器，分阶段对数据进行远程数据落库

func (this *autoCacheManager[Key, Val]) getCacheKey(key Key) string {
	var ()
	return this.builder.keyFun(key)
}

func (this *autoCacheManager[Key, Val]) toLongevity() {
	var ()
}

func (this *autoCacheManager[Key, Val]) mem() bool {
	var ()
	return this.builder.mem
}
func (this *autoCacheManager[Key, Val]) memTimeOutSecond() int64 {
	var ()
	return this.builder.memTimeOutSecond
}

func (this *autoCacheManager[Key, Val]) cache() bool {
	var ()
	return this.builder.cache
}

func (this *autoCacheManager[Key, Val]) longevity() bool {
	var ()
	return this.builder.longevity
}
func (this *autoCacheManager[Key, Val]) cacheTimeOut() time.Duration {
	var ()
	return this.builder.cacheTimeOut
}

func (this *autoCacheManager[Key, Val]) InitStruct() {
	var ()
	this.cacheMap = hashmap.New[Key, *cacheData[Val]]()
	//开启自动清除过期数据
	if this.builder.autoClear {
		this.clearTimer = time.NewTicker(time.Minute)
		util.Go(func() {
			for {
				select {
				case <-this.clearTimer.C:
					this.autoClear()
				}
			}
		})
	}
}
