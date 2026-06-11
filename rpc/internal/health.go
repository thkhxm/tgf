package internal

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/log"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description F2 · Consul agent TTL health check（A6 心跳骨架的真实接线）
//
// 背景（v3 审计2/审计8）：A6 的心跳 goroutine 只打日志 + 维护 atomic 位，
// Consul 侧没有任何真实健康表示——节点 kill -9 后只有 rpcx-consul 的 KV
// session TTL 兜底，运维在 Consul 看不到"这个节点不健康"的一手信号。
//
// 本文件用 hashicorp/consul/api 把心跳接成真实的 Consul agent TTL check：
//   - 节点注册为一个 agent service（AgentServiceRegistration），携带 TTL 检查
//     （AgentServiceCheck.TTL）；
//   - Server 的心跳 goroutine（rpcserver.go startHealthCheckLoop）每 tick 调
//     Renew → agent.UpdateTTL 续约；
//   - 进程 kill / 宕机后停止续约 → Consul 在 TTL 内把检查置为 critical，
//     并按 DeregisterCriticalServiceAfter 自动摘除该 service；
//   - 优雅停机（Server.Destroy → unregisterFromConsul）调 Deregister 立即摘除。
//
// 协议依据（按 §2.8 规范：以本仓库锁定的 consul/api v1.8.1 源码为准核对，
// 而非凭记忆——agent.go ServiceRegister/ServiceDeregister/UpdateTTL）：
//   - TTL check 文档: https://developer.hashicorp.com/consul/docs/services/usage/checks#ttl-checks
//   - UpdateTTL API:  https://developer.hashicorp.com/consul/api-docs/agent/check#ttl-check-update
//   - DeregisterCriticalServiceAfter 的 Consul 服务端硬下限是 1 分钟
//     （低于 1m 的值会被 Consul 按 1m 处理），reaper 周期 30s。
//
//2026/6/10
//***************************************************

const (
	// DefaultTTLHealthInterval 是 Server 未显式 WithHealthCheck 时的默认续约周期。
	// TTL = 3×interval（见 buildRegistration），即默认 15s 内 Consul 必然发现死节点。
	DefaultTTLHealthInterval = 5 * time.Second

	// ttlFactor TTL 与续约周期的倍率。3 倍意味着连续丢 2 次续约仍不会误判，
	// 第 3 次丢失（≈ 真实宕机）才转 critical——兼顾灵敏与抗抖动。
	ttlFactor = 3

	// minDeregisterCriticalAfter Consul 对 DeregisterCriticalServiceAfter 的硬下限。
	minDeregisterCriticalAfter = time.Minute
)

// TTLHealthOptions 是 NewConsulTTLHealth 的输入。
// 零值字段按生产默认解析（见 buildRegistration / NewConsulTTLHealth）。
type TTLHealthOptions struct {
	// ServiceAddress 节点 rpcx 监听地址，"host:port"（容忍 "tcp@host:port"）。必填。
	ServiceAddress string
	// Modules 本节点已注册到服务发现的逻辑模块名，作为 service tags 暴露。
	Modules []string
	// Interval 续约周期。<=0 → DefaultTTLHealthInterval。
	Interval time.Duration
	// TTL 显式覆盖检查 TTL（集成测试用小值）。<=0 → Interval×3。
	// 显式给定时必须 > Interval，否则正常续约都来不及。
	TTL time.Duration
	// DeregisterCriticalAfter 检查转 critical 后多久自动摘除 service。
	// <=0 或小于 1m → 取 Consul 硬下限 1m。
	DeregisterCriticalAfter time.Duration
	// ConsulAddress 显式覆盖 Consul HTTP 地址（测试用）。空 → 读配置 ConsulAddress 第一项。
	ConsulAddress string
	// BasePath 显式覆盖注册根路径（测试用）。空 → 读配置 ConsulPath。
	BasePath string
}

// buildRegistration 校验 options 并组装 AgentServiceRegistration。
// 纯函数（不触网），字段映射逻辑由单元测试锁定（health_test.go）。
func buildRegistration(opt TTLHealthOptions) (*api.AgentServiceRegistration, time.Duration, error) {
	addr := strings.TrimPrefix(opt.ServiceAddress, "tcp@")
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, 0, fmt.Errorf("ServiceAddress 不是合法的 host:port (%q): %w", opt.ServiceAddress, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, 0, fmt.Errorf("ServiceAddress 端口非数字 (%q): %w", portStr, err)
	}

	interval := opt.Interval
	if interval <= 0 {
		interval = DefaultTTLHealthInterval
	}
	ttl := opt.TTL
	if ttl <= 0 {
		ttl = interval * ttlFactor
	}
	if ttl <= interval {
		return nil, 0, fmt.Errorf("TTL(%v) 必须大于续约周期(%v)，否则正常续约都会被判 critical", ttl, interval)
	}
	dereg := opt.DeregisterCriticalAfter
	if dereg < minDeregisterCriticalAfter {
		// Consul 服务端硬下限 1m，低于会被静默按 1m 处理——这里显式归一，
		// 让日志/注册内容与真实生效值一致。
		dereg = minDeregisterCriticalAfter
	}

	base := strings.Trim(opt.BasePath, "/")
	if base == "" {
		base = "tgf"
	}

	serviceID := fmt.Sprintf("%s-%s:%d", base, host, port)
	reg := &api.AgentServiceRegistration{
		ID:      serviceID,
		Name:    base,
		Address: host,
		Port:    port,
		Tags:    opt.Modules,
		Meta:    map[string]string{"nodeId": tgf.NodeId},
		Check: &api.AgentServiceCheck{
			CheckID: serviceID + "-ttl",
			Name:    "tgf node TTL heartbeat",
			TTL:     ttl.String(),
			// 注册即 passing：节点此刻已 Serve 就绪（F2 时序保证注册发生在监听之后），
			// 不需要等第一次续约才转健康。
			Status: api.HealthPassing,
			// critical 后由 Consul reaper 自动摘除（兜底 kill -9 等无法优雅停机的死法）。
			DeregisterCriticalServiceAfter: dereg.String(),
		},
	}
	return reg, interval, nil
}

// ConsulTTLHealth 持有一个已注册的 Consul agent TTL check。
// 生命周期：NewConsulTTLHealth（注册）→ 心跳 goroutine 周期 Renew（续约/自愈）
// → Deregister（优雅停机摘除）。Renew/Deregister 并发安全（mu 保护重注册分支）。
type ConsulTTLHealth struct {
	agent    *api.Agent
	reg      *api.AgentServiceRegistration
	checkID  string
	interval time.Duration
	mu       sync.Mutex
}

// NewConsulTTLHealth 向 Consul agent 注册本节点 service + TTL check。
// 注册失败返回 error（调用方决定是否容忍——KV 注册有独立的 session TTL 兜底）。
func NewConsulTTLHealth(opt TTLHealthOptions) (*ConsulTTLHealth, error) {
	if opt.ConsulAddress == "" {
		addrs := tgf.GetStrListConfig(tgf.EnvironmentConsulAddress)
		if len(addrs) == 0 || addrs[0] == "" {
			return nil, fmt.Errorf("ConsulAddress 未配置,无法注册 TTL health check")
		}
		opt.ConsulAddress = addrs[0]
	}
	if opt.BasePath == "" {
		opt.BasePath = tgf.GetStrConfig[string](tgf.EnvironmentConsulPath)
	}

	reg, interval, err := buildRegistration(opt)
	if err != nil {
		return nil, err
	}

	cli, err := api.NewClient(&api.Config{Address: opt.ConsulAddress})
	if err != nil {
		return nil, fmt.Errorf("创建 consul api client 失败: %w", err)
	}
	h := &ConsulTTLHealth{
		agent:    cli.Agent(),
		reg:      reg,
		checkID:  reg.Check.CheckID,
		interval: interval,
	}
	if err = h.agent.ServiceRegister(reg); err != nil {
		return nil, fmt.Errorf("注册 consul TTL health service 失败 consul=%v service=%v: %w",
			opt.ConsulAddress, reg.ID, err)
	}
	log.InfoTag("health", "Consul TTL health check 注册成功 service=%v check=%v ttl=%v dereg=%v",
		reg.ID, h.checkID, reg.Check.TTL, reg.Check.DeregisterCriticalServiceAfter)
	return h, nil
}

// Renew 续约 TTL check（心跳 goroutine 每 tick 调用）。
// 自愈语义：UpdateTTL 失败最常见的根因是 Consul agent 重启后丢失了本地 check
// （agent 重启不持久化 TTL check）——此时重新 ServiceRegister 再续约一次；
// 仍失败才向调用方返回 error（下个 tick 自然重试，与 KV 刷新 goroutine 的
// re-create 行为对齐）。
func (h *ConsulTTLHealth) Renew(note string) error {
	err := h.agent.UpdateTTL(h.checkID, note, api.HealthPassing)
	if err == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if rerr := h.agent.ServiceRegister(h.reg); rerr != nil {
		return fmt.Errorf("TTL 续约失败且重注册失败: 续约=%v / 重注册=%w", err, rerr)
	}
	if err2 := h.agent.UpdateTTL(h.checkID, note, api.HealthPassing); err2 != nil {
		return fmt.Errorf("重注册后 TTL 续约仍失败: %w", err2)
	}
	log.WarnTag("health", "Consul TTL check 丢失后已自愈重注册 service=%v (原错误: %v)", h.reg.ID, err)
	return nil
}

// Deregister 从 Consul agent 摘除本节点 service（连带 TTL check）。
// 优雅停机路径调用：停机瞬间健康状态即从 Consul 消失，不等 TTL 过期。幂等。
func (h *ConsulTTLHealth) Deregister() error {
	return h.agent.ServiceDeregister(h.reg.ID)
}

// Interval 返回解析后的续约周期（Server 用它对齐心跳 ticker）。
func (h *ConsulTTLHealth) Interval() time.Duration {
	return h.interval
}

// ServiceID 返回注册的 service ID（集成测试断言用）。
func (h *ConsulTTLHealth) ServiceID() string {
	return h.reg.ID
}

// CheckID 返回注册的 check ID（集成测试断言用）。
func (h *ConsulTTLHealth) CheckID() string {
	return h.checkID
}
