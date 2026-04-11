package component

import (
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/10
//***************************************************

var confPath = "./conf/json"

var contextDataManager db.IAutoCacheService[string, []byte]
var cacheDataManager db.IAutoCacheService[string, *hashmap.Map[string, interface{}]]

var newLock = &sync.Mutex{}

func GetGameConf[Val any](id string) (res Val, h bool) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	if tmp, x := data.Get(id); x {
		res = tmp.([]Val)[0]
		h = true
	}
	return
}

func GetGameConfBySlice[Val any](id string) (res []Val, h bool) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	if tmp, x := data.Get(id); x {
		res = tmp.([]Val)
		h = true
	}
	return
}

// GetAllGameConf [Val any]
// @Description: 不建议使用这个函数除非特殊需求，建议使用RangeGameConf
func GetAllGameConf[Val any]() (res []Val) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	tmp := make([]Val, 0, data.Len())
	data.Range(func(s string, i interface{}) bool {
		for _, v := range i.([]Val) {
			tmp = append(tmp, v)
		}
		return true
	})
	res = tmp
	return
}

// RangeGameConf [Val any]
// @Description:
// @param f
func RangeGameConf[Val any](f func(s string, i Val) bool) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	ff := func(a string, b interface{}) bool {
		for _, i := range b.([]Val) {
			if !f(a, i) {
				return false
			}
		}
		return true
	}
	data.Range(ff)
}

func getCacheGameConfData[Val any](key string) *hashmap.Map[string, interface{}] {
	// Fast path：无锁读。
	if data, _ := cacheDataManager.Get(key); data != nil {
		return data
	}
	// Slow path：取锁后 double-check，避免首次并发触发多次 LoadGameConf。
	// B6 修复：原实现虽然有 newLock.Lock 但是锁内没有 re-check，第一个 goroutine
	// 拿锁调完 LoadGameConf 后释放，第二个 goroutine 拿到锁还会再 Load 一遍。
	// 加上 re-check 之后并发首次访问只触发一次加载。
	newLock.Lock()
	defer newLock.Unlock()
	if data, _ := cacheDataManager.Get(key); data != nil {
		return data
	}
	return LoadGameConf[Val]()
}

// LoadGameConf [Val any]
//
//	 泛型传入自动生成的配置即可
//		@Description: 预加载
func LoadGameConf[Val any]() *hashmap.Map[string, interface{}] {
	t := util.ReflectType[Val]()
	key := t.Name()
	context, _ := contextDataManager.Get(key)
	data, _ := util.StrToAny[[]Val](util.ConvertStringByByteSlice(context))
	cc := hashmap.New[string, interface{}]()
	for _, d := range data {
		rd := reflect.ValueOf(d).Elem()
		id := rd.Field(0)
		uniqueId, _ := util.AnyToStr(id.Interface())
		v, _ := cc.Get(uniqueId)
		if v == nil {
			v = make([]Val, 0)
		}
		v = append(v.([]Val), d)
		cc.Set(uniqueId, v)
	}
	cacheDataManager.Set(cc, key)
	log.DebugTag("GameConf", "load game conf , name=%v", t.Name())
	return cc
}

func WithConfPath(path string) {
	confPath, _ = filepath.Abs(path)
	log.InfoTag("GameConf", "set game json file path=%v", confPath)
}

func InitGameConfToMem() {
	builder := db.NewAutoCacheBuilder[string, []byte]()
	builder.WithMemCache(0)
	contextDataManager = builder.New()
	//
	cacheBuilder := db.NewAutoCacheBuilder[string, *hashmap.Map[string, interface{}]]()
	cacheBuilder.WithMemCache(0)
	cacheDataManager = cacheBuilder.New()
	//
	files := util.GetFileList(confPath, ".json")
	for _, filePath := range files {
		file, err := os.Open(filePath)
		if err != nil {
			log.WarnTag("GameConf", "game json file [%v] open error %v", filePath, err)
			continue
		}
		context, err := io.ReadAll(file)
		if err != nil {
			log.WarnTag("GameConf", "game json file [%v] read error %v", filePath, err)
			continue
		}
		_, fileName := filepath.Split(filePath)
		contextDataManager.Set(context, strings.Split(fileName, `.`)[0]+"Conf")
		file.Close()
	}
}
