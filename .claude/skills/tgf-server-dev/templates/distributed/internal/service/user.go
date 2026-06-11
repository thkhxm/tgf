// Package service 存放业务 Module 实现（rpcx service）。
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc"

	"{{PROJECT_NAME}}/internal/api"
)

// UserService 用户模块。内嵌 rpc.Module 获得 IService 默认实现
// （State / Destroy / 登录与下线 hook 接入点等），Name 即 Consul 注册的 module 名。
type UserService struct {
	rpc.Module
}

func NewUserService() *UserService {
	return &UserService{Module: rpc.Module{Name: "user", Version: "1.0"}}
}

// Startup 服务启动钩子：初始化缓存/预热数据放这里。
// 返回 (false, err) 时该服务不会注册进服务发现（fail-fast，不带病发布）。
func (s *UserService) Startup() (bool, error) {
	log.InfoTag("user", "UserService 启动 version=%v", s.Version)
	return true, nil
}

// Login 账号验证后签发网关登录 token（服务间 RPC，由登录入口进程调用）。
//
// 完整客户端登录链路：
//  1. 客户端经入口（HTTP 或白名单帧）调到本方法完成账号验证；
//  2. 本方法用 rpc.GenerateLoginToken 签发 token 下发客户端；
//  3. 客户端向网关发 gate.Login 帧携带 token（服务侧辅助入口
//     rpc.UserLoginWithToken(ctx, userId, token)），网关 HMAC 校验通过后
//     绑定 用户↔连接↔节点。
//
// 密钥来源：.env.<module> 的 LoginTokenSecret（或 WithLoginTokenSecret）。
func (s *UserService) Login(ctx context.Context, req *api.LoginReq, reply *api.LoginRes) error {
	// TODO-业务：替换为真实账号验证（账密 / 第三方平台 SDK code 换 openid 等）
	log.InfoTag("user", "用户登录 userId=%v", req.UserId)

	token, err := rpc.GenerateLoginToken(req.UserId, 12*time.Hour)
	if err != nil {
		// 密钥未配置等致命配置错误——向上抛，禁止吞错降级放行
		return fmt.Errorf("签发登录 token 失败: %w", err)
	}
	reply.Token = token
	reply.Welcome = fmt.Sprintf("欢迎 %s", req.UserId)
	return nil
}

// GetRole 角色查询（服务间 RPC 风格示例）。
func (s *UserService) GetRole(ctx context.Context, req *api.GetRoleReq, reply *api.GetRoleRes) error {
	// TODO-业务：替换为真实数据读取（db.AutoCacheBuilder 缓存层，见 README《数据层》）
	reply.Name = "Hero_" + req.UserId
	reply.Level = 1
	return nil
}

// 客户端帧直达的方法形态（供对照；启用前先生成 protobuf 代码）：
//
//	func (s *UserService) Hello(ctx context.Context,
//	    args *rpc.Args[*pb.HelloReq], reply *rpc.Reply[*pb.HelloRes]) error {
//	    req := args.GetData()
//	    return reply.SetData(&pb.HelloRes{Msg: "hi " + req.Name})
//	}
//
// 普通 struct 参数的方法只能被 rpc.SendRPCMessage（服务间）调用；
// 网关可达方法必须是 rpc.Args[T]/rpc.Reply[T]（T 为 protobuf Message）。
