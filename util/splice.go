package util

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/1/9
//***************************************************

// SliceDeduplication
// @Description: 去重
// @param s
// @return []S
func SliceDeduplication[S comparable](s []S) []S {
	m := make(map[S]bool)
	for _, v := range s {
		m[v] = true
	}
	s = make([]S, 0, len(m))
	for k, _ := range m {
		s = append(s, k)
	}
	return s
}
