package rpc

// 单进程模式 / In-Process RPC 直通。
//
// 背景：
//   C1 的 WithStandalone() 只是关闭了 Consul discovery 和 RPC client 的默认
//   装载，进程内不会向 Consul 注册、也不会启动 client watch。但业务代码里
//   SendRPCMessage 调的 xclient 还是要走 rpcx 的 discovery——没有 discovery
//   条目时调用就会失败。
//
//   真正的"单进程多 module"场景（本地开发 / 单元测试 / 小规模部署）希望：
//     1. 不依赖 Consul 任何基建
//     2. module A 可以通过 SendRPCMessage 调 module B 的方法
//     3. 这种调用零网络开销，直接反射调用本地 method
//     4. 策略管道（C6 限流/熔断）和 metrics 埋点（B4）依然生效，保持和
//        分布式路径一致的语义
//
// 实现：
//   localDispatcher 维护一张 moduleName -> service instance 的 map。
//   SendRPCMessage 入口先查 dispatcher——命中则走反射调用，未命中才 fallback
//   到原有的 rpcx client 路径。
//
// 方法签名约定：
//   tgf service method 的签名必须是：
//     func (s *S) Xxx(ctx context.Context, args *Req, reply *Res) error
//   这和 rpcx 完全一致。反射调用时按此签名 `m.Call([ctx, args, reply])`
//   再读第一个返回值作为 error。

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/thkhxm/rpcx/share"
	"golang.org/x/net/context"
)

// localDispatchEnabled 包级开关。默认 false（零改动），Server.WithInProcessDispatch
// 置为 true。用 sync/atomic.Bool 无锁 fast-path 检查。
var localDispatchEnabled atomic.Bool

// ---- localDispatcher ----

// ErrLocalServiceNotRegistered 在开启 dispatch 但目标 module 未注册时返回。
var ErrLocalServiceNotRegistered = errors.New("tgf/rpc: local dispatch: service not registered")

// ErrLocalMethodNotFound 本地 service 存在但 method 不存在。
var ErrLocalMethodNotFound = errors.New("tgf/rpc: local dispatch: method not found")

// ErrLocalMethodBadSignature 方法签名不符合 rpcx 约定。
var ErrLocalMethodBadSignature = errors.New("tgf/rpc: local dispatch: method signature mismatch")

// localDispatcher 是进程级单例。
// 所有路径只有 Register 是写，RPC 入口是读，用 sync.RWMutex 就够。
type dispatcherT struct {
	mu       sync.RWMutex
	services map[string]reflect.Value // moduleName -> service 反射值（已经是 *Struct）
}

var localDispatcher = &dispatcherT{services: map[string]reflect.Value{}}

// Register 把一个 service 加到本地 dispatcher。moduleName 必须是调用方 api.ModuleName
// 使用的字符串——一般就是 svc.GetName() 的返回值。重复注册会覆盖。
//
// 本函数由 Server.Run 在开启 in-process dispatch 时遍历 s.service 调用。
// 业务代码通常不需要直接调它。
func (d *dispatcherT) Register(moduleName string, svc IService) {
	if moduleName == "" || svc == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.services[moduleName] = reflect.ValueOf(svc)
}

// Lookup 返回是否有注册的 service。
func (d *dispatcherT) Lookup(moduleName string) (reflect.Value, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v, ok := d.services[moduleName]
	return v, ok
}

// Call 执行一次 module.method 的本地反射调用。
// ctx 会自动升级为 share.Context——rpcx 的所有 meta 接口都依赖这个具体类型。
// args / reply 可以是任意指针；内部不做类型检查，完全由调用方保证类型一致。
// 调用方传的 args 会被直接塞给反射调用，reply 指针指向的对象会被方法写入。
func (d *dispatcherT) Call(ctx context.Context, moduleName, methodName string, args, reply any) error {
	svcVal, ok := d.Lookup(moduleName)
	if !ok {
		return fmt.Errorf("%w: %s", ErrLocalServiceNotRegistered, moduleName)
	}

	m := svcVal.MethodByName(methodName)
	if !m.IsValid() {
		return fmt.Errorf("%w: %s.%s", ErrLocalMethodNotFound, moduleName, methodName)
	}

	// 签名校验：(ctx, args, reply) error
	mt := m.Type()
	if mt.NumIn() != 3 || mt.NumOut() != 1 {
		return fmt.Errorf("%w: %s.%s NumIn=%d NumOut=%d",
			ErrLocalMethodBadSignature, moduleName, methodName, mt.NumIn(), mt.NumOut())
	}

	// 升级 ctx 为 share.Context（保留 trace id / user id 等 meta）
	ctx = ensureShareContext(ctx)

	// reply 处理：
	//   - nil → 用方法签名第三个参数类型构造一个零值（SendNoReplyRPCMessage 场景）
	//   - typed nil pointer（如 (*GiveGiftRes)(nil)）→ 分配一个真正的结构体
	//     这是 ServiceAPI.NewRPC 的常见行为——对指针类型它会生成 typed nil
	//   - 非 nil 的正常指针 → 直接用
	replyVal := ensureAllocated(reply, mt.In(2))

	// args 处理：同理
	argsVal := ensureAllocated(args, mt.In(1))

	out := m.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		argsVal,
		replyVal,
	})
	if len(out) > 0 && !out[0].IsNil() {
		return out[0].Interface().(error)
	}
	return nil
}

// ensureShareContext 在 ctx 不是 *share.Context 时把它包一层。
// rpcx 内部用 share.Context 承载 ReqMetaDataKey / ResMetaDataKey，本地调用
// 也保持这个不变式，让 GetUserId / GetNodeId 等 helper 继续工作。
func ensureShareContext(ctx context.Context) context.Context {
	if ctx == nil {
		return share.NewContext(context.Background())
	}
	if _, ok := ctx.(*share.Context); ok {
		return ctx
	}
	return share.NewContext(ctx)
}

// ---- Server builder 接入 ----

// WithInProcessDispatch 打开进程内 RPC 直通。
// 开启后 SendRPCMessage 会先查 localDispatcher——命中则反射调用，绕开 rpcx/Consul。
// 未命中的 module 仍然 fallback 到原有的分布式路径。
//
// 开启时机：任意，SendRPCMessage fast-path 读 atomic 开关，无初始化约束。
// 但 s.service 的反射注册在 Server.Run 里一次性做，所以 Run 后新增 service
// 本地调用不可见（当前版本不支持动态增减）。
func (s *Server) WithInProcessDispatch() *Server {
	s.inProcessDispatch = true
	return s
}

// WithSingleProcess 是"单进程模式"的一键开关。
//
// 等价于：
//
//	WithStandalone()          // 不装 Consul / 不启动 client watch
//	WithInProcessDispatch()   // 进程内 RPC 直通
//
// 典型用法：
//
//	rpc.NewRPCServer().
//	    WithSingleProcess().
//	    WithService(serviceA).
//	    WithService(serviceB).
//	    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
//	    Run()
//
// serviceA 通过 SendRPCMessage 调 serviceB 的方法——零网络，零 Consul，
// 直接反射调用 serviceB 的对应 method。
func (s *Server) WithSingleProcess() *Server {
	return s.WithStandalone().WithInProcessDispatch()
}

// registerLocalServices 由 Server.Run 调用，把 service 列表注册到 dispatcher。
// 幂等：重复注册同一个 module 会覆盖。
func (s *Server) registerLocalServices() {
	if !s.inProcessDispatch {
		return
	}
	for _, svc := range s.service {
		localDispatcher.Register(svc.GetName(), svc)
	}
	localDispatchEnabled.Store(true)
}

// allocateIfNilPtr 在 v 是 typed nil pointer（如 (*T)(nil)）时分配一个
// 真正的 *T。用于 SendRPCMessage 的 local dispatch 路径——ServiceAPI.NewRPC
// 对指针类型生成的 reply 是 typed nil，需要在调 method 之前分配好。
func allocateIfNilPtr(v any) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return reflect.New(rv.Type().Elem()).Interface()
	}
	return v
}

// ensureAllocated 把一个 args/reply 值转成 reflect.Value。
// 处理三种情况：
//   - nil（interface nil）→ 用 paramType 构造零值（指针类型则 reflect.New）
//   - typed nil pointer（如 (*T)(nil)）→ 分配一个 *T 指向的真实结构体
//   - 正常非 nil 值 → 直接 reflect.ValueOf
func ensureAllocated(v any, paramType reflect.Type) reflect.Value {
	if v == nil {
		if paramType.Kind() == reflect.Ptr {
			return reflect.New(paramType.Elem())
		}
		return reflect.Zero(paramType)
	}
	rv := reflect.ValueOf(v)
	// typed nil pointer：rv.Kind()==Ptr && rv.IsNil()
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return reflect.New(rv.Type().Elem())
	}
	return rv
}

// ---- 测试辅助 ----

// ResetLocalDispatcherForTest 清空本地 dispatcher 状态，仅测试用。
func ResetLocalDispatcherForTest() {
	localDispatcher.mu.Lock()
	localDispatcher.services = map[string]reflect.Value{}
	localDispatcher.mu.Unlock()
	localDispatchEnabled.Store(false)
}
