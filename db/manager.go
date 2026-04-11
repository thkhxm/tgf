package db

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"golang.org/x/net/context"
	"golang.org/x/sync/singleflight"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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

type cacheDataState uint8

const (
	data_default = 0
	data_del     = 1 << 1
	data_update  = 1 << 2
)

// cacheData 是写回缓存的最小数据单元。
//
// B6 修复：`state` 从裸 uint8 升级为 atomic.Uint32。
// 原因：autoCacheManager 的读路径（Get / Range / GetAll）与写路径（Set / toLongevity /
// del）可能并发访问同一个 cacheData，而 state 同时被 checkState / removeState / update
// 读写。A1 重写 toLongevity 时只解决了"落库一致性"，没解决 state 字段本身的 data race。
// B6 在 component 并发测试中被 race detector 抓出。
//
// 升级到 atomic.Uint32 而不是 uint8 是因为 Go stdlib 没有 atomic.Uint8——额外 3 字节
// 对每个 cache 条目可忽略。
type cacheData[Val any] struct {
	data      Val
	clearTime int64
	state     atomic.Uint32
}

var defaultUpdateGroupSize = 500

const (
	// 单批落库失败时的默认重试次数（含首次）
	defaultLongevityRetry = 3
	// 单批落库失败后的首个退避间隔
	defaultLongevityRetryBackoff = 50 * time.Millisecond
)

const StateName = "state"

type autoCacheManager[Key cacheKey, Val any] struct {
	builder *AutoCacheBuilder[Key, Val]
	//
	cacheMap *hashmap.Map[string, *cacheData[Val]]
	//
	clearTimer     *time.Timer
	longevityTimer *time.Timer
	//
	sb *sqlBuilder[Val]
	//
	longevityLock *sync.Mutex

	clearPlugins []IAutoCacheClearPlugin

	sf *singleflight.Group

	// 单批落库执行函数，默认指向 sqlBuilder.flushBatch；测试可替换。
	flushBatch func(values []any, count int) error
}

type hashAutoCacheManager[Val IHashModel] struct {
	autoCacheManager[string, Val]
	groupAutoCacheManager IAutoCacheService[string, []string]
	image                 IHashModel
}

func (h *hashAutoCacheManager[Val]) InitStruct(image Val) {
	h.autoCacheManager.InitStruct()
	h.groupAutoCacheManager = NewAutoCacheBuilder[string, []string]().
		WithMemCache(uint32(h.builder.memTimeOutSecond)).
		WithAutoClearPlugins(h).
		New()
	h.image = image
}

func (h *hashAutoCacheManager[Val]) PreClear(key string) {
	if keys, ok := h.groupAutoCacheManager.Get(key); ok == nil {
		for _, s := range keys {
			h.autoCacheManager.cacheMap.Del(s)
		}
	}
	return
}

func (h *hashAutoCacheManager[Val]) PostClear(key string) {

}

func (h *hashAutoCacheManager[Val]) loadCache(key ...string) (keys []string) {
	//获取主键key
	localKey := h.image.HashCachePkKey(key...)
	defer func() {
		if err := recover(); err != nil {
			log.ErrorTag("cache", "load cache error:%v", err)
			return
		}
		if keys != nil {
			h.groupAutoCacheManager.Set(keys, localKey)
		}
	}()

	v, _, _ := h.sf.Do("loadCache:"+localKey, func() (interface{}, error) {
		//从cache缓存中获取
		if h.cache() {
			//根据主键Key组合成redis的Key,获取hash数据
			if val, suc := GetMap[string, Val](h.getCacheKey(localKey)); suc {
				i := 0
				ak := make([]string, len(val))
				for _, v := range val {
					//根据主键Key和hashKey组成唯一的cacheKey
					lk := h.getLocalKey(localKey, v.HashCacheFieldByVal())
					h.set(lk, v)
					//将该cacheKey放入slice中,用于管理用户的key列表
					ak[i] = lk
					i++
				}
				return ak, nil
			}
		}

		//从db获取
		if h.longevity() {
			d := make([]any, len(key))
			for i, k := range key {
				d[i] = k
			}
			val, err := h.sb.queryList(d...)
			if err == nil {
				ak := make([]string, len(val))
				for i, v := range val {
					lk := h.getLocalKey(localKey, v.HashCacheFieldByVal())
					h.set(lk, v)
					PutMap(h.getCacheKey(localKey), v.HashCacheFieldByVal(), v, h.cacheTimeOut())
					ak[i] = lk
				}
				return ak, nil
			}
		}
		return make([]string, 0), errors.New("not found in cache")
	})
	keys = v.([]string)
	return
}

func (h *hashAutoCacheManager[Val]) Get(key ...string) (val Val, err error) {
	mKey := h.image.HashCachePkKey(key...)
	//是否首次加载，如果是
	if _, has := h.groupAutoCacheManager.Get(mKey); has != nil {
		h.loadCache(key...)
	}
	//
	localKey := h.getLocalKey(mKey, h.image.HashCacheFieldByKeys(key...))
	//从本地缓存获取
	if v, has, cd := h.get(localKey); has && !cd.checkState(data_del) {
		return v, nil
	}
	return val, errors.New("data not found in cache")
}

func (h *hashAutoCacheManager[Val]) Set(val Val, key ...string) (success bool) {
	mKey := h.image.HashCachePkKey(key...)
	var keys []string
	var has error
	//是否首次加载
	if keys, has = h.groupAutoCacheManager.Get(mKey); has != nil {
		keys = h.loadCache(key...)
	}
	//
	fieldKey := val.HashCacheFieldByVal()
	localKey := h.getLocalKey(mKey, fieldKey)
	//放入本地cache缓存中
	cd := h.set(localKey, val)
	//判断是否需要添加到key列表
	var ap = true
	for _, k := range keys {
		if k == localKey {
			ap = false
		}
	}
	if ap {
		keys = append(keys, localKey)
		h.groupAutoCacheManager.Set(keys, mKey)
	}
	//写入redis缓存
	if h.cache() {
		PutMap(h.getCacheKey(mKey), fieldKey, val, h.cacheTimeOut())
	}

	//写入db
	if h.longevity() {
		cd.update()
	}

	return true
}

func (h *hashAutoCacheManager[Val]) Push(key ...string) {
	mKey := h.image.HashCachePkKey(key...)
	fieldKey := h.image.HashCacheFieldByKeys(key...)
	localKey := h.getLocalKey(mKey, fieldKey)

	if localCacheData, ok := h.cacheMap.Get(localKey); ok {
		localCacheData.removeState(data_del)
		if h.cache() {
			PutMap(h.getCacheKey(mKey), fieldKey, localCacheData.data, h.cacheTimeOut())
		}
		if h.longevity() {
			localCacheData.update()
		}
	}
}

func (h *hashAutoCacheManager[Val]) Remove(key ...string) (success bool) {
	mKey := h.image.HashCachePkKey(key...)
	fieldKey := h.image.HashCacheFieldByKeys(key...)
	localKey := h.getLocalKey(mKey, fieldKey)
	var keys []string
	var has error
	//是否首次加载
	if keys, has = h.groupAutoCacheManager.Get(mKey); has != nil {
		keys = h.loadCache(key...)
	}
	keys = util.RemoveOneKey(keys, localKey)

	//h.cacheMap.Del(localKey)
	//设置过期时间，不直接删除
	if h.cache() {
		Del(h.getCacheKey(localKey))
	}

	if h.longevity() {
		if localCacheData, ok := h.cacheMap.Get(localKey); ok {
			h.del(localKey)
			localCacheData.update()
		}
	} else {
		h.cacheMap.Del(localKey)
	}
	success = true
	h.groupAutoCacheManager.Set(keys, mKey)
	return
}

func (h *hashAutoCacheManager[Val]) Reset() IAutoCacheService[string, Val] {
	return h.autoCacheManager.Reset()
}

func (h *hashAutoCacheManager[Val]) GetAll(key ...string) (val []Val, err error) {
	mKey := h.image.HashCachePkKey(key...)
	var keys []string
	if keys, err = h.groupAutoCacheManager.Get(mKey); err != nil {
		keys = h.loadCache(key...)
		if len(keys) == 0 {
			err = tgf.DBEmpty
			h.groupAutoCacheManager.Set(make([]string, 0), mKey)
		} else {
			err = nil
		}
	}
	//
	val = make([]Val, 0, len(keys))
	for _, k := range keys {
		if v, ok, cd := h.get(k); ok && !cd.checkState(data_del) {
			val = append(val, v)
		}
	}
	return
}

func newCacheData[Val any](data Val, second int64) *cacheData[Val] {
	res := &cacheData[Val]{}
	res.data = data
	if second > 0 {
		res.clearTime = time.Now().Unix() + second
	}
	return res
}

func (c *cacheData[Val]) checkTimeOut(now int64) bool {
	var ()
	return c.clearTime != 0 && now > c.clearTime
}

// state 标志位的原子操作：用 Or / And 避免读-改-写的窗口。
// 注意 Go 1.19 才加入 `atomic.Uint32.Or/And`，当前工具链是 1.24.7 所以可用。
func (c *cacheData[Val]) del(second int64) {
	c.clearTime = time.Now().Unix() + second
	c.state.Or(data_del)
	c.state.Or(data_update) // 等价于原先 del 里 c.update() 的语义
}

func (c *cacheData[Val]) update() {
	c.state.Or(data_update)
}

func (c *cacheData[Val]) checkState(state uint32) bool {
	return c.state.Load()&state == state
}

func (c *cacheData[Val]) removeState(state uint32) {
	c.state.And(^state)
}

func (c *cacheData[Val]) getData(second int64) Val {
	var ()
	if second > 0 {
		c.clearTime = time.Now().Unix() + second
	}
	return c.data
}

func (a *autoCacheManager[Key, Val]) TryGet(key ...Key) (val Val, err error) {
	var suc bool
	var cd *cacheData[Val]
	localKey := a.getLocalKey(key...)
	//先从本地缓存获取
	if val, suc, cd = a.get(localKey); suc {
		if cd.checkState(data_del) {
			return val, errors.New("data not found in cache")
		}
		return
	} else {
		err = tgf.LocalEmpty
	}
	return
}

func (a *autoCacheManager[Key, Val]) Get(key ...Key) (val Val, err error) {
	var suc bool
	var cd *cacheData[Val]
	localKey := a.getLocalKey(key...)
	//先从本地缓存获取
	if a.mem() {
		if val, suc, cd = a.get(localKey); suc {
			if cd.checkState(data_del) {
				return val, errors.New("data not found in cache")
			}
			return
		} else {
			err = tgf.LocalEmpty
		}
	}
	v, e, _ := a.sf.Do("Get:"+localKey, func() (interface{}, error) {
		//从cache缓存中获取
		if a.cache() {
			if val, suc = Get[Val](a.getCacheKey(localKey)); suc {
				a.set(localKey, val)
				err = nil
				return val, nil
			} else {
				err = tgf.RedisEmpty
			}
		}

		//从db获取
		if a.longevity() {
			d := make([]any, len(key), len(key))
			for i, k := range key {
				d[i] = k
			}
			val, err = a.sb.queryOne(d...)
			if err == nil {
				a.set(localKey, val)
				Set(a.getCacheKey(localKey), val, a.cacheTimeOut())
			} else {
				err = tgf.DBEmpty
			}
			return val, err
		}
		return val, err
	})
	val = v.(Val)
	err = e
	return val, err
}

func (a *autoCacheManager[Key, Val]) Set(val Val, key ...Key) (success bool) {
	localKey := a.getLocalKey(key...)
	cd := a.set(localKey, val)
	cd.removeState(data_del)
	if a.cache() {
		Set(a.getCacheKey(localKey), val, a.cacheTimeOut())
	}
	if a.longevity() {
		cd.update()
	}
	success = true
	return
}

func (a *autoCacheManager[Key, Val]) Range(f func(Key, Val) bool) {
	a.cacheMap.Range(func(key string, value *cacheData[Val]) bool {
		k, _ := util.StrToAny[Key](key)
		return f(k, value.data)
	})
}

// Push
//
//	@Description: 数据变更后,可以调用该接口进行数据的更新,cache缓存会实时更新,longevity缓存会异步更新,如果调用了remove，就不能嗲用push，因为push会重置remove的状态
//	@receiver this
//	@param key
func (a *autoCacheManager[Key, Val]) Push(key ...Key) {
	var ()
	localKey := a.getLocalKey(key...)
	if localCacheData, ok := a.cacheMap.Get(localKey); ok {
		localCacheData.removeState(data_del)
		if a.cache() {
			Set(a.getCacheKey(localKey), localCacheData.data, a.cacheTimeOut())
		}
		if a.longevity() {
			localCacheData.update()
		}
	}
}

func (a *autoCacheManager[Key, Val]) Remove(key ...Key) (success bool) {
	localKey := a.getLocalKey(key...)
	if a.longevity() {
		if localCacheData, ok := a.cacheMap.Get(localKey); ok {
			a.del(localKey)
			localCacheData.update()
		}
	} else {
		a.cacheMap.Del(localKey)
	}

	//设置过期时间，不直接删除
	if a.cache() {
		Del(a.getCacheKey(localKey))
	}
	//延迟删除本地数据
	success = true
	return
}

func (a *autoCacheManager[Key, Val]) Reset() IAutoCacheService[Key, Val] {
	a.Destroy()
	return a.builder.New()
}

func (a *autoCacheManager[Key, Val]) Destroy() {
	var ()
	a.toLongevity()
}

func (a *autoCacheManager[Key, Val]) getLocalKey(key ...Key) (ck string) {
	var (
		size = len(key)
	)
	if size > 1 {
		l := make([]string, size)
		for i, k := range key {
			v, _ := util.AnyToStr(k)
			l[i] = v
		}
		ck = strings.Join(l, ":")
	} else {
		ck, _ = util.AnyToStr(key[0])
	}
	return
}

func (a *autoCacheManager[Key, Val]) get(key string) (val Val, ok bool, cd *cacheData[Val]) {
	var ()
	if data, suc := a.cacheMap.Get(key); suc {
		return data.getData(a.memTimeOutSecond()), true, data
	}
	return
}

func (a *autoCacheManager[Key, Val]) set(key string, val Val) *cacheData[Val] {
	var (
		cacheData = newCacheData[Val](val, a.memTimeOutSecond())
	)
	a.cacheMap.Set(key, cacheData)
	return cacheData
}

func (a *autoCacheManager[Key, Val]) autoClear() {
	var ()
	defer func() {
		if err := recover(); err != nil {
			buf := make([]byte, 1024)
			buf = buf[:runtime.Stack(buf, true)]
			log.ErrorTag("cache", "autoClear error:%v,%v", string(buf), err)
			return
		}
	}()
	now := time.Now().Unix()
	//初始化1/5的容量
	removeKeys := make([]string, 0, a.cacheMap.Len()/5)
	a.cacheMap.Range(func(k string, c *cacheData[Val]) bool {
		if c.checkTimeOut(now) {
			removeKeys = append(removeKeys, k)
		}
		return true
	})
	//
	cp := a.clearPlugins != nil && len(a.clearPlugins) > 0
	for _, key := range removeKeys {
		if cp {
			for _, plugin := range a.clearPlugins {
				plugin.PreClear(key)
			}
		}
		a.cacheMap.Del(key)
		if cp {
			for _, plugin := range a.clearPlugins {
				plugin.PostClear(key)
			}
		}
	}
	log.DebugTag("cache", "remove timeout keys len: %v", len(removeKeys))
}

func (a *autoCacheManager[Key, Val]) getCacheKey(key string) string {
	var ()
	return a.builder.keyFun + ":" + key
}

func (a *autoCacheManager[Key, Val]) toLongevity() {
	var ()
	defer func() {
		if err := recover(); err != nil {
			buf := make([]byte, 1024)
			buf = buf[:runtime.Stack(buf, true)]
			log.ErrorTag("cache", "toLongevity error:%v,%v", string(buf), err)
			return
		}
	}()
	if !a.longevity() {
		return
	}
	// 本轮抢不到锁，说明有另一轮正在执行。直接返回，脏标志保留在内存里，下一轮 timer 会再来。
	if !a.longevityLock.TryLock() {
		log.WarnTag("orm", "toLongevity skipped: another flush in progress, table=%s", a.sb.tableName)
		return
	}
	defer a.longevityLock.Unlock()

	start := time.Now()
	groupSize := a.builder.longevityGroupSize
	if groupSize <= 0 {
		groupSize = defaultUpdateGroupSize
	}

	// Phase 1: 收集脏数据。每个 batch 同时记录 values（用于 SQL）与对应 cacheData 指针
	// （用于事务成功后再清标志——失败时必须保留 data_update，交给下一轮补偿）。
	type pendingBatch struct {
		values []any
		dirty  []*cacheData[Val]
	}
	var batches []pendingBatch
	var current pendingBatch
	a.cacheMap.Range(func(s string, c *cacheData[Val]) bool {
		if !c.checkState(data_update) {
			return true
		}
		current.values = append(current.values, a.sb.toValueSql(c.getData(0))...)
		current.dirty = append(current.dirty, c)
		if len(current.dirty) >= groupSize {
			batches = append(batches, current)
			current = pendingBatch{}
		}
		return true
	})
	if len(current.dirty) > 0 {
		batches = append(batches, current)
	}

	if len(batches) == 0 {
		return
	}

	// Phase 2: 逐批落库。成功才清 data_update；失败打 ERROR 日志并保留脏标志，
	// 下一轮 timer 会重新捡起来重试。这样任何单批失败都不会导致丢数据。
	successCount, failCount := 0, 0
	for _, b := range batches {
		if err := a.flushBatch(b.values, len(b.dirty)); err != nil {
			log.ErrorTag("orm",
				"longevity batch failed, keeping dirty flag for next round, table=%s size=%d err=%v",
				a.sb.tableName, len(b.dirty), err)
			failCount += len(b.dirty)
			continue
		}
		for _, c := range b.dirty {
			c.removeState(data_update)
		}
		successCount += len(b.dirty)
	}

	mill := time.Since(start).Milliseconds()
	log.DebugTag("orm",
		"execute table name [%s] longevity logic, success=%d fail=%d consume=%dms",
		a.sb.tableName, successCount, failCount, mill)
}

func (a *autoCacheManager[Key, Val]) longevityInterval() time.Duration {
	var ()
	if a.builder.longevityInterval == 0 {
		log.DebugTag("orm", "load default timer interval 5 second")
		a.builder.longevityInterval = time.Second * 5
	}
	return a.builder.longevityInterval
}
func (a *autoCacheManager[Key, Val]) del(key string) bool {
	if val, ok := a.cacheMap.Get(key); ok {
		val.del(int64(a.longevityInterval() * 5))
		return true
	}
	return false
}
func (a *autoCacheManager[Key, Val]) mem() bool {
	var ()
	return a.builder.mem
}

func (a *autoCacheManager[Key, Val]) memTimeOutSecond() int64 {
	var ()
	return a.builder.memTimeOutSecond
}

func (a *autoCacheManager[Key, Val]) cache() bool {
	var ()
	return a.builder.cache
}

func (a *autoCacheManager[Key, Val]) longevity() bool {
	var ()
	return a.builder.longevity
}

func (a *autoCacheManager[Key, Val]) cacheTimeOut() time.Duration {
	var ()
	return a.builder.cacheTimeOut
}

func (a *autoCacheManager[Key, Val]) initField(rf reflect.Type, pkFields, pkListFields, fieldName, tableFieldNum []string) (newPkFields, newPkListFields, newFieldName, newTableFieldNum []string) {
	// 使用参数初始化新的切片
	newPkFields = append([]string{}, pkFields...)
	newPkListFields = append([]string{}, pkListFields...)
	newFieldName = append([]string{}, fieldName...)
	newTableFieldNum = append([]string{}, tableFieldNum...)

	for i := 0; i < rf.NumField(); i++ {
		field := rf.Field(i)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			p, pl, f, t := a.initField(field.Type, []string{}, []string{}, []string{}, []string{})
			newPkFields = append(newPkFields, p...)
			newPkListFields = append(newPkListFields, pl...)
			newFieldName = append(newFieldName, f...)
			newTableFieldNum = append(newTableFieldNum, t...)
			continue
		}
		orm := ""
		name := ConvertCamelToSnake(field.Name)
		isTableField := true
		if field.Tag != "" {
			orm = field.Tag.Get("orm")
			data := strings.Split(orm, ";")
			for _, t := range data {
				switch t {
				case ignore:
					isTableField = false
				case pk:
					newPkFields = append(newPkFields, name+" = ?")
				case list:
					newPkListFields = append(newPkListFields, name+" = ?")
				}
			}
		}
		if isTableField {
			newFieldName = append(newFieldName, name)
			newTableFieldNum = append(newTableFieldNum, field.Name)
			if name == StateName {
				a.sb.hasState = true
			}
		}
		log.DebugTag("omr", "结构化日志打印 structName=%v field=%v tag=%v", rf.Name(), field.Name, orm)
	}

	return
}

func (a *autoCacheManager[Key, Val]) InitStruct() {
	var ()
	a.cacheMap = hashmap.New[string, *cacheData[Val]]()
	a.clearPlugins = a.builder.plugins
	a.longevityLock = &sync.Mutex{}
	a.sb = &sqlBuilder[Val]{}
	a.sf = &singleflight.Group{}
	a.flushBatch = a.sb.flushBatch
	var k Val
	v := reflect.ValueOf(k)
	if (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) && v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	//
	switch v.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.String:
	case reflect.Struct:
		log.WarnTag("orm", "自定义数据管理警告 建议使用指针类型做为值类型 否则可能会发生一些数据上的错乱")
	}

	//开启自动清除过期数据
	if a.builder.autoClear {
		a.clearTimer = time.NewTimer(time.Minute * 10)
		//a.clearTimer = time.NewTicker(time.Second)
		util.Go(func() {
			counter := 0
			for {
				select {
				case <-a.clearTimer.C:
					counter++
					a.autoClear()
					if counter > 1e10 {
						counter = 0
					}
					a.clearTimer.Reset(time.Minute * 10)
				}
			}
		})
	}

	//	//INSERT INTO table_name (id, name, value) VALUES (1, 'John', 10), (2, 'Peter', 20), (3, 'Mary', 30)
	//	//ON DUPLICATE KEY UPDATE name=VALUES(name), value=VALUES(value);
	////初始化db结构
	if a.builder.longevity {
		rf := v.Type().Elem()
		getTableNameValue := v.MethodByName("GetTableName")

		if getTableNameValue.Kind() == reflect.Invalid {
			log.WarnTag("omr", "value %v need implements db.IMode", rf.Name())
			return
		}
		pkFields := make([]string, 0)
		pkListFields := make([]string, 0)
		fieldName := make([]string, 0)
		tableFieldNum := make([]string, 0)
		pkFields, pkListFields, fieldName, tableFieldNum = a.initField(rf, pkFields, pkListFields, fieldName, tableFieldNum)
		pkSql := strings.Join(pkFields, " and ")
		pkListSql := strings.Join(pkListFields, " and ")
		queryListSql := strings.Join(fieldName, ",")

		res := getTableNameValue.Call(make([]reflect.Value, 0))
		a.sb.tableName = res[0].Interface().(string)
		a.sb.tableField = queryListSql
		a.sb.tableFieldName = fieldName
		a.sb.pkSql = pkSql
		a.sb.pkListSql = pkListSql
		a.sb.modelFieldName = tableFieldNum
		a.sb.initStruct()

		a.longevityTimer = time.NewTimer(a.longevityInterval())
		util.Go(func() {
			counter := 0
			for {
				select {
				case <-a.longevityTimer.C:
					counter++
					a.toLongevity()
					if counter > 1e10 {
						counter = 0
					}
					a.longevityTimer.Reset(a.longevityInterval())
				}
			}
		})
	}
	tgf.AddDestroyHandler(a)
}

func ConvertCamelToSnake(s string) string {
	result := ""
	for i, v := range s {
		if v >= 'A' && v <= 'Z' {
			if i != 0 {
				result += "_"
			}
			result += string(v + 32)
		} else {
			result += string(v)
		}
	}
	return "`" + result + "`"
}

type sqlBuilder[Val any] struct {
	//table
	modelFieldName []string
	tableName      string
	tableField     string
	tableFieldName []string
	//sql
	pkSql              string
	pkListSql          string
	querySql           string
	queryListSql       string
	updateStartSql     string
	updateEndSql       string
	updateValueBaseSql string
	updateAsSql        string
	//mongo
	collection string
	//chan
	updateChan chan Val
	//
	hasState bool
}

func (s *sqlBuilder[Val]) initStruct() {
	var ()
	s.querySql = "select " + s.tableField + " from " + s.tableName + " where " + s.pkSql
	s.queryListSql = "select " + s.tableField + " from " + s.tableName + " where " + s.pkListSql

	if s.hasState {
		s.querySql += " and state = 1"
		s.queryListSql += " and state = 1"
	}

	log.DebugTag("omr", "table=%v query sql=%v", s.tableName, s.querySql)
	if s.pkListSql != "" {
		log.DebugTag("omr", "table=%v query list sql=%v", s.tableName, s.queryListSql)
	}
	//
	s.updateStartSql = "INSERT INTO " + s.tableName + "(" + s.tableField + ")  VALUES "
	appendSql := make([]string, len(s.tableFieldName), len(s.tableFieldName))
	for i, s := range s.tableFieldName {
		appendSql[i] = fmt.Sprintf("%v=v.%v", s, s)
	}
	s.updateEndSql = "ON DUPLICATE KEY UPDATE " + strings.Join(appendSql, ",")
	//拼接默认单个值的字符串
	fieldCount := len(s.modelFieldName)
	updateValueBaseSql := "("
	for i := 0; i < fieldCount; i++ {
		updateValueBaseSql += "?,"
	}
	updateValueBaseSql = updateValueBaseSql[:len(updateValueBaseSql)-1]
	updateValueBaseSql += ") "
	s.updateValueBaseSql = updateValueBaseSql
	s.updateAsSql = "AS v "
	log.DebugTag("omr", "table=%v update sql=%v", s.tableName, s.updateStartSql+s.updateValueBaseSql+s.updateAsSql+s.updateEndSql)
	s.updateChan = make(chan Val)
}

func (s *sqlBuilder[Val]) toValueSql(val Val) (q []any) {
	var ()
	ref := reflect.ValueOf(val).Elem()
	sliceSize := len(s.modelFieldName)
	q = make([]any, sliceSize)
	for i, index := range s.modelFieldName {
		q[i] = ref.FieldByName(index).Interface()
	}
	return
}

func (s *sqlBuilder[Val]) initField(rf reflect.Type, pkFields, fieldName []string, tableFieldNum []int) (newPkFields, newFieldName []string, newTableFieldNum []int) {
	// 使用参数初始化新的切片
	newPkFields = append([]string{}, pkFields...)
	newFieldName = append([]string{}, fieldName...)
	newTableFieldNum = append([]int{}, tableFieldNum...)

	for i := 0; i < rf.NumField(); i++ {
		field := rf.Field(i)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			p, f, t := s.initField(field.Type, newPkFields, newFieldName, newTableFieldNum)
			newPkFields = append([]string{}, p...)
			newFieldName = append([]string{}, f...)
			newTableFieldNum = append([]int{}, t...)
			continue
		}
		orm := ""
		name := ConvertCamelToSnake(field.Name)
		isTableField := true
		if field.Tag != "" {
			orm = field.Tag.Get("orm")
			data := strings.Split(orm, ";")
			for _, t := range data {
				switch t {
				case ignore:
					isTableField = false
				case pk:
					newPkFields = append(newPkFields, name+" = ?")
				}
			}
		}
		if isTableField {
			newFieldName = append(newFieldName, name)
			newTableFieldNum = append(newTableFieldNum, i)
			if name == StateName {
				s.hasState = true
			}
		}
		log.DebugTag("omr", "结构化日志打印 structName=%v field=%v tag=%v", rf.Name(), field.Name, orm)
	}
	return
}

func (s *sqlBuilder[Val]) queryOne(args ...any) (val Val, err error) {
	var (
		start = time.Now()
	)

	if s.querySql == "" {
		log.WarnTag("orm", "query script is empty")
		return
	}
	conn := dbService.getConnection()
	defer conn.Close()

	stmt, err := conn.PrepareContext(context.Background(), s.querySql)
	defer stmt.Close()
	if err != nil {
		log.WarnTag("orm", "query script=%v error=%v", s.querySql, err)
		return
	}
	rows, err := stmt.Query(args...)
	defer rows.Close()
	if err != nil {
		log.WarnTag("orm", "query params=%v  error=%v", args, err)
		return
	}
	ex := time.Since(start)
	log.DebugTag("orm", "query=%v params=%v time=%v/ms", s.querySql, args, ex)
	if rows.Next() {
		v := reflect.ValueOf(val)
		if v.IsNil() {
			v = reflect.New(v.Type().Elem())
		}
		resPointer := make([]any, 0, len(s.modelFieldName))
		for _, name := range s.modelFieldName {
			field := v.Elem().FieldByName(name)
			param := reflect.New(field.Type()).Interface()
			resPointer = append(resPointer, param)
		}
		err = rows.Scan(resPointer...)
		if err != nil {
			log.WarnTag("orm", "query rows error: %v", err)
			return
		}
		for i, name := range s.modelFieldName {
			f := v.Elem().FieldByName(name)
			f.Set(reflect.ValueOf(resPointer[i]).Elem())
		}
		return v.Interface().(Val), err
	}
	return val, tgf.DBEmpty
}

func (s *sqlBuilder[Val]) queryList(args ...any) (values []Val, err error) {
	var (
		start = time.Now()
	)

	if s.queryListSql == "" {
		log.WarnTag("orm", "query script is empty")
		return
	}
	conn := dbService.getConnection()
	defer conn.Close()

	stmt, err := conn.PrepareContext(context.Background(), s.queryListSql)
	if err != nil {
		log.WarnTag("orm", "query script=%v error=%v", s.queryListSql, err)
		return
	}
	defer stmt.Close()
	rows, err := stmt.Query(args...)
	if err != nil {
		log.WarnTag("orm", "query params=%v  error=%v", args, err)
		return
	}
	defer rows.Close()
	ex := time.Since(start)
	log.DebugTag("orm", "query=%v params=%v time=%v/ms", s.queryListSql, args, ex)
	var val Val
	values = make([]Val, 0)
	for rows.Next() {
		v := reflect.ValueOf(val)
		if v.IsNil() {
			v = reflect.New(v.Type().Elem())
		}
		resPointer := make([]any, 0, len(s.modelFieldName))
		for _, name := range s.modelFieldName {
			field := v.Elem().FieldByName(name)
			param := reflect.New(field.Type()).Interface()
			resPointer = append(resPointer, param)
		}
		err = rows.Scan(resPointer...)
		if err != nil {
			log.WarnTag("orm", "query rows error: %v", err)
			return
		}
		for i, name := range s.modelFieldName {
			f := v.Elem().FieldByName(name)
			f.Set(reflect.ValueOf(resPointer[i]).Elem())
		}
		values = append(values, v.Interface().(Val))
	}
	return
}

// flushBatch 执行单批 upsert，成功返回 nil，失败（含内部重试耗尽）返回最后一次错误。
// 调用方负责把大批数据切分为合适的 group 大小再传进来。
func (s *sqlBuilder[Val]) flushBatch(values []any, count int) error {
	if count <= 0 {
		return nil
	}
	fieldCount := len(s.modelFieldName)
	if fieldCount == 0 {
		return errors.New("sqlBuilder: modelFieldName is empty, initStruct not called?")
	}
	if len(values) != count*fieldCount {
		return fmt.Errorf(
			"sqlBuilder: values size mismatch, got=%d expect=%d (count=%d fieldCount=%d)",
			len(values), count*fieldCount, count, fieldCount)
	}

	insertValues := make([]string, count)
	for x := 0; x < count; x++ {
		insertValues[x] = s.updateValueBaseSql
	}
	updateSql := s.updateStartSql + strings.Join(insertValues, ",") + s.updateAsSql + s.updateEndSql

	traceId := util.GenerateSnowflakeId()
	if logScript, err := sonic.MarshalString(values); err == nil {
		log.DB(traceId, s.tableName, logScript, int32(count))
	}

	maxAttempts := defaultLongevityRetry
	var lastErr error
	backoff := defaultLongevityRetryBackoff
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		start := time.Now()
		err := s.execBatchOnce(updateSql, values)
		if err == nil {
			log.InfoTag("orm",
				"traceId=%s update=%v time=%d/ms valueSize=%v attempt=%d",
				traceId, s.tableName, time.Since(start).Milliseconds(), count, attempt)
			return nil
		}
		lastErr = err
		log.WarnTag("orm",
			"traceId=%s update table=%v attempt=%d/%d failed err=%v",
			traceId, s.tableName, attempt, maxAttempts, err)
		if attempt < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return lastErr
}

// execBatchOnce 执行一次 upsert 事务，不做重试。
func (s *sqlBuilder[Val]) execBatchOnce(updateSql string, values []any) (err error) {
	conn := dbService.getConnection()
	defer conn.Close()

	tx, err := conn.BeginTx(context.Background(), &sql.TxOptions{
		Isolation: sql.LevelReadUncommitted,
		ReadOnly:  false,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(context.Background(), updateSql)
	if err != nil {
		return err
	}
	defer stmt.Close()

	if _, err = stmt.Exec(values...); err != nil {
		return err
	}
	return tx.Commit()
}
