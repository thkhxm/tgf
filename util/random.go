package util

import "math/rand"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/12/11
//***************************************************

type number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

func RandNumber[T number](min, max T) T {
	if min == max {
		return min
	}
	return T(rand.Int63n(int64(max)-int64(min)) + int64(min))
}
