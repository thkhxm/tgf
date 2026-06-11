package util_test

import (
	"github.com/thkhxm/tgf/v2/util"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/12/11
//***************************************************

func BenchmarkRandNumberReturnsNumberWithinRange(b *testing.B) {
	min := 1
	max := 10
	for i := 0; i < b.N; i++ {
		result := util.RandNumber[int](min, max)
		if result < min || result > max {
			b.Errorf("Expected number between %d and %d, got %d", min, max, result)
		}
	}
}

func TestRandNumberReturnsMinWhenMinEqualsMax(t *testing.T) {
	result := util.RandNumber[int64](5, 5)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}

func TestRandNumberReturnsNumberWithinRange(t *testing.T) {
	min := 1
	max := 10
	result := util.RandNumber[int](min, max)
	if result < min || result > max {
		t.Errorf("Expected number between %d and %d, got %d", min, max, result)
	}
}

// TestRandNumberReturnsDifferentNumbers 验证 RandNumber 不是一个常量。
// 原实现只取两次然后断言两次不同——1~100 范围 1% 概率撞相同值会误报。
// 改为取 50 次后断言至少出现过 2 个不同的值，撞相同概率 ~1e-100 可以忽略。
func TestRandNumberReturnsDifferentNumbers(t *testing.T) {
	min := 1
	max := 100
	seen := make(map[int]struct{}, 2)
	for i := 0; i < 50; i++ {
		seen[util.RandNumber[int](min, max)] = struct{}{}
		if len(seen) >= 2 {
			return
		}
	}
	t.Errorf("50 次采样全部相同, seen=%v", seen)
}
