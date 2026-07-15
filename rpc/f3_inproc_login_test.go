package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F3 · 进程内登录协调器（单进程/无 Redis）与并发登录互斥测试
//
//   1. inProcLoginCoordinator 本体语义：互斥、超时、释放幂等、owner CRUD；
//   2. defaultLoginCoordinator 在无 Redis 配置时按方法委托进程内实现（D 遗留收尾）；
//   3. 端到端：纯单进程无 Redis 走真实 gate.Login + 真实 TCPServer 可登录、
//      重复登录踢旧无双在线；
//   4. 验收口径（路线图 F3）：双节点并发登录 1000 轮无双在线——
//      真实 gate.Login 编排 + 真实进程内锁/owner 语义 + 同步 ack 踢人路由。
//
//2026/6/10
//***************************************************

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"context"
)

func newF3InProcCoord() *inProcLoginCoordinator {
	return &inProcLoginCoordinator{
		sems:   make(map[string]chan struct{}),
		owners: make(map[string]string),
	}
}

// TestF3_InProcLock_MutexAndIdempotentRelease 验证进程内锁的互斥 + 超时 + 释放幂等。
func TestF3_InProcLock_MutexAndIdempotentRelease(t *testing.T) {
	origWait := inProcLoginLockWait
	inProcLoginLockWait = 30 * time.Millisecond
	defer func() { inProcLoginLockWait = origWait }()

	c := newF3InProcCoord()
	const uid = "f3-inproc-mutex-u"

	h1, err := c.AcquireLoginLock(uid)
	if err != nil {
		t.Fatalf("首次取锁失败: %v", err)
	}
	// 持锁期间第二次获取必须超时失败(互斥)。
	if _, err2 := c.AcquireLoginLock(uid); err2 == nil {
		t.Fatalf("持锁期间第二次获取不应成功")
	}
	// 释放幂等:重复释放不 panic、不破坏信号量。
	c.ReleaseLoginLock(h1)
	c.ReleaseLoginLock(h1)
	h3, err3 := c.AcquireLoginLock(uid)
	if err3 != nil {
		t.Fatalf("释放后重取失败: %v", err3)
	}
	c.ReleaseLoginLock(h3)
}

// TestF3_InProcOwner_CompareAndClear 验证 owner CRUD 与比对清理语义：
// 地址不符不删（防误删新 owner），地址相符才删。
func TestF3_InProcOwner_CompareAndClear(t *testing.T) {
	c := newF3InProcCoord()
	const uid = "f3-inproc-owner-u"

	if got := c.GetGateOwner(uid); got != "" {
		t.Fatalf("初始 owner 应为空, got %q", got)
	}
	c.SetGateOwner(uid, "tcp@nodeA:1")
	if got := c.GetGateOwner(uid); got != "tcp@nodeA:1" {
		t.Fatalf("owner 读写不一致, got %q", got)
	}
	// 比对不符 → 不删
	c.ClearGateOwner(uid, "tcp@nodeB:2")
	if got := c.GetGateOwner(uid); got != "tcp@nodeA:1" {
		t.Fatalf("地址不符的 ClearGateOwner 不应删除 owner, got %q", got)
	}
	// 比对相符 → 删除
	c.ClearGateOwner(uid, "tcp@nodeA:1")
	if got := c.GetGateOwner(uid); got != "" {
		t.Fatalf("地址相符的 ClearGateOwner 应删除 owner, got %q", got)
	}
}

// withNoRedisLoginBackend 强制 defaultLoginCoordinator 走进程内路径（覆盖配置探测）。
func withNoRedisLoginBackend(t *testing.T) func() {
	t.Helper()
	orig := loginRedisAvailable
	loginRedisAvailable = func() bool { return false }
	return func() { loginRedisAvailable = orig }
}

// TestF3_DefaultCoordinator_DelegatesToInProcWithoutRedis 验证生产协调器在无
// Redis 配置时的按方法委托：取到的是进程内句柄、owner 落在进程内表、释放可重取。
func TestF3_DefaultCoordinator_DelegatesToInProcWithoutRedis(t *testing.T) {
	defer withNoRedisLoginBackend(t)()
	d := &defaultLoginCoordinator{}
	const uid = "f3-delegate-u"

	h, err := d.AcquireLoginLock(uid)
	if err != nil {
		t.Fatalf("无 Redis 时取锁应委托进程内实现, err = %v", err)
	}
	if _, ok := h.(*inProcLockHandle); !ok {
		t.Fatalf("无 Redis 时应返回进程内锁句柄, got %T", h)
	}
	d.SetGateOwner(uid, "tcp@solo:1")
	if got := d.GetGateOwner(uid); got != "tcp@solo:1" {
		t.Fatalf("owner 应落在进程内表并可读回, got %q", got)
	}
	d.ClearGateOwner(uid, "tcp@solo:1")
	if got := d.GetGateOwner(uid); got != "" {
		t.Fatalf("ClearGateOwner 委托失败, got %q", got)
	}
	d.ReleaseLoginLock(h)
	h2, err := d.AcquireLoginLock(uid)
	if err != nil {
		t.Fatalf("释放后重取失败: %v", err)
	}
	d.ReleaseLoginLock(h2)
}

// TestF3_SingleProcessNoRedis_LoginEndToEnd 是 D 遗留收尾的端到端用例：
// 纯单进程、无 Redis，走真实 defaultLoginCoordinator（自动降级进程内锁）+
// 真实 gate.Login + 真实 TCPServer——可登录、可重复登录踢旧、无双在线。
func TestF3_SingleProcessNoRedis_LoginEndToEnd(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withNoRedisLoginBackend(t)()

	// 换上真实生产协调器(其它用例可能换过 fake)。
	origCoord := loginCoord
	loginCoord = &defaultLoginCoordinator{}
	defer func() { loginCoord = origCoord }()

	srv := newTestServer()
	g := &GateService{tcpService: srv}
	const uid = "f3-single-proc-u"

	// 首次登录
	mock1, done1, tpl1 := startF3TestConn(t, srv)
	reply1 := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: uid, TemplateUserId: tpl1}, reply1); err != nil {
		t.Fatalf("单进程无 Redis 登录失败: %v", err)
	}
	if reply1.ResumeToken == "" {
		t.Fatalf("登录成功应签发 resume token")
	}
	// owner 写入进程内表,地址为单进程哨兵地址(localGateAddress 的空地址回退)。
	if got := inProcLoginCoord.GetGateOwner(uid); got != "local@in-proc" {
		t.Fatalf("单进程 owner 应为哨兵地址 local@in-proc, got %q", got)
	}

	// 重复登录 → 本地 owner 命中 → 踢旧会话,无双在线。
	mock2, done2, tpl2 := startF3TestConn(t, srv)
	defer func() {
		_ = mock2.Close()
		<-done2
	}()
	reply2 := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: uid, TemplateUserId: tpl2}, reply2); err != nil {
		t.Fatalf("重复登录失败: %v", err)
	}

	// 旧连接被踢:收到 ReplaceLogin 通知 + handleConn 退出。
	gotNotify := false
	for i := 0; i < 4 && !gotNotify; i++ {
		select {
		case w := <-mock1.writes:
			if bytes.Equal(w, replaceLoginData) {
				gotNotify = true
			}
		case <-time.After(time.Second):
		}
	}
	if !gotNotify {
		t.Fatalf("旧连接未收到 ReplaceLogin 通知")
	}
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatalf("旧连接 handleConn 未退出(双在线?)")
	}

	// users 表中 uid 只对应一个在线会话(新会话)。
	cur, ok := srv.users.Get(uid)
	if !ok {
		t.Fatalf("新会话应在线")
	}
	if curUCD, isUCD := cur.(*UserConnectData); !isUCD || curUCD.conn != IConn(mock2) {
		t.Fatalf("在线会话应是新连接")
	}
}

// ============================================================================
// F3 验收：双节点并发登录 1000 轮无双在线
//
// 同一进程内模拟两个 gate 节点：
//   - 共享 f3ClusterCoord：真实 inProcLoginCoordinator 的锁/owner 语义
//     （等价共享 Redis），KickRemoteOwner 路由到目标节点并同步完成清理（ack）；
//   - 每个节点是真实 GateService.Login 编排 + f3ClusterTCP（在线表 + 互斥不变量
//     校验的 ITCPService）；
//   - 1000 轮、每轮两节点并发登录同一 uid；DoLogin 时刻若另一节点仍在线即记违例。
// ============================================================================

type f3Cluster struct {
	mu         sync.Mutex
	online     map[string]map[string]bool // nodeAddr -> uid 在线集合
	violations int
	nodes      map[string]*f3ClusterTCP // nodeAddr -> 节点网络层
}

type f3ClusterTCP struct {
	addr    string
	cluster *f3Cluster
}

func (s *f3ClusterTCP) Run() {}

func (s *f3ClusterTCP) UpdateUserNodeInfo(userId, servicePath, nodeId string) bool { return true }

func (s *f3ClusterTCP) ToUser(userId, messageType string, data []byte) error { return nil }

func (s *f3ClusterTCP) DoLogin(userId, templateUserId, resumeToken string) (string, error) {
	c := s.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	// 互斥不变量:登录落地时刻,其它节点不得仍有该 uid 在线。
	for addr, set := range c.online {
		if addr != s.addr && set[userId] {
			c.violations++
		}
	}
	c.online[s.addr][userId] = true
	return "cluster-token", nil
}

func (s *f3ClusterTCP) Offline(userId string, replace bool) bool {
	c := s.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.online[s.addr][userId] {
		delete(c.online[s.addr], userId)
		return true
	}
	return false
}

func (s *f3ClusterTCP) CloseListeners() error { return nil }

// f3ClusterCoord 复用真实进程内锁/owner 语义（等价共享 Redis），
// 仅把跨节点踢路由到目标节点的网络层——与生产同步 ack 语义一致：
// 返回即目标节点清理完成。
type f3ClusterCoord struct {
	*inProcLoginCoordinator
	cluster *f3Cluster
}

func (c *f3ClusterCoord) KickRemoteOwner(address, userId string) error {
	n, ok := c.cluster.nodes[address]
	if !ok {
		return fmt.Errorf("unknown node %s", address)
	}
	n.Offline(userId, true)
	return nil
}

func TestF3_DualNodeConcurrentLogin_1000Rounds_NoDoubleOnline(t *testing.T) {
	defer withLoginCheckDisabled(t)()

	cluster := &f3Cluster{
		online: map[string]map[string]bool{
			"tcp@nodeA:1": {},
			"tcp@nodeB:2": {},
		},
		nodes: map[string]*f3ClusterTCP{},
	}
	tcpA := &f3ClusterTCP{addr: "tcp@nodeA:1", cluster: cluster}
	tcpB := &f3ClusterTCP{addr: "tcp@nodeB:2", cluster: cluster}
	cluster.nodes[tcpA.addr] = tcpA
	cluster.nodes[tcpB.addr] = tcpB

	coord := &f3ClusterCoord{inProcLoginCoordinator: newF3InProcCoord(), cluster: cluster}
	defer withFakeLoginCoord(t, coord)()

	gateA := &GateService{tcpService: tcpA, localAddressFn: func() string { return tcpA.addr }}
	gateB := &GateService{tcpService: tcpB, localAddressFn: func() string { return tcpB.addr }}

	const uid = "f3-dual-node-u"
	const rounds = 1000

	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		for _, g := range []*GateService{gateA, gateB} {
			g := g
			go func() {
				defer wg.Done()
				reply := &LoginRes{}
				if err := g.Login(context.Background(),
					&LoginReq{UserId: uid, TemplateUserId: "tpl"}, reply); err != nil {
					t.Errorf("round %d: Login err = %v", i, err)
				}
			}()
		}
		wg.Wait()

		// 每轮结束:恰好一个节点在线。
		cluster.mu.Lock()
		onlineCount := 0
		for _, set := range cluster.online {
			if set[uid] {
				onlineCount++
			}
		}
		violations := cluster.violations
		cluster.mu.Unlock()
		if onlineCount != 1 {
			t.Fatalf("round %d: 在线节点数 = %d, want 1(双在线或全掉线)", i, onlineCount)
		}
		if violations != 0 {
			t.Fatalf("round %d: 检测到双在线违例 %d 次", i, violations)
		}
	}
}
