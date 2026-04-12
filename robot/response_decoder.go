package robot

// response_decoder.go 解析从 TCP/KCP 网关收到的服务端推送帧。
//
// 背景：
// 服务端 getSendToClientData 对 TCP/KCP（非 WS）连接输出的格式是：
//   [1: compress][2: methodNameSize:BE][4: dataSize:BE][methodName:n][data:n]
//
// 这和客户端发送的请求帧格式不同（请求帧有 magic=250 头 + 不同的字段布局）。
// 心跳响应更特殊——只有 1 字节 [0x01]。
//
// 本文件提供 DecodeServerFrame，在 KCP robot 的 read loop 中使用。

import (
	"encoding/binary"
	"fmt"

	util2 "github.com/smallnest/rpcx/util"
)

// ServerFrame 是从服务端收到的一帧解码后的结构。
type ServerFrame struct {
	IsHeartbeat bool
	MessageType string // "module.method" 格式
	Data        []byte // 业务数据（已解压）
}

// DecodeServerFrame 把 readKCPFrame / TCP 读到的原始字节解码为 ServerFrame。
//
// 判断逻辑：
//  1. len == 0 → 错误
//  2. len == 1 且第一个字节 == 0x01 → 心跳响应
//  3. 第一个字节 == 0xFA (magic=250) → 请求格式回显（如某些服务端实现的 heartbeat echo）
//  4. 其它 → TCP/KCP 响应格式 [compress][methodSize:2][dataSize:4][method][data]
func DecodeServerFrame(raw []byte) (*ServerFrame, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("robot: empty frame")
	}

	// Case 1: 心跳响应——服务端发 []byte{1}
	if len(raw) == 1 && raw[0] == 0x01 {
		return &ServerFrame{IsHeartbeat: true}, nil
	}

	// Case 2: 请求格式回显（magic=250）——decodeTgfBinaryFrame 格式
	if raw[0] == 0xFA {
		// 心跳回显 [250, 1]
		if len(raw) >= 2 && raw[1] == 0x01 {
			return &ServerFrame{IsHeartbeat: true}, nil
		}
		// Logic 回显——不太常见，但为安全起见跳过
		return nil, fmt.Errorf("robot: unexpected request-format echo, len=%d", len(raw))
	}

	// Case 3: TCP/KCP 响应格式
	// [1: compress][2: methodNameSize:BE][4: dataSize:BE][methodName:n][data:n]
	if len(raw) < 7 {
		return nil, fmt.Errorf("robot: response frame too short: %d bytes", len(raw))
	}
	compress := raw[0]
	methodSize := binary.BigEndian.Uint16(raw[1:3])
	dataSize := binary.BigEndian.Uint32(raw[3:7])
	expected := 7 + int(methodSize) + int(dataSize)
	if len(raw) < expected {
		return nil, fmt.Errorf("robot: response frame truncated: have %d want %d", len(raw), expected)
	}

	methodBytes := raw[7 : 7+int(methodSize)]
	dataBytes := raw[7+int(methodSize) : expected]

	// 解压
	if compress == 1 && len(dataBytes) > 0 {
		unzipped, err := util2.Unzip(dataBytes)
		if err != nil {
			return nil, fmt.Errorf("robot: response unzip failed: %w", err)
		}
		dataBytes = unzipped
	}

	return &ServerFrame{
		MessageType: string(methodBytes),
		Data:        dataBytes,
	}, nil
}
