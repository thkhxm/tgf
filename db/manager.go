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

// ErrDBFault D5: 持久层真实故障（连接不可用、查询/扫描失败等），
// 与 tgf.DBEmpty（数据确实不存在）严格区分。
// 业务侧用 errors.Is(err, db.ErrDBFault) 判定是否处于"DB 故障"——命中时
// 绝不能把结果当"新数据"处理（例如用默认存档覆盖老档），应当走故障保护分支。
var ErrDBFault = errors.New("db fault")

// FlushResult 一次终末 flush（FlushNow / FlushAll）的执行结果。
type FlushResult struct {
	// Table 该管理器对应的落库表名
	Table string
	// Success 本次成功落库并清除脏标志的条数
	Success int
	// Failed 重试耗尽仍失败的条数（脏标志保留在内存，且已尝试写入补偿队列）
	Failed int
	// Err 失败时的聚合错误（含 panic 转换）；全部成功时为 nil
	Err error
}

// IFlushable D 档停机钩子：把该管理器当前所有脏数据同步落库。
// 所有开启 longevity 的 autoCacheManager / hashAutoCacheManager 都实现该接口，
// 业务持有 IAutoCacheService 时可类型断言到 IFlushable 后调用。
type IFlushable interface {
	// FlushNow 阻塞式终末 flush：等待正在进行的周期 flush 完成（阻塞 Lock，
	// 非 TryLock 尽力而为），随后把全部脏数据分批落库并返回结果。
	FlushNow() FlushResult
}

// flushRegistry 进程内所有 longevity 管理器的注册表，供 FlushAll 在停机时统一收口。
var (
	flushRegistry   []IFlushable
	flushRegistryMu sync.Mutex
)

func registerFlushable(f IFlushable) {
	flushRegistryMu.Lock()
	defer flushRegistryMu.Unlock()
	flushRegistry = append(flushRegistry, f)
}

// FlushAll D 档停机钩子（供 SIGTERM/SIGINT 优雅停机路径调用）：
// 同步把进程内所有 longevity 管理器的脏数据立即落库，逐个返回结果。
//
// 语义保证：
//   - 阻塞等待每个管理器正在进行的周期 flush 完成后再执行终末 flush（非 TryLock 尽力而为）；
//   - 单个管理器 flush 失败（含 panic）不会中断其余管理器，失败信息记录在对应 FlushResult.Err；
//   - 失败 batch 的脏标志保留，并已尝试写入该管理器配置的补偿队列（FailureQueue）。
//
// 调用方（停机流程）应在 drain 完业务流量后、进程退出前调用，并对 Err != nil 的结果打日志告警。
// flush 内部带有限次重试与退避（每批最多 defaultLongevityRetry 次），不会无限阻塞；
// 如需硬性时限，调用方可自行用 goroutine + 超时包裹。
func FlushAll() []FlushResult {
	flushRegistryMu.Lock()
	targets := make([]IFlushable, len(flushRegistry))
	copy(targets, flushRegistry)
	flushRegistryMu.Unlock()

	results := make([]FlushResult, 0, len(targets))
	for _, t := range targets {
		results = append(results, t.FlushNow())
	}
	return results
}

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

// loadCache 加载主键下的全部 hash 数据并返回 cacheKey 列表。
// D5: 返回值增加 err——DB 真实故障（errors.Is(err, ErrDBFault)）时 keys 为 nil
// 且不会把空列表当"已加载"缓存进 group manager，下一次访问会重新尝试加载，
// 避免故障期间数据被误判为"不存在"（进而被默认数据覆盖）。
func (h *hashAutoCacheManager[Val]) loadCache(key ...string) (keys []string, err error) {
	//获取主键key
	localKey := h.image.HashCachePkKey(key...)
	defer func() {
		if r := recover(); r != nil {
			log.ErrorTag("cache", "load cache error:%v", r)
			return
		}
		if keys != nil {
			h.groupAutoCacheManager.Set(keys, localKey)
		}
	}()

	v, err, _ := h.sf.Do("loadCache:"+localKey, func() (interface{}, error) {
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
			val, qerr := h.sb.queryList(d...)
			if qerr != nil {
				// D5: DB 故障 ≠ 无数据。返回 nil keys（不缓存"已加载"状态），
				// 并把原始错误包上 ErrDBFault 向上抛。
				return []string(nil), fmt.Errorf("%w: %w", ErrDBFault, qerr)
			}
			ak := make([]string, len(val))
			for i, v := range val {
				lk := h.getLocalKey(localKey, v.HashCacheFieldByVal())
				h.set(lk, v)
				PutMap(h.getCacheKey(localKey), v.HashCacheFieldByVal(), v, h.cacheTimeOut())
				ak[i] = lk
			}
			return ak, nil
		}
		return make([]string, 0), errors.New("not found in cache")
	})
	keys, _ = v.([]string)
	return
}

func (h *hashAutoCacheManager[Val]) Get(key ...string) (val Val, err error) {
	mKey := h.image.HashCachePkKey(key...)
	//是否首次加载，如果是
	if _, has := h.groupAutoCacheManager.Get(mKey); has != nil {
		if _, lerr := h.loadCache(key...); lerr != nil && errors.Is(lerr, ErrDBFault) {
			// D5: DB 真实故障必须原样向上抛，不能伪装成"数据不存在"。
			return val, lerr
		}
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
		keys, _ = h.loadCache(key...)
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
		keys, _ = h.loadCache(key...)
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
		var lerr error
		keys, lerr = h.loadCache(key...)
		if lerr != nil && errors.Is(lerr, ErrDBFault) {
			// D5: DB 故障时不缓存空列表、不返回 DBEmpty——把故障如实抛给业务。
			return nil, lerr
		}
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
			} else if !errors.Is(err, tgf.DBEmpty) {
				// D5: 区分"DB 故障"与"无数据"。旧实现把查询超时/连接失败等
				// 真实故障一律改写成 DBEmpty，业务会把故障当新玩家并用默认档
				// 覆盖 DB 里的真实存档。现在仅"查无此行"返回 DBEmpty，
				// 真实故障包上 ErrDBFault 原样向上抛。
				err = fmt.Errorf("%w: %w", ErrDBFault, err)
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
	// D3/D5: 终末 flush 改为阻塞可靠版本（FlushNow），不再用 TryLock 尽力而为——
	// 关停瞬间若有周期 flush 正在执行，等它完成后再补一轮终末 flush，
	// 确保最后一个落库窗口的脏数据不随进程退出丢失。
	if res := a.FlushNow(); res.Err != nil {
		log.ErrorTag("orm", "destroy final flush failed table=%s success=%d failed=%d err=%v",
			res.Table, res.Success, res.Failed, res.Err)
	}
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
	successCount, failCount, _ := a.flushDirtyLocked()
	if successCount == 0 && failCount == 0 {
		return
	}

	mill := time.Since(start).Milliseconds()
	log.DebugTag("orm",
		"execute table name [%s] longevity logic, success=%d fail=%d consume=%dms",
		a.sb.tableName, successCount, failCount, mill)
}

// flushDirtyLocked 执行一轮"收集脏数据 → 分批落库 → 成功清脏 / 失败进补偿队列"。
// 调用方必须已持有 longevityLock（toLongevity 用 TryLock，FlushNow 用阻塞 Lock）。
// 返回成功/失败条数与失败 batch 的聚合错误。
func (a *autoCacheManager[Key, Val]) flushDirtyLocked() (successCount, failCount int, err error) {
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
		return 0, 0, nil
	}

	// Phase 2: 逐批落库。成功才清 data_update；失败打 ERROR 日志并保留脏标志，
	// 下一轮 timer 会重新捡起来重试。这样任何单批失败都不会导致丢数据。
	//
	// C4/A1b：如果业务注入了 FailureQueue，失败 batch 也会序列化一份进补偿队列，
	// 业务启动阶段可以 ReplayFailureQueue 重放。脏标志依然保留（队列是补救通道，
	// 不是替代通道），下一轮 timer 还会重试。重试成功时 queue 里的重复条目靠
	// SQL 的 UPSERT 幂等性兜底。
	var errs []error
	failureQueue := a.resolveFailureQueue()
	for _, b := range batches {
		if ferr := a.flushBatch(b.values, len(b.dirty)); ferr != nil {
			log.ErrorTag("orm",
				"longevity batch failed, keeping dirty flag for next round, table=%s size=%d err=%v",
				a.sb.tableName, len(b.dirty), ferr)
			failCount += len(b.dirty)
			errs = append(errs, ferr)
			// 补偿队列降级——即便 enqueue 失败也不影响下一轮 timer 重试。
			if failureQueue != nil {
				payload, perr := encodeFailurePayload(a.sb.tableName, b.values, len(b.dirty))
				if perr != nil {
					log.WarnTag("orm", "longevity failure encode error table=%s err=%v", a.sb.tableName, perr)
				} else if eerr := failureQueue.Enqueue(payload); eerr != nil {
					log.WarnTag("orm", "longevity failure enqueue error table=%s err=%v", a.sb.tableName, eerr)
				}
			}
			continue
		}
		for _, c := range b.dirty {
			c.removeState(data_update)
		}
		successCount += len(b.dirty)
	}
	return successCount, failCount, errors.Join(errs...)
}

// FlushNow D 档停机钩子：阻塞式终末 flush，实现 IFlushable。
// 与周期性 toLongevity 的区别：
//   - 用阻塞 Lock 而非 TryLock——若有周期 flush 正在执行，等它完成后再补一轮，
//     绝不"尽力而为"地放弃；
//   - 返回结构化结果（成功/失败条数 + 聚合错误），调用方（优雅停机流程）可据此告警；
//   - 内部 panic 被转换为 FlushResult.Err，不会击穿停机流程导致后续管理器漏 flush。
func (a *autoCacheManager[Key, Val]) FlushNow() (res FlushResult) {
	if a.sb != nil {
		res.Table = a.sb.tableName
	}
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 1024)
			buf = buf[:runtime.Stack(buf, false)]
			res.Err = fmt.Errorf("db: FlushNow panic: %v\n%s", r, buf)
		}
	}()
	if !a.longevity() {
		return
	}
	a.longevityLock.Lock()
	defer a.longevityLock.Unlock()
	res.Success, res.Failed, res.Err = a.flushDirtyLocked()
	return
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
		// D 档停机钩子：注册进 flushRegistry，停机流程通过 db.FlushAll()
		// 把全部 longevity 管理器的脏数据可靠落库。
		registerFlushable(a)
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
		// D5: 返回真实错误而不是 (零值, nil)——否则调用方会把零值当"查询成功"缓存。
		log.WarnTag("orm", "query script is empty")
		return val, errors.New("orm: query script is empty, initStruct not called?")
	}
	// D5: 连接不可用时返回 error 而不是在 nil conn 上 panic。
	conn, err := getMysqlConn()
	if err != nil {
		log.WarnTag("orm", "query connection unavailable script=%v err=%v", s.querySql, err)
		return val, err
	}
	defer conn.Close()

	// D5: defer 必须放在 err 检查之后——Prepare/Query 失败时 stmt/rows 为 nil，
	// 旧实现在 defer 阶段 nil 解引用 panic（queryList 修了、queryOne 漏了的同源 bug）。
	stmt, err := conn.PrepareContext(context.Background(), s.querySql)
	if err != nil {
		log.WarnTag("orm", "query script=%v error=%v", s.querySql, err)
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
		// D5: 返回真实错误而不是 (nil, nil)——否则调用方会把空结果当"加载成功"缓存。
		log.WarnTag("orm", "query script is empty")
		return nil, errors.New("orm: query list script is empty, initStruct not called?")
	}
	// D5: 连接不可用时返回 error 而不是在 nil conn 上 panic。
	conn, err := getMysqlConn()
	if err != nil {
		log.WarnTag("orm", "query connection unavailable script=%v err=%v", s.queryListSql, err)
		return nil, err
	}
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
	// D5: MySQL 宕机/未初始化时返回 error 走故障路径——flushBatch 的重试与
	// toLongevity 的补偿队列（FailureQueue）由此真正接管，而不是 nil conn panic
	// 穿透 batch 循环、旁路掉 C4 补偿队列。
	conn, err := getMysqlConn()
	if err != nil {
		return err
	}
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
