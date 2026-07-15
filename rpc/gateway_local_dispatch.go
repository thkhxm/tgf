package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description D4 / P0-2：单进程模式下的网关本地直通
//2026/6/10
//***************************************************

// 背景：
//   WithSingleProcess（= WithStandalone + WithInProcessDispatch）下 discovery 为 nil
//   且 rpcClient 不启动。网关 doLogic → sendMessage 原本必走 getRPCClient() →
//   startup() → 对 nil discovery 调方法 panic——单进程 + 网关组合首条客户端消息即崩，
//   panic 被 ants 池 recover 后该连接的 logic goroutine 死亡，后续请求全部静默丢弃。
//
// 本文件提供 sendMessage 的本地直通分支：
//   1. localGateWhiteList —— 单进程模式下"无需登录即可访问"的白名单。分布式路径的
//      白名单存在 rpcClient.whiteMethod 上，单进程模式 client 不启动，所以 Run()
//      在 registerLocalServices 之后把 Server.whiteServiceList 同步到这里，
//      保证两种模式下 WithWhiteService 的语义一致。
//   2. localGateDispatch —— 网关帧到本地 service 方法的反射桥接。
//
// 类型桥接的必要性：
//   doLogic 构造的是 *Args[protoreflect.ProtoMessage]{ByteData: frame.Data} /
//   *Reply[protoreflect.ProtoMessage]，而网关可达的业务方法签名是
//   *Args[*pb.XxxReq] / *Reply[*pb.XxxRes]（具体泛型实例化，反射调用要求类型精确
//   匹配）。分布式模式下这层转换由 rpcx 的序列化天然完成（msgpack 按字段名
//   ByteData/Code 对拷）；本地直通没有序列化，所以这里按同样的字段约定做反射对拷，
//   保证单进程与分布式语义一致。

import (
	"fmt"
	"reflect"
	"sync/atomic"

	"context"
)

// ---- 单进程网关白名单 ----

// localGateWhiteList 持有单进程模式下无需登录即可访问的 module.method 列表。
// 由 Server.Run 在 registerLocalServices 之后通过 setLocalGateWhiteList 写入；
// sendMessage 的本地直通分支读取。atomic.Value 保证并发安全（写一次读多次）。
var localGateWhiteList atomic.Value // 存 []string

// setLocalGateWhiteList 覆盖式写入白名单（拷贝一份，调用方的切片可继续复用）。
func setLocalGateWhiteList(list []string) {
	cp := make([]string, len(list))
	copy(cp, list)
	localGateWhiteList.Store(cp)
}

// checkLocalGateWhiteList 判断 module.method 是否在白名单内。
func checkLocalGateWhiteList(messageType string) bool {
	list, _ := localGateWhiteList.Load().([]string)
	for _, s := range list {
		if s == messageType {
			return true
		}
	}
	return false
}

// resetLocalGateWhiteListForTest 清空白名单，仅测试用。
func resetLocalGateWhiteListForTest() {
	localGateWhiteList.Store([]string{})
}

// ---- 网关帧 → 本地 service 的反射桥接 ----

// gateBridgeFields 是网关参数桥接时按名字对拷的字段集合。
// 与 rpcx 序列化的字段约定一致：Args 携带 ByteData，Reply 携带 ByteData + Code。
var gateBridgeFields = [...]string{"ByteData", "Code"}

// localGateDispatch 在单进程模式下把一条网关请求直接投递到本地注册的 service。
// 语义对齐分布式路径（xclient.Call）：
//   - 方法签名必须是 rpcx 约定的 (ctx, *Req, *Res) error；
//   - args/reply 与目标参数类型相同时直接传引用（零拷贝）；
//   - 类型不同（典型：网关的 Args[protoreflect.ProtoMessage] vs 业务的 Args[*pb.X]）
//     时按 ByteData/Code 字段对拷桥接——等价于 rpcx 序列化的效果；
//   - handler panic 被捕获并转为 error（分布式路径 rpcx service.call 自带 recover，
//     本地路径必须有对等保护，否则 panic 会沿 doLogic 杀死连接的 logic goroutine）。
func localGateDispatch(ctx context.Context, moduleName, serviceName string, args, reply interface{}) (err error) {
	svcVal, ok := localDispatcher.Lookup(moduleName)
	if !ok {
		return fmt.Errorf("%w: %s", ErrLocalServiceNotRegistered, moduleName)
	}
	m := svcVal.MethodByName(serviceName)
	if !m.IsValid() {
		return fmt.Errorf("%w: %s.%s", ErrLocalMethodNotFound, moduleName, serviceName)
	}
	mt := m.Type()
	if mt.NumIn() != 3 || mt.NumOut() != 1 {
		return fmt.Errorf("%w: %s.%s NumIn=%d NumOut=%d",
			ErrLocalMethodBadSignature, moduleName, serviceName, mt.NumIn(), mt.NumOut())
	}

	argVal, err := adaptGateParam(args, mt.In(1))
	if err != nil {
		return fmt.Errorf("tgf/rpc: local gate dispatch %s.%s args: %w", moduleName, serviceName, err)
	}
	replyVal, err := adaptGateParam(reply, mt.In(2))
	if err != nil {
		return fmt.Errorf("tgf/rpc: local gate dispatch %s.%s reply: %w", moduleName, serviceName, err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("tgf/rpc: local gate dispatch panic %s.%s: %v", moduleName, serviceName, r)
			}
		}()
		out := m.Call([]reflect.Value{
			reflect.ValueOf(ensureShareContext(ctx)),
			argVal,
			replyVal,
		})
		if len(out) > 0 && !out[0].IsNil() {
			err = out[0].Interface().(error)
		}
	}()
	if err != nil {
		return err
	}

	// 把桥接副本里 handler 写入的结果回拷给调用方的 reply（同对象时为 no-op）。
	copyBackGateReply(reply, replyVal)
	return nil
}

// adaptGateParam 把调用方传入的 args/reply 适配成目标方法的参数 reflect.Value。
// 规则（按优先级）：
//  1. v 为 nil → 按 want 构造零值（指针类型 reflect.New）
//  2. v 与 want 类型一致 → 直接用（typed nil 指针则分配真实对象）
//  3. v 可赋值给 want（接口参数等） → 直接用
//  4. 两边都是指向 struct 的指针，且都含 ByteData 字段 → 字段桥接
//     （网关 Args/Reply 泛型实例化不同导致的类型差异，按 rpcx 序列化字段约定对拷）
//  5. 其余 → 明确报错：网关可达方法的参数必须是 rpc.Args / rpc.Reply 形式
func adaptGateParam(v interface{}, want reflect.Type) (reflect.Value, error) {
	if v == nil {
		if want.Kind() == reflect.Ptr {
			return reflect.New(want.Elem()), nil
		}
		return reflect.Zero(want), nil
	}
	rv := reflect.ValueOf(v)
	if rv.Type() == want {
		if rv.Kind() == reflect.Ptr && rv.IsNil() {
			return reflect.New(want.Elem()), nil
		}
		return rv, nil
	}
	if rv.Type().AssignableTo(want) {
		return rv, nil
	}

	// 字段桥接路径
	if rv.Kind() == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct &&
		want.Kind() == reflect.Ptr && want.Elem().Kind() == reflect.Struct {
		if _, srcOK := rv.Elem().Type().FieldByName("ByteData"); srcOK {
			if _, dstOK := want.Elem().FieldByName("ByteData"); dstOK {
				dst := reflect.New(want.Elem())
				bridgeCopyFields(dst, rv)
				return dst, nil
			}
		}
	}

	return reflect.Value{}, fmt.Errorf(
		"参数类型不兼容: 传入 %v 目标 %v (网关可达方法的参数必须是 rpc.Args/rpc.Reply 形式)",
		rv.Type(), want)
}

// copyBackGateReply 把桥接副本 used 中 handler 写入的字段回拷到调用方的 reply。
// reply 与 used 是同一对象（未桥接）或 reply 为 nil/typed-nil 时直接返回。
func copyBackGateReply(reply interface{}, used reflect.Value) {
	if reply == nil || !used.IsValid() {
		return
	}
	ov := reflect.ValueOf(reply)
	if ov.Kind() != reflect.Ptr || ov.IsNil() || ov.Elem().Kind() != reflect.Struct {
		return
	}
	if used.Kind() != reflect.Ptr || used.IsNil() || used.Elem().Kind() != reflect.Struct {
		return
	}
	// 同一对象：handler 已经原地写入
	if ov.Type() == used.Type() && ov.Pointer() == used.Pointer() {
		return
	}
	bridgeCopyFields(ov, used)
}

// bridgeCopyFields 按 gateBridgeFields 列表把 src 的同名字段拷到 dst。
// dst / src 都必须是指向 struct 的非 nil 指针。
func bridgeCopyFields(dst, src reflect.Value) {
	de, se := dst.Elem(), src.Elem()
	for _, name := range gateBridgeFields {
		df := de.FieldByName(name)
		sf := se.FieldByName(name)
		if df.IsValid() && sf.IsValid() && df.CanSet() && sf.Type().AssignableTo(df.Type()) {
			df.Set(sf)
		}
	}
}
