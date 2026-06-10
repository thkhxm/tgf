package rpc

import (
	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"golang.org/x/net/context"
	"reflect"
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
	var ()
	var res Res
	resType := reflect.TypeOf((*Res)(nil)).Elem() // 获取Res的类型
	resValue := reflect.New(resType)              // 创建Res的新实例

	// 如果Res是一个指针类型，我们需要通过.Elem()获取其指向的值
	//if resType.Kind() == reflect.Ptr {
	//	res = resValue.Interface().(Res)
	//} else {
	res = resValue.Elem().Interface().(Res)
	//}
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
