package component

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cornelk/hashmap"
	"github.com/fsnotify/fsnotify"
	"github.com/thkhxm/tgf/v2/db"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/util"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/10
//***************************************************

// ---- C5 · 游戏配置热更 ----
//
// v2 的 C5 档给 game_config 引入了三件东西：
//
//  1. ReloadGameConf() — 手动触发一次全量重载。从磁盘再读一遍 confPath，
//     覆盖 contextDataManager 的字节缓存；然后对所有通过 GetGameConf /
//     RangeGameConf 注册过的类型，调用它们对应的 loader 把 cacheDataManager
//     原子替换成新 map。
//
//  2. OnReload(fn) — 订阅一个重载完成回调。每次 ReloadGameConf 成功后
//     按注册顺序调用，适合业务侧做缓存失效/日志/metrics 上报。
//
//  3. StartConfigWatcher(ctx) — 基于 fsnotify 监听 confPath 目录，
//     Create/Write/Rename 事件 debounce 200ms 后触发 ReloadGameConf。
//     生产环境可选开，开发环境默认可开用来"改 JSON 就生效"。
//
// 原子性说明：
//   - cacheDataManager 底层是 hashmap.Map，.Set(cc, key) 对 key 的 value
//     是 pointer 替换，lock-free。读者要么看到旧 map 要么看到新 map。
//   - 已经通过 getCacheGameConfData 拿到旧 *hashmap.Map 的 goroutine 会
//     继续在旧 map 上做 Range / Get，不会"看到半个新配置"。这就是"双 buffer
//     + 原子指针切换"的最小实现——我们没有额外分配 buffer，复用 hashmap.Map
//     本身的指针替换语义。
//
// 失败策略：
//   - 单个 JSON 文件读失败 → 只跳过这个文件，其它继续。日志 WARN。
//   - 单个类型 loader 反序列化失败 → cacheDataManager 里的旧 map 保持原样，
//     业务拿到的仍是上一版。日志 WARN。
//   - ReloadGameConf 整体只有"全成功"或"部分成功"，不抛 panic。

var confPath = "./conf/json"

var (
	contextDataManager db.IAutoCacheService[string, []byte]
	cacheDataManager   db.IAutoCacheService[string, *hashmap.Map[string, interface{}]]
)

var newLock = &sync.Mutex{}

// ---- C5 新增的全局状态 ----

// typeLoaders 收集所有已经被 GetGameConf / RangeGameConf / GetAllGameConf
// 触发过首次加载的类型。key 是类型名，value 是一个 reloader 闭包——闭包
// 捕获了泛型类型参数 Val，因此每个具体类型都有自己独立的 loader。
//
// ReloadGameConf 会遍历这张表调用每个 loader。未被访问过的类型不会被
// 预先注册——这是刻意的惰性策略：热更只重载"业务已经在用的"配置，未使用
// 的配置跟原先一样在首次访问时走 LoadGameConf 懒加载。
var typeLoaders sync.Map // map[string]func()

// reloadMu 序列化 ReloadGameConf 的并发调用。热更本身不应该并发——watcher
// 事件通过 debounce 去重后也只会一次一次调 Reload。但手动触发 + watcher
// 触发可能撞一起，用一把锁吃掉。
var reloadMu sync.Mutex

// reloadHooksMu 保护 reloadHooks slice。写入极少（OnReload 通常只在启动
// 阶段注册几个），读取在 ReloadGameConf 完成时触发，用 RWMutex 够了。
var (
	reloadHooksMu sync.RWMutex
	reloadHooks   []func()
)

// watcherStop 是 watcher goroutine 的退出信号。atomic.Pointer 保证
// Start/Stop 的并发安全。Start 幂等：多次调用只启动一个 watcher。
var watcherStop atomic.Pointer[chan struct{}]

// ---- Query API（保持 v1 兼容） ----

func GetGameConf[Val any](id string) (res Val, h bool) {
	t := util.ReflectType[Val]()
	key := t.Name()
	data := getCacheGameConfData[Val](key)
	if data == nil {
		return
	}
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
	if data == nil {
		return
	}
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
	if data == nil {
		return
	}
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
	if data == nil {
		return
	}
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
	// C5：首次加载时注册 loader，供后续 ReloadGameConf 使用。
	registerLoader[Val](key)
	return LoadGameConf[Val]()
}

// registerLoader 把当前类型的 reloader 闭包登记到 typeLoaders。
// 多次登记同一类型只保留第一次（闭包等价，无需覆盖）。
func registerLoader[Val any](key string) {
	if _, ok := typeLoaders.Load(key); ok {
		return
	}
	typeLoaders.Store(key, func() {
		// LoadGameConf 内部会做 cacheDataManager.Set 原子替换。
		// 失败时返回的 map 可能为空，这是可接受的——反序列化失败应当日志告警，
		// 不应 panic 影响其它类型。
		_ = LoadGameConf[Val]()
	})
}

// LoadGameConf [Val any]
//
//	 泛型传入自动生成的配置即可
//		@Description: 预加载 / 热更重建
func LoadGameConf[Val any]() *hashmap.Map[string, interface{}] {
	t := util.ReflectType[Val]()
	key := t.Name()
	context, _ := contextDataManager.Get(key)
	data, err := util.StrToAny[[]Val](util.ConvertStringByByteSlice(context))
	if err != nil {
		log.WarnTag("GameConf", "reload game conf 反序列化失败 name=%v err=%v", key, err)
		// 失败时不要覆盖 cacheDataManager 的老数据——让业务继续看到上一版。
		if old, _ := cacheDataManager.Get(key); old != nil {
			return old
		}
	}
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

// ---- 配置目录与初始化 ----

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
	loadFilesIntoContext()
}

// loadFilesIntoContext 把 confPath 目录下的 JSON 全部读进 contextDataManager。
// InitGameConfToMem 和 ReloadGameConf 共用这段逻辑——区别只在于 Init 会重建
// 两个 cacheService，Reload 复用已有 cacheService。
func loadFilesIntoContext() {
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
			file.Close()
			continue
		}
		_, fileName := filepath.Split(filePath)
		contextDataManager.Set(context, strings.Split(fileName, `.`)[0]+"Conf")
		file.Close()
	}
}

// ---- C5 · Reload API ----

// ReloadGameConf 手动触发一次全量重载。线程安全。
//
// 流程：
//  1. 重读 confPath 下的 JSON 文件到 contextDataManager
//  2. 调用每个已注册类型的 loader 重建 cacheDataManager
//  3. 触发 OnReload 钩子
//
// 返回 error 为 nil 时表示流程执行完，但**个别类型**可能因为反序列化
// 失败保留了旧数据——这种情况只写日志，不抛错，业务 caller 感知
// 不到具体哪个类型出问题。需要强一致的场景应当用 panic + 回滚策略
// 自行包装。
func ReloadGameConf() error {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	if contextDataManager == nil || cacheDataManager == nil {
		log.WarnTag("GameConf", "ReloadGameConf 被调用但 InitGameConfToMem 还未执行")
		return nil
	}

	start := time.Now()
	loadFilesIntoContext()

	// 遍历所有已注册的 loader 重建 cacheDataManager
	count := 0
	typeLoaders.Range(func(_, v any) bool {
		if fn, ok := v.(func()); ok && fn != nil {
			fn()
			count++
		}
		return true
	})

	log.InfoTag("GameConf", "ReloadGameConf 完成 类型数=%v 耗时=%v", count, time.Since(start))

	// 调用 OnReload 钩子（读锁保护）
	reloadHooksMu.RLock()
	hooks := make([]func(), len(reloadHooks))
	copy(hooks, reloadHooks)
	reloadHooksMu.RUnlock()
	for i, h := range hooks {
		func(idx int, fn func()) {
			defer func() {
				if r := recover(); r != nil {
					log.WarnTag("GameConf", "OnReload 回调 %d panic: %v", idx, r)
				}
			}()
			fn()
		}(i, h)
	}
	return nil
}

// OnReload 注册一个重载完成回调。按注册顺序调用，panic 被内部 recover。
// 不支持取消订阅（简化实现——业务方需要切换行为的话自己在闭包里加开关）。
func OnReload(fn func()) {
	if fn == nil {
		return
	}
	reloadHooksMu.Lock()
	reloadHooks = append(reloadHooks, fn)
	reloadHooksMu.Unlock()
}

// ---- C5 · 文件监听 ----

// configWatcherDebounce 是 fsnotify 事件合并窗口。快速连续写入（编辑器
// 保存触发多个 Write 事件）在窗口内只触发一次 Reload。
var configWatcherDebounce = 200 * time.Millisecond

// StartConfigWatcher 启动 fsnotify 监听。幂等——多次调用只启动一个。
// 返回 error 表示创建 watcher 失败；创建成功后事件处理在后台 goroutine 里。
//
// 调用方通过 StopConfigWatcher 停止监听，或者进程退出时自然释放。
func StartConfigWatcher() error {
	// 幂等检查
	if cur := watcherStop.Load(); cur != nil {
		return nil
	}
	if confPath == "" {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(confPath); err != nil {
		_ = w.Close()
		return err
	}
	stop := make(chan struct{})
	if !watcherStop.CompareAndSwap(nil, &stop) {
		// 已经有另一个 watcher 在跑
		_ = w.Close()
		return nil
	}
	log.InfoTag("GameConf", "配置监听启动 path=%v", confPath)

	go runConfigWatcher(w, stop)
	return nil
}

// StopConfigWatcher 停止监听。幂等。
func StopConfigWatcher() {
	cur := watcherStop.Swap(nil)
	if cur != nil {
		close(*cur)
	}
}

func runConfigWatcher(w *fsnotify.Watcher, stop chan struct{}) {
	defer w.Close()
	var (
		debounceTimer *time.Timer
		triggerCh     = make(chan struct{}, 1)
	)
	// schedule 在收到事件时启动 / 重置 debounce 定时器。
	schedule := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(configWatcherDebounce, func() {
			select {
			case triggerCh <- struct{}{}:
			default:
			}
		})
	}

	for {
		select {
		case <-stop:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			log.InfoTag("GameConf", "配置监听退出")
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// 只关心 Create / Write / Rename，Chmod / Remove 忽略。
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			// 只关心 .json 文件
			if filepath.Ext(ev.Name) != ".json" {
				continue
			}
			schedule()
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.WarnTag("GameConf", "配置监听错误 err=%v", err)
		case <-triggerCh:
			if err := ReloadGameConf(); err != nil {
				log.WarnTag("GameConf", "热更失败 err=%v", err)
			}
		}
	}
}

// ---- 测试辅助（仅限同包测试使用） ----

// resetGameConfForTest 清理所有包级状态，让测试之间隔离。
// 生产代码不应调用。
func resetGameConfForTest() {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	typeLoaders.Range(func(k, _ any) bool {
		typeLoaders.Delete(k)
		return true
	})
	reloadHooksMu.Lock()
	reloadHooks = nil
	reloadHooksMu.Unlock()
	StopConfigWatcher()
	contextDataManager = nil
	cacheDataManager = nil
}
