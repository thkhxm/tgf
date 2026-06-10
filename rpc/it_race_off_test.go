//go:build integration && !race
// +build integration,!race

package rpc

// E5：非 -race 构建标记（说明见 it_race_on_test.go）。
const itRaceEnabled = false
