package rpc

// A7: RPC 调用超时策略化（骨架）。
//
// 原实现 SendRPCMessage 里硬编码 `time.After(time.Second * 5)`——所有 RPC 调用
// 一律 5 秒，既没法按方法区分，也无法在运行时覆盖。A7 引入两层配置：
//
//   1. defaultRPCTimeout — 全局默认（package 级 atomic.Int64，纳秒）
//   2. rpcMethodTimeouts — 每方法覆盖（sync.Map，key = "module.method"）
//
// 查找顺序：per-method → default。
//
// 这一档**只做骨架**——完整的限流 / 熔断 / 降级策略在 C6 落地。
// 本档不引入新的环境变量、不改 rpcx 层 TCP 超时，只替换 SendRPCMessage 里
// 那个魔数的读取路径。

import (
	"sync"
	"sync/atomic"
	"time"

	tgfconfig "github.com/thkhxm/tgf/config"
)

// defaultRPCTimeoutNanos 以纳秒保存全局默认 RPC 超时；用 atomic.Int64 保证
// 并发读写安全（WithDefaultRPCTimeout 可以在 Server.Run() 之后动态改）。
//
// v2: 启动时从 RPCDefaultTimeoutMs 读取，找不到时回退到 5 秒。
// 这意味着零配置行为仍然是 5 秒（和 A7 之前一致），同时业务可以通过
// 环境变量 RPCDefaultTimeoutMs=2000 全局调整为 2 秒。
//
// E 档配置迁移 + 热更接线：读取改走新配置系统的类型化字段；本值是"可热更项"
// （本就是 atomic），init 注册 OnReload 订阅者——业务改 .env.<module> 后经
// admin POST /config/reload 或显式 tgfconfig.Reload() 触发，新超时原子生效。
// 注意：Reload 会以"配置值"覆盖 SetDefaultRPCTimeout 设置的运行态值（配置为准）。
var defaultRPCTimeoutNanos atomic.Int64

// applyDefaultRPCTimeoutFromConfig 把配置中的默认 RPC 超时原子写入运行态。
// 既是 init 的启动装载，也是 OnReload 的热更订阅者。
func applyDefaultRPCTimeoutFromConfig(c *tgfconfig.Config) {
	timeoutMs := c.RPC.DefaultTimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	defaultRPCTimeoutNanos.Store(int64(time.Duration(timeoutMs) * time.Millisecond))
}

func init() {
	applyDefaultRPCTimeoutFromConfig(tgfconfig.Current())
	tgfconfig.OnReload(applyDefaultRPCTimeoutFromConfig)
}

// rpcMethodTimeouts 存每方法的覆盖。key 格式 "module.method"，和 ServiceAPI.MessageType
// 字段对齐，避免两套命名。value 是 time.Duration 的纳秒值（sync.Map 用 any 类型）。
var rpcMethodTimeouts sync.Map

// resolveRPCTimeout 是 SendRPCMessage 用的单一查找入口。
// 查找顺序：per-method → default。返回的 duration 一定 > 0。
// 传入的 moduleName/methodName 可以为空——空时只查 default。
func resolveRPCTimeout(moduleName, methodName string) time.Duration {
	if moduleName != "" && methodName != "" {
		key := moduleName + "." + methodName
		if v, ok := rpcMethodTimeouts.Load(key); ok {
			if d, ok := v.(time.Duration); ok && d > 0 {
				return d
			}
		}
	}
	d := time.Duration(defaultRPCTimeoutNanos.Load())
	if d <= 0 {
		// 防御：万一被设成非正数，退回到编译期默认 5 秒避免 time.After(0) 立即触发
		return 5 * time.Second
	}
	return d
}

// SetDefaultRPCTimeout 直接修改全局默认 RPC 超时。
// 公开给业务代码 / 测试使用——生产代码更推荐走 Server.WithDefaultRPCTimeout
// 保持"通过 builder 配置"的一致风格。
func SetDefaultRPCTimeout(dur time.Duration) {
	if dur <= 0 {
		return
	}
	defaultRPCTimeoutNanos.Store(int64(dur))
}

// SetMethodRPCTimeout 直接给某个 "module.method" 注册覆盖。传入 dur <= 0 表示
// 删除覆盖，恢复到默认值。公开给测试和配置热更路径使用；生产推荐走 Server.WithMethodTimeout。
func SetMethodRPCTimeout(method string, dur time.Duration) {
	if method == "" {
		return
	}
	if dur <= 0 {
		rpcMethodTimeouts.Delete(method)
		return
	}
	rpcMethodTimeouts.Store(method, dur)
}

// WithDefaultRPCTimeout 是 Server 上的 builder 风格入口，等价于 SetDefaultRPCTimeout。
// A7 决定把实际状态放在 package-level 而非 Server 实例字段上，因为 SendRPCMessage
// 是 package-level 的泛型函数，没有 *Server 句柄可以查。C 档重构时会连 SendRPCMessage
// 一起挪到 Server 方法上。
func (s *Server) WithDefaultRPCTimeout(dur time.Duration) *Server {
	SetDefaultRPCTimeout(dur)
	return s
}

// WithMethodTimeout 覆盖某个 "module.method" 的 RPC 超时。
// 推荐用 ServiceAPI.MessageType 作为 key（例如 Gate.Login.MessageType = "gate.Login"）。
func (s *Server) WithMethodTimeout(method string, dur time.Duration) *Server {
	SetMethodRPCTimeout(method, dur)
	return s
}

// ClearMethodRPCTimeoutsForTest 清空所有 per-method 覆盖。仅用于单测隔离，
// 生产代码不应依赖。
func ClearMethodRPCTimeoutsForTest() {
	rpcMethodTimeouts.Range(func(k, _ any) bool {
		rpcMethodTimeouts.Delete(k)
		return true
	})
}
