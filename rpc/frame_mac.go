package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description F5 帧级防伪：会话密钥对客户端上行帧加 MAC（与 D7 登录鉴权联动）
//2026/6/10
//***************************************************

// 背景（v3 审计 P2"客户端可向任意 module.method 发起调用 + 非 TLS/非 AEAD 通道
// 无帧级鉴真"）：裸 TCP / 明文 KCP 通道上，能注入字节流的攻击者可以伪造任意
// 已登录会话的业务请求（登录后网关只认连接不再认帧）。本文件提供帧级 MAC：
//
// 设计：
//   - 会话密钥：DoLogin 成功签发 resume token（F3，经 LoginRes 由业务转交客户端，
//     双方独有）→ 两端各自派生 macKey = HMAC-SHA256(token, "tgf/frame-mac/v1")。
//     每次登录都签发新 token，密钥随会话轮换。
//   - 帧格式（LogicMAC，message type=4，在 Logic 帧基础上扩展）：
//     [1:magic=250][1:type=4][2:methodSize][2:dataSize][8:seq BE][n:method][n:data][16:mac]
//     mac = HMAC-SHA256(macKey, seq(8B BE) || method || data) 截断 16 字节。
//   - 防重放：seq 必须严格递增（服务端记录已验证的最大 seq）；MAC 覆盖 seq，
//     捕获的合法帧无法原样重放，也无法改 seq 重放。
//   - 启用范围：客户端→服务端的业务帧（Logic）。心跳帧无业务语义不要求 MAC；
//     下行帧不加 MAC（伪造下行需先劫持服务端连接，属传输层安全范畴）。
//   - 传输豁免：WS 帧是 WSMessage proto（无 MAC 字段位，浏览器场景请用 WSS）；
//     KCP+AEAD 自带认证加密。两者通过 frameMACPolicy 可选接口豁免，
//     裸 TCP 与明文 KCP 强制执行。
//   - 开关：默认随登录鉴权开启（WithoutLoginCheck 关闭鉴权时 MAC 一并关闭），
//     可用 SetFrameMACMode 强制开/关。
//
// 安全边界（诚实声明）：会话密钥派生自 resume token，而 token 经 LoginRes →
// 业务服务 → 客户端的链路若全程明文，被动窃听者可还原密钥。帧级 MAC 防的是
// "盲注入/跨会话伪造/重放"，不防全链路被动窃听 + 主动 MITM——后者请用
// WSS / KCP+AEAD / TLS。这与审计建议（"对非 TLS/非 AEAD 通道至少提供可选的
// 帧级 MAC(基于会话密钥)"）的定位一致。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"sync/atomic"
)

const (
	// FrameMACSize 是帧尾 MAC 的字节数（HMAC-SHA256 截断 16 字节 / 128-bit，
	// 与 IPsec/TLS 常用截断长度一致）。
	FrameMACSize = 16
	// frameMACSeqSize 是防重放序号字段的字节数（uint64 big-endian）。
	frameMACSeqSize = 8
	// frameMACKeyLabel 是密钥派生标签（域分离：resume token 还用于重连续联）。
	frameMACKeyLabel = "tgf/frame-mac/v1"
)

// FrameMACMode 是帧级 MAC 的开关模式。
type FrameMACMode int32

const (
	// FrameMACAuto（默认）：随登录鉴权开启——D7 鉴权开启（默认）时 MAC 同步开启，
	// WithoutLoginCheck 显式关闭鉴权时 MAC 一并关闭。
	FrameMACAuto FrameMACMode = iota
	// FrameMACOn 强制开启（即使鉴权被关闭）。
	FrameMACOn
	// FrameMACOff 强制关闭（兼容窗口：客户端尚未支持 MAC 帧时使用）。
	FrameMACOff
)

// frameMACMode 是进程级开关（与登录鉴权配置同性质的 package 级全局，理由见
// login_check.go 的"包级鉴权状态"注释）。
var frameMACMode atomic.Int32

// SetFrameMACMode 设置帧级 MAC 模式。默认 FrameMACAuto（随登录鉴权开启）。
func SetFrameMACMode(mode FrameMACMode) {
	frameMACMode.Store(int32(mode))
}

// frameMACActive 返回当前是否启用帧级 MAC。
func frameMACActive() bool {
	switch FrameMACMode(frameMACMode.Load()) {
	case FrameMACOn:
		return true
	case FrameMACOff:
		return false
	default:
		// Auto：随 D7 登录鉴权——鉴权开启（默认）即开启。
		return !loginCheckDisabled.Load()
	}
}

// frameMACPolicy 是 IConn 实现的可选接口：声明该连接是否要求帧级 MAC。
//   - tcpFramedConn → true（裸 TCP，无传输层鉴真）
//   - kcpFramedConn → !sealer.Enabled()（AEAD 已提供帧鉴真时豁免）
//   - wsFramedConn 不实现（WSMessage proto 无 MAC 字段位，浏览器场景用 WSS）
//   - 测试 mock 不实现 → 豁免（存量行为不变）
type frameMACPolicy interface {
	RequiresFrameMAC() bool
}

// connRequiresFrameMAC 判断连接是否需要帧级 MAC（conn 为 nil 或未声明则豁免）。
func connRequiresFrameMAC(conn IConn) bool {
	p, ok := conn.(frameMACPolicy)
	return ok && p.RequiresFrameMAC()
}

// DeriveFrameMACKey 从 resume token（LoginRes.ResumeToken）派生 32 字节会话 MAC
// 密钥。服务端 DoLogin 与客户端（robot / 业务客户端 SDK）必须使用同一派生算法。
func DeriveFrameMACKey(resumeToken string) []byte {
	mac := hmac.New(sha256.New, []byte(resumeToken))
	mac.Write([]byte(frameMACKeyLabel))
	return mac.Sum(nil)
}

// computeFrameMAC 计算一帧的 MAC：HMAC-SHA256(key, seq || methodName || data) 截断
// FrameMACSize 字节。methodName 是 "module.method" 完整方法名（与帧内编码一致）。
func computeFrameMAC(key []byte, seq uint64, methodName string, data []byte) []byte {
	var seqBytes [frameMACSeqSize]byte
	binary.BigEndian.PutUint64(seqBytes[:], seq)
	mac := hmac.New(sha256.New, key)
	mac.Write(seqBytes[:])
	mac.Write([]byte(methodName))
	mac.Write(data)
	return mac.Sum(nil)[:FrameMACSize]
}

// EncodeTgfBinaryMACFrame 构造一个带 MAC 的业务请求帧（LogicMAC）。
// 客户端侧（robot / 业务 SDK）在登录拿到 resume token 并 DeriveFrameMACKey 后，
// 用严格递增的 seq（从 1 开始）编码每一帧。服务端校验见 checkInboundFrameAuth。
func EncodeTgfBinaryMACFrame(module, method string, data []byte, seq uint64, key []byte) []byte {
	methodName := module + "." + method
	methodSize := uint16(len(methodName))
	dataSize := uint16(len(data))
	mac := computeFrameMAC(key, seq, methodName, data)

	buf := make([]byte, 0, int(requestHeadSize)+frameMACSeqSize+int(methodSize)+int(dataSize)+FrameMACSize)
	buf = append(buf, requestMagicNumber, byte(LogicMAC))
	buf = append(buf, byte(methodSize>>8), byte(methodSize))
	buf = append(buf, byte(dataSize>>8), byte(dataSize))
	var seqBytes [frameMACSeqSize]byte
	binary.BigEndian.PutUint64(seqBytes[:], seq)
	buf = append(buf, seqBytes[:]...)
	buf = append(buf, methodName...)
	buf = append(buf, data...)
	buf = append(buf, mac...)
	return buf
}

// checkInboundFrameAuth 是 handleConn reader 在分发 Logic 帧之前的强制鉴真点。
// 返回 false 表示帧未通过鉴真，调用方应断开连接（fail-closed）。
//
// 规则：
//  1. 会话已 Online 且持有 macKey（DoLogin 派生）→ 帧必须携带合法 MAC 且 seq
//     严格递增；缺失/伪造/重放一律拒绝。
//  2. 其余情况（未登录窗口、MAC 关闭、豁免传输）→ 放行无 MAC 帧；但若客户端
//     带了 MAC 而本会话无键可验，同样拒绝——静默忽略未经校验的 MAC 是假安全，
//     且能在两端开关不一致的配置错误时第一时间暴露。
//
// 并发与内存可见性：本函数只在该连接的 reader goroutine 调用；macKey 的写发生在
// DoLogin markOnline（atomic CAS, release）之前，这里先观测 state==Online（acquire）
// 再读 macKey；macLastSeq 仅 reader 读写。
func checkInboundFrameAuth(u *UserConnectData, frame *FrameIn) bool {
	if u.loadState() == sessionOnline && u.macKey != nil {
		if frame.MAC == nil {
			return false
		}
		// 防重放：seq 必须严格递增。
		if frame.Seq <= u.macLastSeq {
			return false
		}
		expect := computeFrameMAC(u.macKey, frame.Seq, frame.Module+"."+frame.Method, frame.Data)
		if !hmac.Equal(expect, frame.MAC) {
			return false
		}
		u.macLastSeq = frame.Seq
		return true
	}
	// 无键可验的 MAC 帧：拒绝（见函数注释规则 2）。
	return frame.MAC == nil
}
