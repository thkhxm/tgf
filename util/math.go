package util

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/12/17
//***************************************************

// Min Min[T number]
//
//	@Description: 获取最小值
//	@param a
//	@param b
//	@return T
func Min[T number](a, b T) T {
	if a < b {
		return a
	}
	return b
}

// Max Max[T number]
//
//	@Description: 获取最大值
//	@param a
//	@param b
//	@return T
func Max[T number](a, b T) T {
	if a < b {
		return b
	}
	return a
}
