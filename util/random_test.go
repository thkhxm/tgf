package util_test

import (
	"github.com/thkhxm/tgf/util"
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

func TestRandNumberReturnsDifferentNumbers(t *testing.T) {
	min := 1
	max := 100
	result1 := util.RandNumber[int](min, max)
	result2 := util.RandNumber[int](min, max)
	if result1 == result2 {
		t.Errorf("Expected different numbers, got %d and %d", result1, result2)
	}
}
