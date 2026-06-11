package rpc

import (
	"errors"
	"fmt"
	"os"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/log"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G3：client-only 模式——
//
// 背景（v3 审计5 P1）：业务方想写"REST API + 调后端 RPC"的无状态 web 进程，
// 原本必须 WithService(占位服务) 把自己伪装成一个 RPC 服务节点（占一个 rpcx
// 端口、出现在 Consul 服务发现里）才能让 getRPCClient 可用。client-only 模式
// 砍掉这个伪装：
//   - 不注册任何 service、不创建 rpcx server、不监听 rpcx 端口、不出现在
//     RPC 服务发现里；
//   - 照常初始化 Consul discovery + RPC client watch——SendRPCMessage /
//     ServiceAPI / web Backend 全部可用；
//   - 可与 WithHTTPService / WithHTTPServiceConsul 组合成纯 web 进程
//     （HTTP 入口 + 后端 RPC 调用 + 自身以 HTTP 形态注册进 Consul）；
//   - 共享 D3 优雅停机（Destroy 序列对 rpcServer==nil / 无 service 全部安全）。
//
// 典型用法（无状态 web 服务）：
//
//	rpc.NewRPCServer().
//	    WithClientOnly().
//	    WithHTTPService(web.Options{Routes: func(r *web.Router) {
//	        r.POST("/api/user", web.RPC[GetUserReq, GetUserRes]("user", "GetUser"))
//	    }}).
//	    Run()
//
//2026/6/10
//***************************************************

// WithClientOnly 开启 client-only 模式：本进程不注册任何 service、不监听
// rpcx 端口，仅初始化 RPC client 调用后端（可叠加 WithHTTPService 家族构成
// 纯 web 进程）。与 WithService / WithGateway* / WithoutServiceClient /
// WithoutConsul 互斥（Run 时校验，冲突即 fail-fast 退出）。
func (s *Server) WithClientOnly() *Server {
	s.clientOnly = true
	log.InfoTag("init", "开启 client-only 模式(不注册 service,不监听 rpcx 端口)")
	return s
}

// validateClientOnly 校验 client-only 模式的配置一致性。
// 在 preServe 钩子执行后调用（WithGatewayOptions 等通过钩子追加 service，
// 必须等钩子跑完才能看到完整的 service 列表）。
func (s *Server) validateClientOnly() error {
	if !s.clientOnly {
		return nil
	}
	if len(s.service) > 0 {
		names := make([]string, 0, len(s.service))
		for _, svc := range s.service {
			names = append(names, svc.GetName())
		}
		return fmt.Errorf("client-only 模式不能装载 service(WithService/WithGateway* 与 WithClientOnly 互斥): %v", names)
	}
	if s.disableClient {
		return errors.New("client-only 模式与 WithoutServiceClient 互斥(client-only 的意义就是初始化 RPC client)")
	}
	if s.disableConsul {
		return errors.New("client-only 模式与 WithoutConsul/WithStandalone/WithSingleProcess 互斥(RPC client 依赖 Consul 服务发现定位后端)")
	}
	return nil
}

// runClientOnly 是 client-only 模式下 Run 的执行体（Run 在 preServe 钩子之后
// 分流到这里）。与常规 Run 的差异：跳过 rpcx server 创建 / 监听 / 服务注册 /
// Consul TTL 节点心跳，保留 postServe 钩子（RPC client watch + 白名单）、
// HTTP 服务启动与 HTTP 服务的 Consul 注册。
//
// 启动期配置冲突 fail-fast（os.Exit(1)）——与 rpcx 监听失败同语义：
// 不允许"进程活着但形态不对"的半启动状态。
func (s *Server) runClientOnly() <-chan bool {
	if err := s.validateClientOnly(); err != nil {
		log.Error("[init] client-only 配置冲突 err=%v", err)
		os.Exit(1)
	}

	// postServe 钩子：用户钩子 + 默认 RPC client watch（client-only 不允许
	// WithoutServiceClient，client 必然在这里启动；Consul 暂不可达时 startup
	// 返回 nil，后续 getRPCClient 按 D4/D5 语义自动重试）。
	for _, hook := range s.buildPostServeHooks() {
		hook(s)
	}

	// HTTP 服务（G1）+ Consul 注册（G2/G3）：与常规 Run 同一套启动/注册路径。
	s.startHTTPServers()
	s.registerHTTPConsulServices()

	// WithHealthCheck 配置了心跳时维持 A6 行为（atomic 状态位 + 日志）；
	// client-only 无 rpcx 节点注册，不挂 Consul TTL 节点心跳（consulHealth 为 nil）。
	s.startHealthCheckLoop()

	log.InfoTag("init", "client-only 模式启动完成(未注册 service,未监听 rpcx 端口) nodeId=%v httpServers=%v",
		tgf.NodeId, len(s.httpServers))
	return tgf.CloseChan()
}
