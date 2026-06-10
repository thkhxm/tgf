package robot

// response_decoder.go 解析从 TCP/KCP 网关收到的服务端下行帧。
//
// 服务端下行帧有两个协议版本（F5 协议修缮，tgf/rpc/tcp.go ResponseHeader）：
//
// v2（服务端默认）——带 [magic=251][messageType] 引导头，字节流无歧义：
//
//	心跳:        [251][1]
//	逻辑响应:    [251][2][1:compress][4:code BE][2:methodSize BE][4:dataSize BE][method][data]
//	替换登录通知: [251][3]
//
// v1（legacy，服务端 UseLegacyResponseProtocol(true) 时）：
//
//	心跳:     [1]
//	逻辑响应: [1:compress][2:methodSize BE][4:dataSize BE][method][data]（无 code）
//	替换登录: [3]
//
// 注意 v1 的设计歧义（v3 审计 P1，也是 v2 存在的理由）：流式读取时首字节 0x01
// 既可能是心跳、也可能是 compress=1 的逻辑响应——ReadServerFrame 的 v1 分支只能
// 按心跳优先解释（与历史客户端行为一致），压缩响应会脱轨。需要压缩下行的部署
// 必须使用 v2。KCP 通道因外层有长度前缀（DecodeServerFrame 整帧解码），
// v1 凭 len==1 可以规避该歧义。
//
// 本文件提供：
//   - DecodeServerFrame：整帧解码（KCP robot 用，外层 KCP 长度前缀已分帧）
//   - ReadServerFrame：流式解码（TCP robot 用，自带分帧）

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"

	util2 "github.com/thkhxm/rpcx/v2/util"
)

// 协议常量（与 tgf/rpc 包的 requestMagicNumber/responseMagicNumber 等保持一致；
// robot 是客户端视角，独立声明避免依赖服务端非导出符号）。
const (
	requestMagic  byte = 250 // 0xFA 请求帧引导字节
	responseMagic byte = 251 // 0xFB v2 下行帧引导字节
	msgHeartbeat  byte = 1
	msgLogic      byte = 2
	msgReplace    byte = 3

	// v2 逻辑响应头长度：magic(1)+type(1)+compress(1)+code(4)+methodSize(2)+dataSize(4)
	v2LogicHeadSize = 13
	// v1 逻辑响应头长度：compress(1)+methodSize(2)+dataSize(4)
	v1LogicHeadSize = 7
)

// ServerFrame 是从服务端收到的一帧解码后的结构。
type ServerFrame struct {
	IsHeartbeat bool
	// IsReplaceKick 表示这是"账号在别处登录,本连接即将被踢"的通知帧。
	IsReplaceKick bool
	MessageType   string // "module.method" 格式
	Data          []byte // 业务数据（已解压）
	// Code 是服务端回写的业务/系统错误码（仅 v2 协议携带；v1 恒为 0）。
	// 负数为框架系统码——rpc.CodeGateBusy(-429) 表示网关过载限流，应退避重试。
	Code int32
}

// decodeLogicBody 解码逻辑响应的 method/data 段并按需解压。
func decodeLogicBody(method, data []byte, compress byte) (*ServerFrame, error) {
	if compress == 1 && len(data) > 0 {
		unzipped, err := util2.Unzip(data)
		if err != nil {
			return nil, fmt.Errorf("robot: response unzip failed: %w", err)
		}
		data = unzipped
	}
	return &ServerFrame{MessageType: string(method), Data: data}, nil
}

// DecodeServerFrame 把一个完整的下行帧字节解码为 ServerFrame（v2 / v1 自适应）。
// KCP robot 在 readKCPFrame（外层长度前缀分帧）之后调用。
func DecodeServerFrame(raw []byte) (*ServerFrame, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("robot: empty frame")
	}

	switch raw[0] {
	case responseMagic: // v2
		if len(raw) < 2 {
			return nil, fmt.Errorf("robot: v2 frame too short: %d bytes", len(raw))
		}
		switch raw[1] {
		case msgHeartbeat:
			return &ServerFrame{IsHeartbeat: true}, nil
		case msgReplace:
			return &ServerFrame{IsReplaceKick: true}, nil
		case msgLogic:
			if len(raw) < v2LogicHeadSize {
				return nil, fmt.Errorf("robot: v2 response head truncated: %d bytes", len(raw))
			}
			compress := raw[2]
			code := int32(binary.BigEndian.Uint32(raw[3:7]))
			methodSize := int(binary.BigEndian.Uint16(raw[7:9]))
			dataSize := int(binary.BigEndian.Uint32(raw[9:13]))
			expected := v2LogicHeadSize + methodSize + dataSize
			if len(raw) < expected {
				return nil, fmt.Errorf("robot: v2 response truncated: have %d want %d", len(raw), expected)
			}
			sf, err := decodeLogicBody(raw[v2LogicHeadSize:v2LogicHeadSize+methodSize], raw[v2LogicHeadSize+methodSize:expected], compress)
			if err != nil {
				return nil, err
			}
			sf.Code = code
			return sf, nil
		default:
			return nil, fmt.Errorf("robot: v2 unknown message type %d", raw[1])
		}

	case msgHeartbeat: // v1 心跳——服务端发 []byte{1}
		if len(raw) == 1 {
			return &ServerFrame{IsHeartbeat: true}, nil
		}
		// len>1 且首字节 0x01：v1 的"compress=1 逻辑响应"与心跳歧义（审计 P1）。
		// KCP 整帧场景按逻辑响应解析（len==1 才是心跳，与历史行为一致）。
		return decodeV1Logic(raw)

	case msgReplace: // v1 替换登录通知——服务端发 []byte{3}
		if len(raw) == 1 {
			return &ServerFrame{IsReplaceKick: true}, nil
		}
		return nil, fmt.Errorf("robot: unexpected v1 frame leading 0x03, len=%d", len(raw))

	case requestMagic: // 请求格式回显（magic=250），如心跳回显 [250,1]
		if len(raw) >= 2 && raw[1] == msgHeartbeat {
			return &ServerFrame{IsHeartbeat: true}, nil
		}
		return nil, fmt.Errorf("robot: unexpected request-format echo, len=%d", len(raw))

	default: // v1 逻辑响应（compress=0）
		return decodeV1Logic(raw)
	}
}

// decodeV1Logic 解析 v1 逻辑响应 [compress][methodSize:2][dataSize:4][method][data]。
func decodeV1Logic(raw []byte) (*ServerFrame, error) {
	if len(raw) < v1LogicHeadSize {
		return nil, fmt.Errorf("robot: v1 response frame too short: %d bytes", len(raw))
	}
	compress := raw[0]
	methodSize := int(binary.BigEndian.Uint16(raw[1:3]))
	dataSize := int(binary.BigEndian.Uint32(raw[3:7]))
	expected := v1LogicHeadSize + methodSize + dataSize
	if len(raw) < expected {
		return nil, fmt.Errorf("robot: v1 response truncated: have %d want %d", len(raw), expected)
	}
	return decodeLogicBody(raw[v1LogicHeadSize:v1LogicHeadSize+methodSize], raw[v1LogicHeadSize+methodSize:expected], compress)
}

// ReadServerFrame 从字节流（TCP）中读出一个完整下行帧并解码（v2 / v1 自适应分帧）。
// v2 凭 [251] 引导字节无歧义分帧；v1 流式场景首字节 0x01 一律按心跳解释
// （legacy 协议的固有歧义，见文件头注释——这正是 v2 引导头存在的理由）。
func ReadServerFrame(r *bufio.Reader) (*ServerFrame, error) {
	head, err := r.Peek(1)
	if err != nil {
		return nil, err
	}
	switch head[0] {
	case responseMagic: // v2
		head2, err := r.Peek(2)
		if err != nil {
			return nil, err
		}
		switch head2[1] {
		case msgHeartbeat:
			_, _ = r.Discard(2)
			return &ServerFrame{IsHeartbeat: true}, nil
		case msgReplace:
			_, _ = r.Discard(2)
			return &ServerFrame{IsReplaceKick: true}, nil
		case msgLogic:
			hdr, err := r.Peek(v2LogicHeadSize)
			if err != nil {
				return nil, err
			}
			methodSize := int(binary.BigEndian.Uint16(hdr[7:9]))
			dataSize := int(binary.BigEndian.Uint32(hdr[9:13]))
			total := v2LogicHeadSize + methodSize + dataSize
			all := make([]byte, total)
			if _, err := io.ReadFull(r, all); err != nil {
				return nil, err
			}
			return DecodeServerFrame(all)
		default:
			return nil, fmt.Errorf("robot: v2 unknown message type %d", head2[1])
		}

	case msgHeartbeat: // v1 心跳（流式场景下与 compress=1 歧义，按心跳优先）
		_, _ = r.Discard(1)
		return &ServerFrame{IsHeartbeat: true}, nil

	case msgReplace: // v1 替换登录通知
		_, _ = r.Discard(1)
		return &ServerFrame{IsReplaceKick: true}, nil

	default: // v1 逻辑响应（compress=0）
		hdr, err := r.Peek(v1LogicHeadSize)
		if err != nil {
			return nil, err
		}
		methodSize := int(binary.BigEndian.Uint16(hdr[1:3]))
		dataSize := int(binary.BigEndian.Uint32(hdr[3:7]))
		total := v1LogicHeadSize + methodSize + dataSize
		all := make([]byte, total)
		if _, err := io.ReadFull(r, all); err != nil {
			return nil, err
		}
		return decodeV1Logic(all)
	}
}
