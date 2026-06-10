package internal

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G2/G3 · HTTP 服务注册进 Consul（带 health check）
//
// 背景（v3 审计5 P2："分布式 web 服务三要素全缺"之一）：Consul 注册原本只发生
// 在 rpcx KV 维度，HTTP 服务没有任何途径把自己的 HTTP 端口注册进服务发现被
// 发现/负载均衡。本文件把 HTTP 服务注册为 Consul agent service（标准 catalog，
// 可被 Consul DNS / API / fabio / traefik 等标准生态发现与 LB），并挂 health
// check——复用 F2 的两种检查形态：
//   - TTL check（默认）：框架侧续约 goroutine 周期 UpdateTTL。不要求 Consul
//     agent 能反向访问服务端口（容器/NAT 环境零配置可用），语义与 F2 节点
//     心跳完全一致（停止续约 = 服务死亡信号，TTL 内转 critical 并按
//     DeregisterCriticalServiceAfter 自动摘除）。
//   - HTTP check：Consul agent 主动 GET health endpoint。要求 agent 可达服务
//     地址（同机 agent / 扁平网络），换来"探活打真实 HTTP 栈"的更强语义。
//
// 协议依据（§2.8：以本仓库锁定的 consul/api v1.8.1 源码核对，
// agent.go AgentServiceCheck 的 TTL / HTTP / Interval / Timeout 字段）：
//   - HTTP check 文档: https://developer.hashicorp.com/consul/docs/services/usage/checks#http-checks
//   - TTL check 文档:  https://developer.hashicorp.com/consul/docs/services/usage/checks#ttl-checks
//   - DeregisterCriticalServiceAfter 服务端硬下限 1 分钟（同 health.go）。
//
//2026/6/10
//***************************************************

// HTTP 服务 health check 的两种模式。
const (
	// HTTPCheckModeTTL 框架侧续约（默认）：调用方周期调 Renew。
	HTTPCheckModeTTL = "ttl"
	// HTTPCheckModeHTTP Consul agent 主动探测 health endpoint。
	HTTPCheckModeHTTP = "http"
)

// DefaultHTTPCheckTimeout 是 HTTP check 模式的默认探测超时。
const DefaultHTTPCheckTimeout = 2 * time.Second

// HTTPServiceOptions 是 NewConsulHTTPService 的输入。
type HTTPServiceOptions struct {
	// ServiceName Consul service 逻辑名（发现/负载均衡的检索键）。必填。
	ServiceName string
	// Address HTTP 服务对外可达地址 "host:port"。必填，host 不能是通配地址
	//（调用方负责把 ":8090" / "0.0.0.0:8090" 解析成可达 host，见 rpc 侧
	// resolveHTTPAdvertiseAddr）。
	Address string
	// HealthPath health endpoint 路径。空 → "/health"（与 web.DefaultHealthPath 一致）。
	HealthPath string
	// CheckMode 检查模式：空/HTTPCheckModeTTL → TTL；HTTPCheckModeHTTP → HTTP。
	CheckMode string
	// Interval TTL 模式 = 续约周期（TTL = 3×Interval）；HTTP 模式 = 探测间隔。
	// <=0 → DefaultTTLHealthInterval。
	Interval time.Duration
	// TTL 显式覆盖 TTL 模式的检查 TTL（测试用小值）。<=0 → Interval×3；
	// 显式给定时必须 > Interval。HTTP 模式忽略。
	TTL time.Duration
	// Timeout HTTP 模式的单次探测超时。<=0 → DefaultHTTPCheckTimeout。TTL 模式忽略。
	Timeout time.Duration
	// DeregisterCriticalAfter 检查转 critical 后自动摘除 service 的时长。
	// <=0 或小于 1m → Consul 硬下限 1m。
	DeregisterCriticalAfter time.Duration
	// Tags / Meta 透传到 Consul service（路由标签 / 元数据）。
	Tags []string
	Meta map[string]string
	// ConsulAddress 显式覆盖 Consul HTTP 地址（测试用）。空 → 读配置 ConsulAddress 第一项。
	ConsulAddress string
}

// buildHTTPServiceRegistration 校验 options 并组装 AgentServiceRegistration。
// 纯函数（不触网），字段映射由单元测试锁定（http_service_test.go）。
// 返回 (注册体, 归一化后的 Interval, error)。
func buildHTTPServiceRegistration(opt HTTPServiceOptions) (*api.AgentServiceRegistration, time.Duration, error) {
	name := strings.TrimSpace(opt.ServiceName)
	if name == "" {
		return nil, 0, fmt.Errorf("ServiceName 必填（Consul 发现/负载均衡的检索键）")
	}
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(opt.Address, "http://"))
	if err != nil {
		return nil, 0, fmt.Errorf("Address 不是合法的 host:port (%q): %w", opt.Address, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return nil, 0, fmt.Errorf("Address 的 host 不能是通配地址 (%q)——Consul 消费方拿到它无法回连", opt.Address)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, 0, fmt.Errorf("Address 端口非数字 (%q): %w", portStr, err)
	}

	mode := opt.CheckMode
	if mode == "" {
		mode = HTTPCheckModeTTL
	}
	if mode != HTTPCheckModeTTL && mode != HTTPCheckModeHTTP {
		return nil, 0, fmt.Errorf("CheckMode 非法 (%q)，只支持 %q / %q", opt.CheckMode, HTTPCheckModeTTL, HTTPCheckModeHTTP)
	}

	interval := opt.Interval
	if interval <= 0 {
		interval = DefaultTTLHealthInterval
	}
	dereg := opt.DeregisterCriticalAfter
	if dereg < minDeregisterCriticalAfter {
		dereg = minDeregisterCriticalAfter
	}
	healthPath := strings.TrimSpace(opt.HealthPath)
	if healthPath == "" {
		healthPath = "/health" // 与 web.DefaultHealthPath 保持一致（internal 不 import web，常量同步维护）
	}
	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}

	serviceID := fmt.Sprintf("%s-%s:%d", name, host, port)
	check := &api.AgentServiceCheck{
		CheckID: serviceID + "-check",
		// critical 后由 Consul reaper 自动摘除（兜底 kill -9 等无法优雅停机的死法）。
		DeregisterCriticalServiceAfter: dereg.String(),
	}
	switch mode {
	case HTTPCheckModeTTL:
		ttl := opt.TTL
		if ttl <= 0 {
			ttl = interval * ttlFactor
		}
		if ttl <= interval {
			return nil, 0, fmt.Errorf("TTL(%v) 必须大于续约周期(%v)，否则正常续约都会被判 critical", ttl, interval)
		}
		check.Name = "tgf http service TTL heartbeat"
		check.TTL = ttl.String()
		// 注册即 passing：注册发生在 HTTP 监听就绪之后（rpc 侧时序保证），
		// 不需要等第一次续约才转健康。
		check.Status = api.HealthPassing
	case HTTPCheckModeHTTP:
		timeout := opt.Timeout
		if timeout <= 0 {
			timeout = DefaultHTTPCheckTimeout
		}
		check.Name = "tgf http service health endpoint"
		check.HTTP = fmt.Sprintf("http://%s%s", net.JoinHostPort(host, portStr), healthPath)
		check.Interval = interval.String()
		check.Timeout = timeout.String()
		// HTTP 模式初始状态交给 Consul 首轮探测裁定（注册后 interval 内出结果），
		// 不预置 passing——agent 探不通的环境应当尽快暴露而不是假绿。
	}

	meta := map[string]string{
		"nodeId":     tgf.NodeId,
		"protocol":   "http",
		"healthPath": healthPath,
	}
	for k, v := range opt.Meta {
		meta[k] = v
	}
	reg := &api.AgentServiceRegistration{
		ID:      serviceID,
		Name:    name,
		Address: host,
		Port:    port,
		Tags:    opt.Tags,
		Meta:    meta,
		Check:   check,
	}
	return reg, interval, nil
}

// ConsulHTTPService 持有一个已注册进 Consul 的 HTTP service。
// 生命周期：NewConsulHTTPService（注册）→ [TTL 模式] 周期 Renew（续约/自愈）
// → Deregister（优雅停机摘除）。Renew/Deregister 并发安全。
type ConsulHTTPService struct {
	agent    *api.Agent
	reg      *api.AgentServiceRegistration
	checkID  string
	interval time.Duration
	ttlMode  bool
	mu       sync.Mutex
}

// NewConsulHTTPService 向 Consul agent 注册 HTTP service + health check。
// 注册失败返回 error（调用方决定容忍策略——HTTP 服务本身照常监听）。
func NewConsulHTTPService(opt HTTPServiceOptions) (*ConsulHTTPService, error) {
	if opt.ConsulAddress == "" {
		addrs := tgf.GetStrListConfig(tgf.EnvironmentConsulAddress)
		if len(addrs) == 0 || addrs[0] == "" {
			return nil, fmt.Errorf("ConsulAddress 未配置,无法注册 HTTP service")
		}
		opt.ConsulAddress = addrs[0]
	}

	reg, interval, err := buildHTTPServiceRegistration(opt)
	if err != nil {
		return nil, err
	}

	cli, err := api.NewClient(&api.Config{Address: opt.ConsulAddress})
	if err != nil {
		return nil, fmt.Errorf("创建 consul api client 失败: %w", err)
	}
	h := &ConsulHTTPService{
		agent:    cli.Agent(),
		reg:      reg,
		checkID:  reg.Check.CheckID,
		interval: interval,
		ttlMode:  reg.Check.TTL != "",
	}
	if err = h.agent.ServiceRegister(reg); err != nil {
		return nil, fmt.Errorf("注册 consul HTTP service 失败 consul=%v service=%v: %w",
			opt.ConsulAddress, reg.ID, err)
	}
	log.InfoTag("health", "Consul HTTP service 注册成功 service=%v addr=%v:%v ttlMode=%v dereg=%v",
		reg.ID, reg.Address, reg.Port, h.ttlMode, reg.Check.DeregisterCriticalServiceAfter)
	return h, nil
}

// NeedsRenew 返回该注册是否需要框架侧周期续约（TTL 模式 true / HTTP 模式 false）。
func (h *ConsulHTTPService) NeedsRenew() bool {
	return h.ttlMode
}

// Renew 续约 TTL check（TTL 模式下由续约 goroutine 每 tick 调用；HTTP 模式 no-op）。
// 自愈语义同 F2（health.go）：UpdateTTL 失败最常见根因是 Consul agent 重启后
// 丢失本地 check——重新 ServiceRegister 再续约一次，仍失败才返回 error
// （下个 tick 自然重试）。
func (h *ConsulHTTPService) Renew(note string) error {
	if !h.ttlMode {
		return nil
	}
	err := h.agent.UpdateTTL(h.checkID, note, api.HealthPassing)
	if err == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if rerr := h.agent.ServiceRegister(h.reg); rerr != nil {
		return fmt.Errorf("HTTP service TTL 续约失败且重注册失败: 续约=%v / 重注册=%w", err, rerr)
	}
	if err2 := h.agent.UpdateTTL(h.checkID, note, api.HealthPassing); err2 != nil {
		return fmt.Errorf("HTTP service 重注册后 TTL 续约仍失败: %w", err2)
	}
	log.WarnTag("health", "Consul HTTP service check 丢失后已自愈重注册 service=%v (原错误: %v)", h.reg.ID, err)
	return nil
}

// Deregister 从 Consul agent 摘除该 HTTP service（连带 check）。
// 优雅停机路径调用：停机瞬间从发现结果消失，不等 TTL/探测周期。幂等。
func (h *ConsulHTTPService) Deregister() error {
	return h.agent.ServiceDeregister(h.reg.ID)
}

// Interval 返回归一化后的续约/探测周期（rpc 侧续约 goroutine 对齐 ticker）。
func (h *ConsulHTTPService) Interval() time.Duration {
	return h.interval
}

// ServiceID 返回注册的 service ID（停机日志 / 集成测试断言用）。
func (h *ConsulHTTPService) ServiceID() string {
	return h.reg.ID
}

// CheckID 返回注册的 check ID（集成测试断言用）。
func (h *ConsulHTTPService) CheckID() string {
	return h.checkID
}
