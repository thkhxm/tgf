//go:build !race

package rpc

// raceDetectorEnabled 标记当前测试二进制是否带 -race 编译。
// 用途见 race_on_test.go。
const raceDetectorEnabled = false
