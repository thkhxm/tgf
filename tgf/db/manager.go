package db

import (
	"errors"
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"golang.org/x/net/context"
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
//2023/2/27
//***************************************************

type cacheKey interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr | ~float32 | ~float64 | ~string
}

type cacheData[Val any] struct {
	data      Val
	clearTime int64
}

type autoCacheManager[Key cacheKey, Val any] struct {
	builder *AutoCacheBuilder[Key, Val]
	//
	cacheMap *hashmap.Map[string, *cacheData[Val]]
	//
	clearTimer *time.Ticker
	//
	sb *sqlBuilder[Val]
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
	this.cacheMap.Set(localKey, newCacheData[Val](val, this.memTimeOutSecond()))
	if this.cache() {
		Set(this.getCacheKey(localKey), val, this.cacheTimeOut())
	}
	success = true
	return
}

func (this *autoCacheManager[Key, Val]) Push(key ...Key) {
	var ()
	if !this.cache() {
		return
	}
	localKey := this.getLocalKey(key...)
	if val, err := this.Get(key...); err == nil {
		Set(this.getCacheKey(localKey), val, this.cacheTimeOut())
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
	//TODO 缓存之前的列表
	this.toLongevity()
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
		//fieldStr := make([]string, rf.NumField(), rf.NumField())
		pkFields := make([]string, 0)
		fieldName := make([]string, 0)
		tableFieldNum := make([]int, 0)
		for i := 0; i < rf.NumField(); i++ {
			field := rf.Field(i)
			orm := ""
			name := field.Name
			name = strings.ToLower(name[0:1]) + name[1:]
			isTableField := field.Name != name
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
		querySql := "select " + queryListSql + " from " + res[0].Interface().(string) + " where " + pkSql
		this.sb.querySql = querySql
		this.sb.tableFieldNum = tableFieldNum
		log.DebugTag("omr", "%v query model : %v", rf.Name(), querySql)
	}
}

type sqlBuilder[Val any] struct {
	querySql      string
	tableFieldNum []int
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
	log.DebugTag("orm", "query model=%v params=%v time=%v/ms", this.querySql, args, ex)
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
