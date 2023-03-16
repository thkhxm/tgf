package db

import (
	"github.com/cornelk/hashmap"
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
	this.clearTimer = time.NewTicker(time.Minute)
}

//TODO 还需要优化
//
//type BaseDataManager struct {
//	cache     *sync.Map
//	subStruct interface{}
//}
//
//// 全局管理缓存
//var dataManagerList map[string]*BaseDataManager
//var one = &sync.Once{}
//
//// 定时器开关信号
//var tickerSignal bool
//
//// 重启次数
//var restartCount int
//
//// 当前操作的缓存Key
//var curKey string
//
//// 最大击空数,如果累计击空超过当前次数,拒绝访问数据,需等待资源自动回收后,才可以使用
//var maxHit = 1000
//
//var cacheTimeOut = time.Minute * 60 * 3
//
////var cacheTimeOut = time.Second * 2
//
//// StartDBManagerTicker
//// @update tim.huang 2021-08-03 09:47:07
//// @Description: 开启缓存管理定时器
//func StartDBManagerTicker() {
//	tickerSignal = true
//	go func() {
//		task := time.NewTicker(time.Minute * 1)
//		//task := time.NewTicker(time.Second * 1)
//		defer func() {
//			if err := recover(); err != nil {
//				log.Error("BaseDataManager的定时器异常,", err)
//				//移除异常的本地缓存队列
//				dataManagerList[curKey].cache = new(sync.Map)
//				//重启次数自增
//				restartCount++
//				log.Warn("重新启动定时器，避免数据内存溢出.重启次数：[%d]", restartCount)
//				StartDBManagerTicker()
//			}
//		}()
//		for tickerSignal {
//			select {
//			case <-task.C:
//				removeTimeOutCache()
//			}
//		}
//	}()
//}
//
//// removeTimeOutCache
//// @update tim.huang 2021-08-03 09:46:35
//// @Description: 超时移除缓存数据
//func removeTimeOutCache() {
//	now := time.Now().Unix()
//	//log.Info("执行移除方法->%v<-%v",len(dataManagerList),now)
//	for key, manager := range dataManagerList {
//		curKey = key
//		//log.Info("执行移除方法->",len(manager.cache),"<-",now)
//		manager.cache.Range(func(cacheKey, value interface{}) bool {
//			if value == nil {
//				return false
//			}
//			//log.Info("当前判断得Value:%v",value)
//			//log.Info("%d,%d",now-value.(*CacheData).updateTime,int64(cacheTimeOut.Seconds()))
//			data := value.(*CacheData)
//			timeout := int64(manager.subStruct.(IDataManager).GetTimeOut())
//			if timeout > 0 && data.value != nil && (now-data.updateTime) > timeout {
//				manager.cache.Delete(cacheKey)
//				manager.OnRemove(cacheKey, data)
//			}
//			return true
//		})
//	}
//}
//
//func (baseDataManager *BaseDataManager) GetTimeOut() float64 {
//	return cacheTimeOut.Seconds()
//}
//
//// InitStruct
//// @update tim.huang 2021-08-03 09:47:25
//// @Description: 初始化父类结构
//// @receiver BaseDataManager
//// @param subStruct 子类结构,需要传入
//func (baseDataManager *BaseDataManager) InitStruct(subStruct interface{}) {
//	baseDataManager.subStruct = subStruct
//	baseDataManager.cache = new(sync.Map)
//	one.Do(func() {
//		dataManagerList = make(map[string]*BaseDataManager)
//		StartDBManagerTicker()
//	})
//	if _, err := baseDataManager.subStruct.(IDataManager); !err {
//		log.Error("BaseDBManager子类[%s]没有实现[%s]接口", reflect.TypeOf(subStruct).Name(), "IDataManager")
//	}
//	dataManagerList[baseDataManager.subStruct.(IDataManager).GetDBName()] = baseDataManager
//}
//
//// Get
//// @update tim.huang 2021-08-03 09:50:41
//// @Description: 获取缓存数据
//// @receiver BaseDataManager
//// @param key 缓存唯一Key
//// @param db 数据为空，是否从db中读取
//// @return interface{}
//// @return bool
//func (baseDataManager *BaseDataManager) Get(key interface{}, db bool) (interface{}, bool) {
//	data, ok := baseDataManager.cache.Load(key)
//	var cacheData *CacheData
//
//	var sub = baseDataManager.subStruct.(IDataManager)
//	if !ok {
//		cacheData = new(CacheData)
//		cacheData.value = nil
//		cacheData.hit++
//		baseDataManager.cache.Store(key, cacheData)
//	} else {
//		cacheData = data.(*CacheData)
//	}
//	defer func() {
//		//不管任何时候，都会更新时间
//		baseDataManager.subStruct.(IDataManager).UpdateTime(cacheData)
//	}()
//	switch {
//	case cacheData.hit > 1000:
//		cacheData.hit = 0
//		log.Warn("存在数据缓存击穿风险,[%s]", sub.GetRedisKey(key))
//		return nil, false
//	case cacheData.value != nil:
//		//命中本地缓存，直接返回数据
//		return cacheData.value, true
//	default:
//		cacheData.hit++
//		reKey := sub.GetRedisKey(key)
//		if reKey == "" {
//			return nil, false
//		}
//		ref := sub.InstanceEmptyData()
//		if ref == nil {
//			return nil, false
//		}
//		//err := Redis.Get(reKey, ref)
//		//if err == nil {
//		//	cacheData.value = ref
//		//	cacheData.key = key
//		//	ok = true
//		//}
//		//redis没取到数据，从mdb尝试获取/
//		if cacheData.value == nil && db {
//		}
//	}
//
//	return cacheData.value, cacheData.value != nil
//}
//
//func (baseDataManager *BaseDataManager) Remove(key interface{}) {
//	baseDataManager.cache.Delete(key)
//}
//
//func (baseDataManager *BaseDataManager) CheckKey(key interface{}) (has bool) {
//	_, has = baseDataManager.cache.Load(key)
//	return
//}
//
//// Store
//// @update tim.huang 2021-08-03 09:51:56
//// @Description: 存储数据到缓存
//// @receiver BaseDataManager
//// @param key
//// @param value
//// @return bool
//func (baseDataManager *BaseDataManager) Store(key, value interface{}) bool {
//	var data = new(CacheData)
//	data.value = value
//	data.key = key
//	data.updateTime = time.Now().Unix()
//
//	baseDataManager.cache.Store(key, data)
//	var sub = baseDataManager.subStruct.(IDataManager)
//	reKey := sub.GetRedisKey(key)
//	if reKey != "" {
//		//if da, err := json.Marshal(value); err == nil {
//		//Redis.Set(reKey, da, baseDataManager.GetExpiration())
//		//}
//	}
//	return true
//}
//
//func (baseDataManager *BaseDataManager) OnRemove(key interface{}, val *CacheData) {
//	var sub = baseDataManager.subStruct.(IDataManager)
//	reKey := sub.GetRedisKey(key)
//	if reKey != "" {
//		//if da, err := json.Marshal(val.value); err == nil {
//		//Redis.Set(reKey, da, baseDataManager.GetExpiration())
//		//}
//	}
//}
//
//// OnDestroy 默认缓存所有数据到redis
//func (baseDataManager *BaseDataManager) OnDestroy() {
//	var sub = baseDataManager.subStruct.(IDataManager)
//	//
//	baseDataManager.cache.Range(func(key, value interface{}) bool {
//		reKey := sub.GetRedisKey(key)
//		if reKey != "" {
//			//if da, err := json.Marshal(value.(*CacheData).value); err == nil {
//			//	Redis.Set(reKey, da, baseDataManager.GetExpiration())
//			//}
//		}
//		return true
//	})
//}
//
//func (baseDataManager *BaseDataManager) UpdateTime(cacheData *CacheData) {
//	cacheData.updateTime = time.Now().Unix()
//}
//
//func (baseDataManager *BaseDataManager) GetExpiration() time.Duration {
//	return -1
//}
//
//func (baseDataManager *BaseDataManager) Range(executeFunc func(key, value interface{}) bool) {
//	baseDataManager.cache.Range(func(key, value interface{}) bool {
//		return executeFunc(key, value.(*CacheData).value)
//	})
//}
//
//type CacheData struct {
//	updateTime int64
//	key        interface{}
//	value      interface{}
//	hit        int
//}
//
//type IDataManager interface {
//	GetRedisKey(in interface{}) string
//	GetDBName() string
//	// InstanceEmptyData 需要实例化的引用结构对象
//	InstanceEmptyData() interface{}
//	UpdateTime(cacheData *CacheData)
//	GetTimeOut() float64
//	GetExpiration() time.Duration
//}
//
