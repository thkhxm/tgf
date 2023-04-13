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

func GetGameConf[Val any](id string) (res Val) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	tmp, _ := data.Get(id)
	res = tmp.([]Val)[0]
	return
}

func GetGameConfBySlice[Val any](id string) (res []Val) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	tmp, _ := data.Get(id)
	res = tmp.([]Val)
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
	data, _ := cacheDataManager.Get(key)
	if data == nil {
		newLock.Lock()
		defer newLock.Unlock()
		data = LoadGameConf[Val]()
	}
	return data
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
