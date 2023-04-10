package db

import (
	"errors"
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"golang.org/x/net/context"
	"reflect"
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

type cacheData[Val any] struct {
	data      Val
	clearTime int64
	update    bool
}

var defaultUpdateGroupSize = 500

type autoCacheManager[Key cacheKey, Val any] struct {
	builder *AutoCacheBuilder[Key, Val]
	//
	cacheMap *hashmap.Map[string, *cacheData[Val]]
	//
	clearTimer *time.Ticker
	//
	sb *sqlBuilder[Val]
	//
	longevityLock *sync.Mutex
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

func (this *autoCacheManager[Key, Val]) Get(key ...Key) (val Val, err error) {
	var suc bool
	localKey := this.getLocalKey(key...)
	//先从本地缓存获取
	if this.mem() {
		if val, suc = this.get(localKey); suc {
			return
		}
	}
	//从cache缓存中获取
	if this.cache() {
		if val, suc = Get[Val](this.getCacheKey(localKey)); suc {
			this.set(localKey, val)
			return
		}
	}
	//从db获取
	if this.longevity() {
		a := make([]any, len(key), len(key))
		for i, k := range key {
			a[i] = k
		}
		val, err = this.sb.queryOne(a...)
		if err == nil {
			this.set(localKey, val)
			Set(this.getCacheKey(localKey), val, this.cacheTimeOut())
		}
	}
	return
}

func (this *autoCacheManager[Key, Val]) Set(val Val, key ...Key) (success bool) {
	localKey := this.getLocalKey(key...)
	cd := newCacheData[Val](val, this.memTimeOutSecond())
	this.cacheMap.Set(localKey, cd)
	if this.cache() {
		Set(this.getCacheKey(localKey), val, this.cacheTimeOut())
	}
	if this.longevity() {
		cd.update = true
	}
	success = true
	return
}

// Push
//
//	@Description: 数据变更后,可以调用该接口进行数据的更新,cache缓存会实时更新,longevity缓存会异步更新
//	@receiver this
//	@param key
func (this *autoCacheManager[Key, Val]) Push(key ...Key) {
	var ()
	localKey := this.getLocalKey(key...)
	if this.cache() {
		if val, err := this.Get(key...); err == nil {
			Set(this.getCacheKey(localKey), val, this.cacheTimeOut())
		}
	}

	if this.longevity() {
		if localCacheData, ok := this.cacheMap.Get(localKey); ok {
			localCacheData.update = true
		}
	}

}

func (this *autoCacheManager[Key, Val]) Remove(key ...Key) (success bool) {
	localKey := this.getLocalKey(key...)
	this.cacheMap.Del(localKey)
	//设置过期时间，不直接删除
	if this.cache() {
		Del(this.getCacheKey(localKey))
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
	this.toLongevity()
}

func (this *autoCacheManager[Key, Val]) getLocalKey(key ...Key) (ck string) {
	var (
		size = len(key)
	)
	if size > 1 {
		l := make([]string, size, size)
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

func (this *autoCacheManager[Key, Val]) get(key string) (Val, bool) {
	var ()

	if data, suc := this.cacheMap.Get(key); suc {
		return data.getData(this.memTimeOutSecond()), true
	}
	return *new(Val), false
}

func (this *autoCacheManager[Key, Val]) set(key string, val Val) {
	var ()
	this.cacheMap.Set(key, newCacheData[Val](val, this.memTimeOutSecond()))
}

func (this *autoCacheManager[Key, Val]) autoClear() {
	var ()
	now := time.Now().Unix()
	//初始化1/5的容量
	removeKeys := make([]string, 0, this.cacheMap.Len()/5)
	this.cacheMap.Range(func(k string, c *cacheData[Val]) bool {
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

func (this *autoCacheManager[Key, Val]) getCacheKey(key string) string {
	var ()
	return this.builder.keyFun + ":" + key
}

func (this *autoCacheManager[Key, Val]) toLongevity() {
	var ()
	if !this.longevity() {
		return
	}
	if this.longevityLock.TryLock() {
		defer this.longevityLock.Unlock()
		size := this.cacheMap.Len()
		valueStr := make([]any, 0, size*len(this.sb.tableFieldNum))
		count := 0
		this.cacheMap.Range(func(s string, c *cacheData[Val]) bool {
			if c.update {
				valueStr = append(valueStr, this.sb.toValueSql(c.getData(0))...)
				count++
				c.update = false
			}
			return true
		})
		util.Go(func() {
			this.sb.updateOrCreate(valueStr, count)
		})
		log.DebugTag("orm", "execute longevity logic , longevity size=%v", count)
	}
}

func (this *autoCacheManager[Key, Val]) longevityInterval() time.Duration {
	var ()
	if this.builder.longevityInterval == 0 {
		log.DebugTag("orm", "load default timer interval 5 second")
		return time.Second * 5
	}
	return this.builder.longevityInterval
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
	this.cacheMap = hashmap.New[string, *cacheData[Val]]()
	this.longevityLock = &sync.Mutex{}
	this.sb = &sqlBuilder[Val]{}
	var k Val
	v := reflect.ValueOf(k)
	if v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	//
	switch v.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.String:
	case reflect.Struct:
		log.WarnTag("orm", "自定义数据管理警告 建议使用指针类型做为值类型 否则可能会发生一些数据上的错乱")
	}

	//开启自动清除过期数据
	if this.builder.autoClear {
		this.clearTimer = time.NewTicker(this.longevityInterval())
		util.Go(func() {
			counter := 0
			for {
				select {
				case <-this.clearTimer.C:
					counter++
					this.toLongevity()
					if counter%10 == 0 {
						this.autoClear()
					}
				}
			}
		})
	}

	//	//INSERT INTO table_name (id, name, value) VALUES (1, 'John', 10), (2, 'Peter', 20), (3, 'Mary', 30)
	//	//ON DUPLICATE KEY UPDATE name=VALUES(name), value=VALUES(value);
	////初始化db结构
	if this.builder.longevity {
		rf := v.Type().Elem()
		getTableNameValue := v.MethodByName("GetTableName")

		if getTableNameValue.Kind() == reflect.Invalid {
			log.WarnTag("omr", "value %v need implements db.IMode", rf.Name())
			return
		}
		pkFields := make([]string, 0)
		fieldName := make([]string, 0)
		tableFieldNum := make([]int, 0)
		for i := 0; i < rf.NumField(); i++ {
			field := rf.Field(i)
			if !field.IsExported() {
				continue
			}
			orm := ""
			name := field.Name
			name = strings.ToLower(name[0:1]) + name[1:]
			isTableField := true
			if field.Tag != "" {
				orm = field.Tag.Get("orm")
				data := strings.Split(orm, ";")
				for _, t := range data {
					switch t {
					case ignore:
						isTableField = false
					case pk:
						pkFields = append(pkFields, name+" = ?")
					}
				}
			}
			if isTableField {
				fieldName = append(fieldName, name)
				tableFieldNum = append(tableFieldNum, i)
			}
			log.DebugTag("omr", "结构化日志打印 structName=%v field=%v tag=%v", rf.Name(), field.Name, orm)
		}
		pkSql := strings.Join(pkFields, " and ")
		queryListSql := strings.Join(fieldName, ",")

		res := getTableNameValue.Call(make([]reflect.Value, 0))
		this.sb.tableName = res[0].Interface().(string)
		this.sb.tableField = queryListSql
		this.sb.tableFieldName = fieldName
		this.sb.pkSql = pkSql
		this.sb.tableFieldNum = tableFieldNum
		this.sb.initStruct()
	}
}

type sqlBuilder[Val any] struct {
	//table
	tableFieldNum  []int
	tableName      string
	tableField     string
	tableFieldName []string
	//sql
	pkSql              string
	querySql           string
	updateStartSql     string
	updateEndSql       string
	updateValueBaseSql string
	updateAsSql        string
	//chan
	updateChan chan Val
}

func (this *sqlBuilder[Val]) initStruct() {
	var ()
	this.querySql = "select " + this.tableField + " from " + this.tableName + " where " + this.pkSql
	log.DebugTag("omr", "table=%v query sql=%v", this.tableName, this.querySql)
	//
	this.updateStartSql = "INSERT INTO " + this.tableName + "(" + this.tableField + ")  VALUES "
	appendSql := make([]string, len(this.tableFieldName), len(this.tableFieldName))
	for i, s := range this.tableFieldName {
		appendSql[i] = fmt.Sprintf("%v=v.%v", s, s)
	}
	this.updateEndSql = "ON DUPLICATE KEY UPDATE " + strings.Join(appendSql, ",")
	//拼接默认单个值的字符串
	fieldCount := len(this.tableFieldNum)
	updateValueBaseSql := "("
	for i := 0; i < fieldCount; i++ {
		updateValueBaseSql += "?,"
	}
	updateValueBaseSql = updateValueBaseSql[:len(updateValueBaseSql)-1]
	updateValueBaseSql += ") "
	this.updateValueBaseSql = updateValueBaseSql
	this.updateAsSql = "AS v "
	log.DebugTag("omr", "table=%v update sql=%v", this.tableName, this.updateStartSql+this.updateValueBaseSql+this.updateAsSql+this.updateEndSql)
	this.updateChan = make(chan Val)
}

func (this *sqlBuilder[Val]) toValueSql(val Val) (s []any) {
	var ()
	ref := reflect.ValueOf(val).Elem()
	sliceSize := len(this.tableFieldNum)
	s = make([]any, sliceSize, sliceSize)
	for i, index := range this.tableFieldNum {
		s[i] = ref.Field(index).Interface()
	}
	return
}

func (this *sqlBuilder[Val]) queryOne(args ...any) (val Val, err error) {
	var (
		start = time.Now()
	)

	if this.querySql == "" {
		log.WarnTag("orm", "query script is empty")
		return
	}
	stmt, err := dbService.getConnection().PrepareContext(context.Background(), this.querySql)
	if err != nil {
		log.WarnTag("orm", "query script=%v error=%v", this.querySql, err)
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
	log.DebugTag("orm", "query=%v params=%v time=%v/ms", this.querySql, args, ex)
	if rows.Next() {
		v := reflect.ValueOf(val)
		if v.IsNil() {
			v = reflect.New(v.Type().Elem())
		}
		resPointer := make([]any, 0, len(this.tableFieldNum))
		for _, num := range this.tableFieldNum {
			field := v.Elem().Field(num)
			param := reflect.New(field.Type()).Interface()
			resPointer = append(resPointer, param)
		}
		err = rows.Scan(resPointer...)
		if err != nil {
			log.WarnTag("orm", "query rows error: %v", err)
			return
		}
		for i, num := range this.tableFieldNum {
			f := v.Elem().Field(num)
			f.Set(reflect.ValueOf(resPointer[i]).Elem())
		}
		return v.Interface().(Val), err
	}
	return val, errors.New("data is empty")
}

func (this *sqlBuilder[Val]) updateOrCreate(values []any, count int) {

	//按批次发送所有更新脚本
	group := count/defaultUpdateGroupSize + 1
	fieldCount := len(this.tableFieldNum)
	baseValueSize := len(values)
	for i := 0; i < group; i++ {
		//初始化更新脚本
		start := time.Now()
		startIndex := i * defaultUpdateGroupSize * fieldCount
		if startIndex >= baseValueSize {
			break
		}
		endIndex := startIndex + defaultUpdateGroupSize*fieldCount
		if endIndex > baseValueSize {
			endIndex = baseValueSize
		}
		valueSize := (endIndex - startIndex) / fieldCount
		insertValues := make([]string, valueSize, valueSize)
		for x := 0; x < valueSize; x++ {
			insertValues[x] = this.updateValueBaseSql
		}
		insertValuesSql := strings.Join(insertValues, ",")
		updateSql := this.updateStartSql + insertValuesSql + this.updateAsSql + this.updateEndSql
		//执行脚本
		stmt, err := dbService.getConnection().PrepareContext(context.Background(), updateSql)
		if err != nil {
			log.WarnTag("orm", "update script=%v params=%v error=%v", updateSql, values[startIndex:endIndex], err)
			continue
		}
		_, err = stmt.Exec(values[startIndex:endIndex]...)
		if err != nil {
			log.WarnTag("orm", "update run script=%v params=%v error=%v", updateSql, values[startIndex:endIndex], err)
			continue
		}
		stmt.Close()
		ex := time.Since(start)
		log.DebugTag("orm", "update=%v params=%v time=%v/ms", updateSql, values[startIndex:endIndex], ex)
	}

}
