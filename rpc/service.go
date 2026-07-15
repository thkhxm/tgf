package rpc

import (
	"sync"

	"context"
	"reflect"

	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/log"
	"go.uber.org/zap"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

// IService
//
//	@Description: 逻辑服务核心接口——身份、生命周期、同步方法清单。
//
// C2 重塑：
//   - 把 `GetLogicSyncMethod` 重命名为 `LogicSyncMethods`，遵循 Go 约定
//     （不带 Get 前缀，复数形式反映 "a list of"）。老名字直接删掉——业务方
//     升级 v2 需要把方法名一并改掉，否则原先的覆盖会静默失效。
//   - 身份 / 生命周期相关的几个方法保留在核心接口里。
//   - StateHandler / Login+Offline hook 相关挪到**可选子接口**
//     （IStatefulService / IUserLifecycleService）——服务按需实现，
//     框架代码按需 type assert。
type IService interface {
	GetName() string
	GetVersion() string
	Startup() (bool, error)
	Destroy(sub IService)
	// LogicSyncMethods 返回该 service 要求 rpcx 按"串行化"模式处理的方法名列表。
	// 返回 nil 表示全部走默认并发路径。
	// C2 重命名：原 GetLogicSyncMethod。
	LogicSyncMethods() []string
}

// IStatefulService 是一个可选子接口，描述那些会通过 rpcx 同步自身运行状态
// （ConsulServerState）的服务。
//
// 设计动机：StateHandler 在 v1 里只定义在 Module 基类上，不在 IService 接口里。
// 框架代码无法通过"接口类型断言"来判断某个 service 是否支持 state 变更通知，
// 只能依赖"它是不是 Module"这种反射式判断。C2 把它抽出来作为独立接口：
//
//	if stateful, ok := svc.(IStatefulService); ok {
//	    stateful.StateHandler(...)
//	}
//
// 继续嵌入 `Module` 的服务自动满足这个接口，不需要改任何代码。
type IStatefulService interface {
	StateHandler(ctx context.Context, args *client.ConsulServerState, reply *string) (err error)
}

// IUserLifecycleService 是一个可选子接口，描述可以注册"用户登录/下线"钩子的服务。
//
// 和 IStatefulService 同样的动机：把 v1 里只在 `Module` 基类上的 AddUserLoginHook /
// AddUserOfflineHook 抽出来作为独立接口，框架代码可以在启动阶段集中遍历
// 注册过的 service，type-assert 到本接口后统一分发全局 login/offline 事件。
//
// 嵌入 `Module` 的服务自动满足；业务若需要自定义行为可以重写或不实现。
type IUserLifecycleService interface {
	AddUserLoginHook(hook loginHook)
	AddUserOfflineHook(hook offlineHook)
}

type Module struct {
	Name            string
	Version         string
	State           client.ConsulServerState
	userLoginHook   []loginHook
	userOfflineHook []offlineHook
}

func (m *Module) GetName() string {
	return m.Name
}

func (m *Module) GetVersion() string {
	return m.Version
}

func (m *Module) Destroy(sub IService) {
	var ()
	log.InfoTag("system", "destroy module=%v version=%v", sub.GetName(), sub.GetVersion())
}

// LogicSyncMethods 是 C2 重命名后的默认实现：返回 nil 表示没有串行化方法。
// 业务子类按需 override 并返回具体的方法名列表。
func (m *Module) LogicSyncMethods() []string {
	return nil
}

func (m *Module) StateHandler(ctx context.Context, args *client.ConsulServerState, reply *string) (err error) {
	m.State = *args
	log.InfoTag("system", "update module state %v to %v module=%v version=%v", m.State, args, m.Name, m.Version)
	return
}

func (m *Module) AddUserLoginHook(hook loginHook) {
	m.userLoginHook = append(m.userLoginHook, hook)
}
func (m *Module) AddUserOfflineHook(hook offlineHook) {
	m.userOfflineHook = append(m.userOfflineHook, hook)
}

func (m *Module) OfflineHook(ctx context.Context, args *OfflineReq, reply *EmptyReply) (err error) {
	if len(m.userOfflineHook) == 0 {
		return
	}
	for _, hook := range m.userOfflineHook {
		err = hook(ctx, args.UserId, args.Replace)
		if err != nil {
			return
		}
	}
	return
}

func (m *Module) LoginHook(ctx context.Context, args *DefaultArgs, reply *EmptyReply) (err error) {
	if len(m.userLoginHook) == 0 {
		return
	}
	for _, hook := range m.userLoginHook {
		err = hook(ctx, args.C)
		if err != nil {
			return
		}
	}
	return
}

//***************************************************
// C2 子接口的真实消费方（CLEAN / E6 半成品清理）
//
// 背景：C2 把 StateHandler / Add*Hook 从 Module 基类抽成可选子接口
// IStatefulService / IUserLifecycleService，但框架生产代码里一直没有任何
// `svc.(IStatefulService)` 型断言落地——两个子接口是"定义了没人用"的死代码
// （V3-audit-findings.md C2 缺口：rpc/service.go）。
//
// 这里给它们补上真实语义：
//  1. IUserLifecycleService —— 全局登录/下线钩子的扇出消费方。业务过去必须对
//     每个 service 单独 AddUserLoginHook；现在通过 RegisterGlobalLoginHook 注册
//     一次，框架在启动时 type-assert 所有 service，统一分发给满足该子接口的服务。
//  2. IStatefulService —— Consul state 通知能力盘点。框架不再用"它是不是 Module"
//     这种反射式判断，而是按接口断言判断某个 service 能否接收 StateHandler 通知，
//     并把缺失能力的服务在启动日志里显式标出，便于排查"状态推送没生效"。
//
// 消费点：wireServiceCapabilities 由 Server.Run 经 registerLocalServices
//（local_dispatcher.go，Run 无条件调用）触发，对已成功 Startup 的 service 生效，
// 与单进程 / 分布式无关。
//***************************************************

var (
	// globalHookMu 保护下面两张全局钩子表。注册一般只在启动前发生，但用锁兜底
	// 业务在运行期补注册的场景。
	globalHookMu sync.Mutex
	// globalLoginHooks / globalOfflineHooks 是"注册一次、扇出到所有
	// IUserLifecycleService"的全局钩子。wireServiceCapabilities 在启动时消费。
	globalLoginHooks   []loginHook
	globalOfflineHooks []offlineHook
)

// RegisterGlobalLoginHook 注册一个全局用户登录钩子。
//
// 与 service 级 AddUserLoginHook 的区别：全局钩子在 Server.Run 时被框架统一
// 扇出到**所有**满足 IUserLifecycleService 的已装载 service，业务无需对每个
// service 单独注册。典型用途：跨所有逻辑服的统一登录埋点 / 风控 / 在线人数统计。
func RegisterGlobalLoginHook(hook func(ctx context.Context, userId string) error) {
	if hook == nil {
		return
	}
	globalHookMu.Lock()
	globalLoginHooks = append(globalLoginHooks, hook)
	globalHookMu.Unlock()
}

// RegisterGlobalOfflineHook 注册一个全局用户下线钩子。语义同 RegisterGlobalLoginHook，
// 扇出到所有满足 IUserLifecycleService 的 service。
func RegisterGlobalOfflineHook(hook func(ctx context.Context, userId string, replace bool) error) {
	if hook == nil {
		return
	}
	globalHookMu.Lock()
	globalOfflineHooks = append(globalOfflineHooks, hook)
	globalHookMu.Unlock()
}

// resetGlobalHooksForTest 清空全局钩子表（仅供单测隔离用）。
func resetGlobalHooksForTest() {
	globalHookMu.Lock()
	globalLoginHooks = nil
	globalOfflineHooks = nil
	globalHookMu.Unlock()
}

// lastServiceCapabilities 缓存最近一次 wireServiceCapabilities 的盘点结果，
// 供启动后诊断 / 单测断言读取。用锁保护避免并发启动多个 Server 时的数据竞争。
var (
	lastServiceCapMu   sync.RWMutex
	lastServiceCapData ServiceCapabilityReport
)

func setLastServiceCapabilities(r ServiceCapabilityReport) {
	lastServiceCapMu.Lock()
	lastServiceCapData = r
	lastServiceCapMu.Unlock()
}

// LastServiceCapabilities 返回最近一次服务能力盘点结果（C2 子接口断言的产物）。
// 业务可在 Run 之后读取，确认哪些逻辑服支持 state 通知 / 全局生命周期钩子。
func LastServiceCapabilities() ServiceCapabilityReport {
	lastServiceCapMu.RLock()
	defer lastServiceCapMu.RUnlock()
	return lastServiceCapData
}

// ServiceCapabilityReport 是 wireServiceCapabilities 的盘点结果，
// 描述本次启动里各 service 命中了哪些 C2 子接口。供日志 / 单测断言。
type ServiceCapabilityReport struct {
	// Stateful 是满足 IStatefulService（可接收 Consul StateHandler 通知）的 service 名。
	Stateful []string
	// Lifecycle 是满足 IUserLifecycleService（可挂载登录/下线钩子）的 service 名。
	Lifecycle []string
	// PlainOnly 是两个子接口都不满足的 service 名——它们收不到 state 通知、
	// 也吃不到全局钩子，多半是裸实现 IService 没嵌 Module，启动日志里要警示。
	PlainOnly []string
	// LoginHooksFanned / OfflineHooksFanned 记录本次实际扇出的全局钩子数量×命中服务数。
	LoginHooksFanned   int
	OfflineHooksFanned int
}

// wireServiceCapabilities 是 C2 两个子接口的真实消费方。
//
// 对传入的全部 service 做接口断言：
//   - 命中 IUserLifecycleService：把全局登录/下线钩子（RegisterGlobalHook* 注册的）
//     扇出注册进去，使一次注册对所有逻辑服生效；
//   - 命中 IStatefulService：登记到能力盘点，确认它能接收 Consul state 通知；
//   - 两者都不命中：归入 PlainOnly，启动日志显式警告（state 推送 / 全局钩子对它无效）。
//
// 返回的 ServiceCapabilityReport 既写进启动日志，也供单测断言"断言路径真的跑了"。
func wireServiceCapabilities(services []IService) ServiceCapabilityReport {
	var report ServiceCapabilityReport

	globalHookMu.Lock()
	loginHooks := make([]loginHook, len(globalLoginHooks))
	copy(loginHooks, globalLoginHooks)
	offlineHooks := make([]offlineHook, len(globalOfflineHooks))
	copy(offlineHooks, globalOfflineHooks)
	globalHookMu.Unlock()

	for _, svc := range services {
		if svc == nil {
			continue
		}
		name := svc.GetName()
		matched := false

		// IStatefulService：能否接收 Consul StateHandler 通知。
		if _, ok := svc.(IStatefulService); ok {
			report.Stateful = append(report.Stateful, name)
			matched = true
		}

		// IUserLifecycleService：扇出全局登录/下线钩子。
		if lifecycle, ok := svc.(IUserLifecycleService); ok {
			report.Lifecycle = append(report.Lifecycle, name)
			matched = true
			for _, h := range loginHooks {
				lifecycle.AddUserLoginHook(h)
				report.LoginHooksFanned++
			}
			for _, h := range offlineHooks {
				lifecycle.AddUserOfflineHook(h)
				report.OfflineHooksFanned++
			}
		}

		if !matched {
			report.PlainOnly = append(report.PlainOnly, name)
			log.WarnTagW("init", "service 未实现任何 C2 子接口，收不到 state 通知/全局钩子",
				zap.String("service", name))
		}
	}

	log.InfoTagW("init", "C2 服务能力盘点完成",
		zap.Int("stateful", len(report.Stateful)),
		zap.Int("lifecycle", len(report.Lifecycle)),
		zap.Int("plain", len(report.PlainOnly)),
		zap.Int("loginHooksFanned", report.LoginHooksFanned),
		zap.Int("offlineHooksFanned", report.OfflineHooksFanned))

	return report
}

type ServiceAPI[Req, Res any] struct {
	ModuleName  string
	Name        string
	MessageType string
	Des         string
	args        Req
	reply       Res
}

func (s *ServiceAPI[Req, Res]) New(req Req, res Res) *ServiceAPI[Req, Res] {
	var ()
	return &ServiceAPI[Req, Res]{ModuleName: s.ModuleName, Name: s.Name, args: req, reply: res, MessageType: s.MessageType}
}

func (s *ServiceAPI[Req, Res]) NewRPC(req Req) *ServiceAPI[Req, Res] {
	var res Res
	resType := reflect.TypeOf((*Res)(nil)).Elem()
	if resType.Kind() == reflect.Ptr {
		// rpcx 需要一个可写的回包目标；指针响应必须预先分配其 Elem，
		// 否则接口中的 typed nil 无法被远程解码器填充。
		res = reflect.New(resType.Elem()).Interface().(Res)
	}
	return &ServiceAPI[Req, Res]{ModuleName: s.ModuleName, Name: s.Name, args: req, reply: res, MessageType: s.MessageType}
}

func (s *ServiceAPI[Req, Res]) NewEmpty() *ServiceAPI[Req, Res] {
	var ()
	var req Req
	var res Res
	return &ServiceAPI[Req, Res]{ModuleName: s.ModuleName, Name: s.Name, args: req, reply: res, MessageType: s.MessageType}
}

func (s *ServiceAPI[Req, Res]) GetResult() Res {
	var ()
	return s.reply
}

func GetUserId(ctx context.Context) string {
	if ct, ok := ctx.(*share.Context); ok {
		return ct.GetReqMetaDataByKey(tgf.ContextKeyUserId)
	}
	return ""
}

func GetNodeId(ctx context.Context) string {
	if ct, ok := ctx.(*share.Context); ok {
		return ct.GetReqMetaDataByKey(tgf.ContextKeyNodeId)
	}
	return ""
}

func GetTemplateUserId(ctx context.Context) string {
	if ct, ok := ctx.(*share.Context); ok {
		return ct.GetReqMetaDataByKey(tgf.ContextKeyTemplateUserId)
	}
	return ""
}
