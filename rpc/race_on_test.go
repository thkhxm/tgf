//go:build race

package rpc

// raceDetectorEnabled 标记当前测试二进制是否带 -race 编译。
//
// 背景：rpcx fork 自研的 client.input() 并发化存在已知 err 字段竞态
// （V3 审计 fork 治理 P1，rpcx/client/client.go:644/716，修复归 F1 档 fork 桶，
// 不在 tgf/rpc 的文件归属内）。任何经真实 rpcx client 的调用在 -race 下都会
// 触发该报告。在 fork 修复前，依赖真实 rpcx client 链路的端到端测试在 race
// 模式下跳过——非 race 的 `go test ./rpc/...` 仍全量执行这些用例。
const raceDetectorEnabled = true
