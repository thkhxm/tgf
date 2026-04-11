package util

import (
	"sync/atomic"
	"time"

	"golang.org/x/exp/rand"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description 权重通用工具。
//
// A5: 单个 weight item 的 Hit 已经是 atomic（基于 sync/atomic.Int32 的 CAS 循环），
// 不再有"并发导致数量负数"的风险。但外层 weightOperation（Roll/OnlyRoll/TotalRatio 等）
// 对 totalRatio 的读写仍非原子，并发 Roll 会看到 totalRatio 的中间态，导致随机分布
// 略有偏差。真正高并发场景下请给 weightOperation 外部加锁。C4 会考虑完整重构。
//2023/11/29
//***************************************************

type IWeightData[T any] interface {
	Data() T
	Amount() int32
	Ratio() int32
	BaseRatio() int32
}

type IWeightItem[T any] interface {
	IWeightData[T]
	Hit() (IWeightItem[T], bool)
}

type IWeight[T any] interface {
	// Roll
	// @Description: 根据权重随机出一个数据,并且减少物品的数量
	// @return T
	//
	Roll() (res IWeightData[T])
	// AllItem
	// @Description: 这里不会进行切片的拷贝,所以在使用的时候需要注意
	// @return IWeightItem[T]
	//
	AllItem() []IWeightData[T]

	TotalRatio() int32
	BaseRatio() int32
	BaseAmount() int32
	Len() int

	OnlyRoll() (res IWeightData[T])
	UpdateItemStock(data IWeightData[T])
}

type IWeightBuilder[T any] interface {
	AddWeight(weightRatio, amount int32, data T) IWeightBuilder[T]
	Build() IWeight[T]
	Seed(seed uint64) IWeightBuilder[T]
}

type weight[T any] struct {
	ratio int32
	data  T
	// A5: amount 改为 atomic.Int32，Hit 走 CAS 循环保证并发正确性。
	// 负值语义：< 0 表示无限库存（Hit 永远返回 true 但不减）。
	amount atomic.Int32
}

type weightOperation[T any] struct {
	weights    []IWeightItem[T]
	totalRatio int32
	baseRatio  int32
	baseAmount int32
	//
	ran *rand.Rand
}

type weightBuilder[T any] struct {
	weights []IWeightItem[T]
	seed    uint64
}

//-----------------------------------

func (w *weight[T]) Data() T {
	return w.data
}

func (w *weight[T]) Amount() int32 {
	return w.amount.Load()
}

func (w *weight[T]) Ratio() int32 {
	if w.amount.Load() == 0 {
		return 0
	}
	return w.ratio
}

func (w *weight[T]) BaseRatio() int32 {
	return w.ratio
}

// Hit 原子递减 amount。
//   - amount < 0：无限库存，返回 (w, false)，不减
//   - amount == 0：已耗尽，返回 (nil, false)
//   - amount > 0：CAS 循环递减 1；返回 (w, 递减后是否恰好耗尽)
//
// A5 修复：原实现用非原子 `w.amount--`，并发 Hit 会出现负值（作者已加一个
// `if w.amount < 0 { w.amount = 0 }` 的 hack 但仍然丢 Hit 计数）。现在 CAS
// 循环保证每次 Hit 恰好消耗 1 份库存，不丢不多。
func (w *weight[T]) Hit() (IWeightItem[T], bool) {
	for {
		cur := w.amount.Load()
		if cur < 0 {
			return w, false
		}
		if cur == 0 {
			return nil, false
		}
		if w.amount.CompareAndSwap(cur, cur-1) {
			return w, cur-1 == 0
		}
		// CAS 失败说明有并发修改，重新读取再试
	}
}

func (w *weightOperation[T]) Roll() (res IWeightData[T]) {
	res = w.OnlyRoll()
	w.UpdateItemStock(res)
	return
}

// OnlyRoll
// @Description: 只命中，但是不对数量和权重做变更
// @receiver w
// @return res
func (w *weightOperation[T]) OnlyRoll() (res IWeightData[T]) {
	if w.totalRatio <= 0 {
		return
	}
	r := w.ran.Int31n(w.totalRatio)
	for _, wei := range w.weights {
		if r < wei.Ratio() {
			return wei
		}
		r -= wei.Ratio()
	}
	return

}

// UpdateItemStock
// @Description: 变更权重和数量
// @receiver w
// @param data
func (w *weightOperation[T]) UpdateItemStock(data IWeightData[T]) {
	item := data.(IWeightItem[T])
	if _, done := item.Hit(); done {
		w.totalRatio -= item.BaseRatio()
	}
}

func (w *weightOperation[T]) AllItem() []IWeightData[T] {
	res := make([]IWeightData[T], 0, len(w.weights))
	for _, wei := range w.weights {
		res = append(res, wei)
	}
	return res
}

func (w *weightOperation[T]) Len() int {
	return len(w.weights)
}
func (w *weightOperation[T]) TotalRatio() int32 {
	return w.totalRatio
}

func (w *weightOperation[T]) BaseRatio() int32 {
	return w.baseRatio
}

func (w *weightOperation[T]) BaseAmount() int32 {
	return w.baseAmount
}

func (w *weightBuilder[T]) Seed(seed uint64) IWeightBuilder[T] {
	w.seed = seed
	return w
}

func (w *weightBuilder[T]) Build() IWeight[T] {
	operation := &weightOperation[T]{weights: w.weights}
	for _, wei := range w.weights {
		operation.totalRatio += wei.Ratio()
		if wei.Amount() > 0 {
			operation.baseAmount += wei.Amount()
		}
	}
	operation.baseRatio = operation.totalRatio
	if w.seed == 0 {
		w.seed = uint64(time.Now().UnixMilli())
	}
	//自定义随机数种子
	operation.ran = rand.New(rand.NewSource(w.seed))

	return operation
}
func (w *weightBuilder[T]) AddWeight(weightRatio, amount int32, data T) IWeightBuilder[T] {
	if weightRatio <= 0 {
		return w
	}
	// A5: atomic.Int32 不能直接在 struct literal 里初始化，改为构造后 Store。
	item := &weight[T]{ratio: weightRatio, data: data}
	item.amount.Store(amount)
	w.weights = append(w.weights, item)
	return w
}

func NewWeightBuilder[T any]() IWeightBuilder[T] {
	return &weightBuilder[T]{weights: make([]IWeightItem[T], 0)}
}
