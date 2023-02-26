package db

import (
	"github.com/thkhxm/tgf/log"
	"reflect"
	"sync"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

//TODO 还需要优化

type BaseDataManager struct {
	cache     *sync.Map
	subStruct interface{}
}

// 全局管理缓存
var dataManagerList map[string]*BaseDataManager
var one = &sync.Once{}

// 定时器开关信号
var tickerSignal bool

// 重启次数
var restartCount int

// 当前操作的缓存Key
var curKey string

// 最大击空数,如果累计击空超过当前次数,拒绝访问数据,需等待资源自动回收后,才可以使用
var maxHit = 1000

var cacheTimeOut = time.Minute * 60 * 3

//var cacheTimeOut = time.Second * 2

// StartDBManagerTicker
// @update tim.huang 2021-08-03 09:47:07
// @Description: 开启缓存管理定时器
func StartDBManagerTicker() {
	tickerSignal = true
	go func() {
		task := time.NewTicker(time.Minute * 1)
		//task := time.NewTicker(time.Second * 1)
		defer func() {
			if err := recover(); err != nil {
				log.Error("BaseDataManager的定时器异常,", err)
				//移除异常的本地缓存队列
				dataManagerList[curKey].cache = new(sync.Map)
				//重启次数自增
				restartCount++
				log.Warn("重新启动定时器，避免数据内存溢出.重启次数：[%d]", restartCount)
				StartDBManagerTicker()
			}
		}()
		for tickerSignal {
			select {
			case <-task.C:
				removeTimeOutCache()
			}
		}
	}()
}

// removeTimeOutCache
// @update tim.huang 2021-08-03 09:46:35
// @Description: 超时移除缓存数据
func removeTimeOutCache() {
	now := time.Now().Unix()
	//log.Info("执行移除方法->%v<-%v",len(dataManagerList),now)
	for key, manager := range dataManagerList {
		curKey = key
		//log.Info("执行移除方法->",len(manager.cache),"<-",now)
		manager.cache.Range(func(cacheKey, value interface{}) bool {
			if value == nil {
				return false
			}
			//log.Info("当前判断得Value:%v",value)
			//log.Info("%d,%d",now-value.(*CacheData).updateTime,int64(cacheTimeOut.Seconds()))
			data := value.(*CacheData)
			timeout := int64(manager.subStruct.(IDataManager).GetTimeOut())
			if timeout > 0 && data.value != nil && (now-data.updateTime) > timeout {
				manager.cache.Delete(cacheKey)
				manager.OnRemove(cacheKey, data)
			}
			return true
		})
	}
}

func (baseDataManager *BaseDataManager) GetTimeOut() float64 {
	return cacheTimeOut.Seconds()
}

// InitStruct
// @update tim.huang 2021-08-03 09:47:25
// @Description: 初始化父类结构
// @receiver BaseDataManager
// @param subStruct 子类结构,需要传入
func (baseDataManager *BaseDataManager) InitStruct(subStruct interface{}) {
	baseDataManager.subStruct = subStruct
	baseDataManager.cache = new(sync.Map)
	one.Do(func() {
		dataManagerList = make(map[string]*BaseDataManager)
		StartDBManagerTicker()
	})
	if _, err := baseDataManager.subStruct.(IDataManager); !err {
		log.Error("BaseDBManager子类[%s]没有实现[%s]接口", reflect.TypeOf(subStruct).Name(), "IDataManager")
	}
	dataManagerList[baseDataManager.subStruct.(IDataManager).GetDBName()] = baseDataManager
}

// Get
// @update tim.huang 2021-08-03 09:50:41
// @Description: 获取缓存数据
// @receiver BaseDataManager
// @param key 缓存唯一Key
// @param db 数据为空，是否从db中读取
// @return interface{}
// @return bool
func (baseDataManager *BaseDataManager) Get(key interface{}, db bool) (interface{}, bool) {
	data, ok := baseDataManager.cache.Load(key)
	var cacheData *CacheData

	var sub = baseDataManager.subStruct.(IDataManager)
	if !ok {
		cacheData = new(CacheData)
		cacheData.value = nil
		cacheData.hit++
		baseDataManager.cache.Store(key, cacheData)
	} else {
		cacheData = data.(*CacheData)
	}
	defer func() {
		//不管任何时候，都会更新时间
		baseDataManager.subStruct.(IDataManager).UpdateTime(cacheData)
	}()
	switch {
	case cacheData.hit > 1000:
		cacheData.hit = 0
		log.Warn("存在数据缓存击穿风险,[%s]", sub.GetRedisKey(key))
		return nil, false
	case cacheData.value != nil:
		//命中本地缓存，直接返回数据
		return cacheData.value, true
	default:
		cacheData.hit++
		reKey := sub.GetRedisKey(key)
		if reKey == "" {
			return nil, false
		}
		ref := sub.InstanceEmptyData()
		if ref == nil {
			return nil, false
		}
		//err := Redis.Get(reKey, ref)
		//if err == nil {
		//	cacheData.value = ref
		//	cacheData.key = key
		//	ok = true
		//}
		//redis没取到数据，从mdb尝试获取/
		if cacheData.value == nil && db {
		}
	}

	return cacheData.value, cacheData.value != nil
}

func (baseDataManager *BaseDataManager) Remove(key interface{}) {
	baseDataManager.cache.Delete(key)
}

func (baseDataManager *BaseDataManager) CheckKey(key interface{}) (has bool) {
	_, has = baseDataManager.cache.Load(key)
	return
}

// Store
// @update tim.huang 2021-08-03 09:51:56
// @Description: 存储数据到缓存
// @receiver BaseDataManager
// @param key
// @param value
// @return bool
func (baseDataManager *BaseDataManager) Store(key, value interface{}) bool {
	var data = new(CacheData)
	data.value = value
	data.key = key
	data.updateTime = time.Now().Unix()

	baseDataManager.cache.Store(key, data)
	var sub = baseDataManager.subStruct.(IDataManager)
	reKey := sub.GetRedisKey(key)
	if reKey != "" {
		//if da, err := json.Marshal(value); err == nil {
		//Redis.Set(reKey, da, baseDataManager.GetExpiration())
		//}
	}
	return true
}

func (baseDataManager *BaseDataManager) OnRemove(key interface{}, val *CacheData) {
	var sub = baseDataManager.subStruct.(IDataManager)
	reKey := sub.GetRedisKey(key)
	if reKey != "" {
		//if da, err := json.Marshal(val.value); err == nil {
		//Redis.Set(reKey, da, baseDataManager.GetExpiration())
		//}
	}
}

// OnDestroy 默认缓存所有数据到redis
func (baseDataManager *BaseDataManager) OnDestroy() {
	var sub = baseDataManager.subStruct.(IDataManager)
	//
	baseDataManager.cache.Range(func(key, value interface{}) bool {
		reKey := sub.GetRedisKey(key)
		if reKey != "" {
			//if da, err := json.Marshal(value.(*CacheData).value); err == nil {
			//	Redis.Set(reKey, da, baseDataManager.GetExpiration())
			//}
		}
		return true
	})
}

func (baseDataManager *BaseDataManager) UpdateTime(cacheData *CacheData) {
	cacheData.updateTime = time.Now().Unix()
}

func (baseDataManager *BaseDataManager) GetExpiration() time.Duration {
	return -1
}

func (baseDataManager *BaseDataManager) Range(executeFunc func(key, value interface{}) bool) {
	baseDataManager.cache.Range(func(key, value interface{}) bool {
		return executeFunc(key, value.(*CacheData).value)
	})
}

type CacheData struct {
	updateTime int64
	key        interface{}
	value      interface{}
	hit        int
}

type IDataManager interface {
	GetRedisKey(in interface{}) string
	GetDBName() string
	// InstanceEmptyData 需要实例化的引用结构对象
	InstanceEmptyData() interface{}
	UpdateTime(cacheData *CacheData)
	GetTimeOut() float64
	GetExpiration() time.Duration
}
