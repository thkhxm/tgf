package util

import (
	"golang.org/x/exp/rand"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description 权重通用工具,非线程安全,需要注意
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
}

type IWeightBuilder[T any] interface {
	AddWeight(weightRatio, amount int32, data T) IWeightBuilder[T]
	Build() IWeight[T]
	Seed(seed uint64) IWeightBuilder[T]
}

type weight[T any] struct {
	ratio  int32
	data   T
	amount int32
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
	return w.amount
}

func (w *weight[T]) Ratio() int32 {
	if w.amount == 0 {
		return 0
	}
	return w.ratio
}

func (w *weight[T]) BaseRatio() int32 {
	return w.ratio
}

func (w *weight[T]) Hit() (IWeightItem[T], bool) {
	//如果数量小于0,则表示该权重无限制
	if w.amount < 0 {
		return w, false
	}

	if w.amount > 0 {
		w.amount--
		//避免因为并发导致的负数
		if w.amount < 0 {
			w.amount = 0
		}
		return w, w.amount == 0
	}
	return nil, false
}

func (w *weightOperation[T]) Roll() (res IWeightData[T]) {
	r := w.ran.Int31n(w.totalRatio)
	for _, wei := range w.weights {
		if r < wei.Ratio() {
			if _, done := wei.Hit(); done {
				w.totalRatio -= wei.BaseRatio()
			}
			return wei
		}
		r -= wei.Ratio()
	}
	return
}

func (w *weightOperation[T]) AllItem() []IWeightData[T] {
	res := make([]IWeightData[T], 0, len(w.weights))
	for _, wei := range w.weights {
		res = append(res, wei)
	}
	return res
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
	w.weights = append(w.weights, &weight[T]{ratio: weightRatio, amount: amount, data: data})
	return w
}

func NewWeightBuilder[T any]() IWeightBuilder[T] {
	return &weightBuilder[T]{weights: make([]IWeightItem[T], 0)}
}
