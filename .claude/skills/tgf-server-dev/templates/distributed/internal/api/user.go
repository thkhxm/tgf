// Package api 集中声明跨 module 的 RPC 契约（出入参 + ServiceAPI 描述符）。
//
// 约定：调用方与实现方都只依赖本包，不互相 import 对方的 service 包——
// 这是 tgf 项目里防 import 环、保持模块边界清晰的标准做法。
package api

import "github.com/thkhxm/tgf/v2/rpc"

// ---- user.Login：账号验证 + 签发网关登录 token ----

type LoginReq struct {
	UserId string
	// 真实项目这里是账密 / 第三方平台 code 等凭据字段
}

type LoginRes struct {
	// Token 用于客户端后续 gate.Login（框架默认登录鉴权，fail-closed）
	Token   string
	Welcome string
}

// UserLogin 调用方式：rpc.SendRPCMessage(ctx, api.UserLogin.NewRPC(&api.LoginReq{...}))
var UserLogin = &rpc.ServiceAPI[*LoginReq, *LoginRes]{
	ModuleName:  "user",
	Name:        "Login",
	MessageType: "user.Login",
}

// ---- user.GetRole：角色查询 ----

type GetRoleReq struct {
	UserId string
}

type GetRoleRes struct {
	Name  string
	Level int32
}

var UserGetRole = &rpc.ServiceAPI[*GetRoleReq, *GetRoleRes]{
	ModuleName:  "user",
	Name:        "GetRole",
	MessageType: "user.GetRole",
}
