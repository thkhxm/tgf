//go:build integration && race
// +build integration,race

package rpc

// E5：-race 构建标记。
// rpcx fork 的 client.input() 自研并发化存在已知数据竞态（V3 审计 P1，
// rpcx/client/client.go:644/716，排期 F1 fork 治理修复）。集成 e2e 在
// -race 构建下跳过"真实 rpcx Call"小节避免已知问题误报；CI 的集成轨
// 不带 -race，完整链路在 CI 始终全量执行。
const itRaceEnabled = true
