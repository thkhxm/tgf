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

type cacheData[Val any] struct {
	data      Val
	clearTime int64
	state     uint8
}

var defaultUpdateGroupSize = 500

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
	pk := h.image.HashCachePkKey(key...)
	defer func() {
		if err := recover(); err != nil {
			log.ErrorTag("cache", "load cache error:%v", err)
			return
		}
		if keys != nil {
			h.groupAutoCacheManager.Set(keys, pk)
		}
	}()

	h.sf.Do(pk, func() (interface{}, error) {
		//从cache缓存中获取
		if h.cache() {
			//根据主键Key组合成redis的Key,获取hash数据
			if val, suc := GetMap[string, Val](h.getCacheKey(pk)); suc {
				i := 0
				keys = make([]string, len(val))
				for _, v := range val {
					//根据主键Key和hashKey组成唯一的cacheKey
					lk := h.getLocalKey(pk, v.HashCacheFieldByVal())
					h.set(lk, v)
					//将该cacheKey放入slice中,用于管理用户的key列表
					keys[i] = lk
					i++
				}
				return keys, nil
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
				keys = make([]string, len(val))
				for i, v := range val {
					lk := h.getLocalKey(pk, v.HashCacheFieldByVal())
					h.set(lk, v)
					PutMap(h.getCacheKey(pk), v.HashCacheFieldByVal(), v, h.cacheTimeOut())
					keys[i] = lk
				}
				return keys, nil
			}
		}
		return keys, errors.New("not found in cache")
	})
	return
}

func (h *hashAutoCacheManager[Val]) Get(key ...string) (val Val, err error) {
	pk := h.image.HashCachePkKey(key...)
	d, e, _ := h.sf.Do(pk, func() (interface{}, error) {
		//是否首次加载，如果是
		if _, has := h.groupAutoCacheManager.Get(pk); has != nil {
			h.loadCache(key...)
		}
		//
		localKey := h.getLocalKey(pk, h.image.HashCacheFieldByKeys(key...))
		//从本地缓存获取
		if v, has, cd := h.get(localKey); has && !cd.checkState(data_del) {
			return v, nil
		}
		return nil, errors.New("not found in cache")
	})
	if d == nil {
		return val, e
	}
	return d.(Val), e
}

func (h *hashAutoCacheManager[Val]) Set(val Val, key ...string) (success bool) {
	pk := h.image.HashCachePkKey(key...)
	var keys []string
	var has error
	//是否首次加载
	if keys, has = h.groupAutoCacheManager.Get(pk); has != nil {
		keys = h.loadCache(key...)
	}
	//
	fieldKey := val.HashCacheFieldByVal()
	localKey := h.getLocalKey(pk, fieldKey)
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
		h.groupAutoCacheManager.Set(keys, pk)
	}
	//写入redis缓存
	if h.cache() {
		PutMap(h.getCacheKey(pk), fieldKey, val, h.cacheTimeOut())
	}

	//写入db
	if h.longevity() {
		cd.update()
	}

	return true
}

func (h *hashAutoCacheManager[Val]) Push(key ...string) {
	pk := h.image.HashCachePkKey(key...)
	fieldKey := h.image.HashCacheFieldByKeys(key...)
	localKey := h.getLocalKey(pk, fieldKey)

	if localCacheData, ok := h.cacheMap.Get(localKey); ok {
		localCacheData.removeState(data_del)
		if h.cache() {
			PutMap(h.getCacheKey(pk), fieldKey, localCacheData.data, h.cacheTimeOut())
		}
		if h.longevity() {
			localCacheData.update()
		}
	}
}

func (h *hashAutoCacheManager[Val]) Remove(key ...string) (success bool) {
	pk := h.image.HashCachePkKey(key...)
	fieldKey := h.image.HashCacheFieldByKeys(key...)
	localKey := h.getLocalKey(pk, fieldKey)
	var keys []string
	var has error
	//是否首次加载
	if keys, has = h.groupAutoCacheManager.Get(pk); has != nil {
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
	h.groupAutoCacheManager.Set(keys, pk)
	return
}

func (h *hashAutoCacheManager[Val]) Reset() IAutoCacheService[string, Val] {
	return h.autoCacheManager.Reset()
}

func (h *hashAutoCacheManager[Val]) GetAll(key ...string) (val []Val, err error) {
	pk := h.image.HashCachePkKey(key...)
	var keys []string
	if keys, err = h.groupAutoCacheManager.Get(pk); err != nil {
		keys = h.loadCache(key...)
		if len(keys) == 0 {
			err = tgf.DBEmpty
			h.groupAutoCacheManager.Set(make([]string, 0), pk)
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

func (c *cacheData[Val]) del(second int64) {
	c.clearTime = time.Now().Unix() + second
	c.state = c.state | data_del
	c.update()
}

func (c *cacheData[Val]) update() {
	c.state = c.state | data_update
}

func (c *cacheData[Val]) checkState(state uint8) bool {
	return c.state&state == state
}

func (c *cacheData[Val]) removeState(state uint8) {
	c.state = c.state &^ state
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
	a.sf.Do(pk, func() (interface{}, error) {
		//从cache缓存中获取
		if a.cache() {
			if val, suc = Get[Val](a.getCacheKey(localKey)); suc {
				a.set(localKey, val)
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

	return val, err
}

func (a *autoCacheManager[Key, Val]) Set(val Val, key ...Key) (success bool) {
	localKey := a.getLocalKey(key...)
	cd := a.set(localKey, val)
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
	util.Go(func() {
		a.Destroy()
	})
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
	if a.longevityLock.TryLock() {
		defer a.longevityLock.Unlock()
		size := a.cacheMap.Len()
		valueStr := make([]any, 0, size*len(a.sb.modelFieldName))
		count := 0
		a.cacheMap.Range(func(s string, c *cacheData[Val]) bool {
			if c.checkState(data_update) {
				valueStr = append(valueStr, a.sb.toValueSql(c.getData(0))...)
				count++
				c.removeState(data_update)
			}
			return true
		})
		//util.Go(func() {
		a.sb.updateOrCreate(valueStr, count)
		//})
		log.DebugTag("orm:trace", "execute longevity logic , longevity size=%v", count)
	}
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

func (s *sqlBuilder[Val]) updateOrCreate(values []any, count int) {
	//按批次发送所有更新脚本
	group := count/defaultUpdateGroupSize + 1
	fieldCount := len(s.modelFieldName)
	baseValueSize := len(values)
	for i := 0; i < group; i++ {
		s.update(i, fieldCount, baseValueSize, values)
	}
}

func (s *sqlBuilder[Val]) update(i, fieldCount, baseValueSize int, values []any) {
	//初始化更新脚本
	start := time.Now()
	startIndex := i * defaultUpdateGroupSize * fieldCount
	if startIndex >= baseValueSize {
		return
	}
	endIndex := startIndex + defaultUpdateGroupSize*fieldCount
	if endIndex > baseValueSize {
		endIndex = baseValueSize
	}
	valueSize := (endIndex - startIndex) / fieldCount
	insertValues := make([]string, valueSize)
	for x := 0; x < valueSize; x++ {
		insertValues[x] = s.updateValueBaseSql
	}
	insertValuesSql := strings.Join(insertValues, ",")
	updateSql := s.updateStartSql + insertValuesSql + s.updateAsSql + s.updateEndSql
	//执行脚本
	conn := dbService.getConnection()
	defer conn.Close()
	tx, err := conn.BeginTx(context.Background(), &sql.TxOptions{
		Isolation: sql.LevelReadUncommitted,
		ReadOnly:  false,
	})
	logScript, err := sonic.MarshalString(values[startIndex:endIndex])
	traceId := util.GenerateSnowflakeId()
	if err == nil {
		log.DB(traceId, s.tableName, logScript, int32(valueSize))
	}
	if err != nil {
		return
	}
	stmt, err := tx.PrepareContext(context.Background(), updateSql)
	defer stmt.Close()
	if err != nil {
		log.WarnTag("orm", "traceId=%s,update script=%v params=%v error=%v", traceId, updateSql, values[startIndex:endIndex], err)
		tx.Rollback()
		return
	}
	_, err = stmt.Exec(values[startIndex:endIndex]...)
	if err != nil {
		log.WarnTag("orm", "traceId=%s update run script=%v params=%v error=%v", traceId, updateSql, values[startIndex:endIndex], err)
		tx.Rollback()
		return
	}
	tx.Commit()
	ex := time.Since(start)
	log.InfoTag("orm", "traceId=%s update=%v time=%v/ms group=%d valueSize=%v", traceId, s.tableName, ex.Milliseconds(), i, valueSize)
}
