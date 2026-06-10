package rpc

// E3 链路追踪贯通的端到端测试。
//
// v3 审计（P2 两套体系断链）：实际 RPC 链路的 trace id 在 rpcx share.Context 的
// ReqMetaData["TraceId"]（网关 StartReq 每请求生成、随 rpcx 协议透传），而 trace
// 包原先只认私有 context key——服务内 StartSpan 会另起炉灶生成新 id，跨进程
// 父子关系不存在。E3 用鸭子类型桥接（trace.metaReader/metaWriter ↔ share.Context
// 的 GetReqMetaDataByKey/SetReqMetaData）打通两套体系，本文件证明：
//
//   1. 真实 rpcx 协议链路：客户端（网关侧）ctx 写入 TraceId → rpcx wire 透传 →
//      服务端 handler 内 trace.StartSpan 拿到**同一个** trace id；
//   2. 网关单进程直通链路：doLogic→sendMessage→localGateDispatch 同样贯通；
//   3. WithTraceID 对 share.Context 原地写 meta，不破坏类型断言。

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/trace"
	"golang.org/x/net/context"
)

// traceProbeService 在 handler 内启 span 并记录拿到的 trace id。
type traceProbeService struct {
	Module
	spanTraceID atomic.Value // string：handler 内 StartSpan 的 span.TraceID()
	ctxTraceID  atomic.Value // string：handler 内 TraceIDFromContext 的读取值
}

func (s *traceProbeService) Startup() (bool, error) { return true, nil }

func (s *traceProbeService) Probe(ctx context.Context, args *DefaultArgs, reply *DefaultReply) error {
	sctx, span := trace.StartSpan(ctx, "traceProbe.Probe")
	defer span.End()
	s.spanTraceID.Store(span.TraceID())
	s.ctxTraceID.Store(trace.TraceIDFromContext(sctx))
	reply.C = 1
	return nil
}

// TestTraceE2E_RPCXMetaDataPropagation 是 E3 的核心验收：一条请求经真实 rpcx
// 协议从客户端到服务端，handler 内 StartSpan 的 trace id 与发起方在 ReqMetaData
// 写入的 id 完全一致——"网关→后端 service 全链路同一个 traceId"成立。
func TestTraceE2E_RPCXMetaDataPropagation(t *testing.T) {
	svc := &traceProbeService{Module: Module{Name: "traceProbe", Version: "1.0"}}
	addr, shutdown := startTestRPCXServer(t, "traceProbe", svc)
	defer shutdown()
	xc := newP2PXClient(t, "traceProbe", addr)
	defer func() { _ = xc.Close() }()

	// 模拟网关 StartReq：把 trace id 写进连接 ctx 的 ReqMetaData。
	const wantTraceID = "gw-snowflake-e2e-1"
	ctx := share.NewContext(context.Background())
	meta := map[string]string{
		tgf.ContextKeyTRACEID: wantTraceID,
		tgf.ContextKeyUserId:  "u-trace",
	}
	ctx.SetValue(share.ReqMetaDataKey, meta)

	if err := xc.Call(ctx, "Probe", &DefaultArgs{C: "ping"}, &DefaultReply{}); err != nil {
		t.Fatalf("rpcx call: %v", err)
	}

	if got, _ := svc.spanTraceID.Load().(string); got != wantTraceID {
		t.Fatalf("服务端 span.TraceID = %q, want %q（trace 包应桥接 rpcx ReqMetaData）", got, wantTraceID)
	}
	if got, _ := svc.ctxTraceID.Load().(string); got != wantTraceID {
		t.Fatalf("服务端 TraceIDFromContext = %q, want %q", got, wantTraceID)
	}
}

// TestTraceE2E_GatewayLocalDispatch 验证单进程网关链路（doLogic 的真实调用入口
// sendMessage → localGateDispatch）同样贯通：连接 meta 里的 TraceId 与 handler
// 内 span 的 id 一致。
func TestTraceE2E_GatewayLocalDispatch(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	svc := &traceProbeService{Module: Module{Name: "traceLocal", Version: "1.0"}}
	localDispatcher.Register("traceLocal", svc)
	localDispatchEnabled.Store(true)

	const wantTraceID = "gw-local-trace-7"
	ct := newBareConnectData()
	ct.userId = "u-local-trace"
	// 模拟网关 doLogic 前置的 StartReq（tcp.go:StartReq 同款写法）。
	ct.contextData.SetReqMetaData(tgf.ContextKeyTRACEID, wantTraceID)

	if err := sendMessage(ct, "traceLocal", "Probe", &DefaultArgs{C: "ping"}, &DefaultReply{}); err != nil {
		t.Fatalf("sendMessage: %v", err)
	}

	if got, _ := svc.spanTraceID.Load().(string); got != wantTraceID {
		t.Fatalf("本地直通链路 span.TraceID = %q, want %q", got, wantTraceID)
	}
}

// TestTraceE2E_StartReqGeneratesAndPropagates 验证网关真实的 StartReq 入口：
// StartReq 写入的 snowflake trace id 能被 trace.TraceIDFromContext 桥接读到
// （即 doLogic 链路上任何下游用 trace 包都能拿到本请求的 id）。
func TestTraceE2E_StartReqGeneratesAndPropagates(t *testing.T) {
	ct := newBareConnectData()
	ct.StartReq()

	tid := trace.TraceIDFromContext(ct.GetContextData())
	if tid == "" {
		t.Fatal("StartReq 之后 TraceIDFromContext 应能桥接读到 trace id")
	}
	if got := ct.GetContextData().GetReqMetaDataByKey(tgf.ContextKeyTRACEID); got != tid {
		t.Fatalf("桥接读取值应与 ReqMetaData 一致: %q vs %q", tid, got)
	}
}

// TestWithTraceID_ShareContextNotWrapped 验证 WithTraceID 对 *share.Context
// 原地写 meta 并返回原 ctx——绝不包装（包装会破坏 rpcx 的类型断言透传）。
func TestWithTraceID_ShareContextNotWrapped(t *testing.T) {
	sc := share.NewContext(context.Background())
	out := trace.WithTraceID(sc, "tid-share-1")
	if out != context.Context(sc) {
		t.Fatalf("share.Context 应原地写入并返回原 ctx, got %T", out)
	}
	if got := sc.GetReqMetaDataByKey(tgf.ContextKeyTRACEID); got != "tid-share-1" {
		t.Fatalf("ReqMetaData 应被写入, got %q", got)
	}
	// 仍是 *share.Context：rpcx/tgf 的类型断言链路不受影响。
	if _, ok := out.(*share.Context); !ok {
		t.Fatalf("返回 ctx 必须仍是 *share.Context")
	}
}

// TestTraceE2E_SpanEndDoesNotBlock 冒烟：span 在网关链路上反复创建/结束零成本
// 不出错（NoOp 路径），保证埋点可以无顾虑地放在热路径。
func TestTraceE2E_SpanEndDoesNotBlock(t *testing.T) {
	ct := newBareConnectData()
	ct.StartReq()
	deadline := time.Now().Add(200 * time.Millisecond)
	n := 0
	for time.Now().Before(deadline) && n < 10000 {
		_, span := trace.StartSpan(ct.GetContextData(), "hot")
		span.End()
		n++
	}
	if n == 0 {
		t.Fatal("热路径 span 创建不应被阻塞")
	}
}
