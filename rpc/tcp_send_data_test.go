package rpc

// D2 / P0-1 回归测试：getSendToClientData 的返回值必须与任何共享存储零别名。
//
// 历史 bug：编码走 bytebufferpool，`res = bp.Bytes()` 返回池化 buffer 的内部切片，
// defer Put 归还池后任何其他连接的编码调用都可能复用同一 buffer 并覆盖其内容——
// 而 res 此时还在 writeChan 里排队等 writer goroutine 异步写出，造成跨连接串包。
//
// 测试策略：
//   1. 顺序复用回归（确定性）：先编码一帧并留存引用，再连续编码大量不同内容的帧
//      ——旧实现下池化 buffer 几乎必然被复用并覆盖留存帧；新实现留存帧必须保持原样。
//   2. 并发无串扰（-race）：N 个 goroutine 各自编码带身份标记的载荷并立即校验完整性，
//      模拟多连接同时响应的真实场景。
//   3. 压缩路径：>= compressMinSize 触发压缩位，解压后内容一致；
//      WS 分支 Zip 标志与数据完整性。

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	util2 "github.com/thkhxm/rpcx/v2/util"
	"google.golang.org/protobuf/proto"
)

// decodeTCPResponseFrame 按 getSendToClientData 的 TCP 编码格式反解一帧，
// 返回 messageType 与（已按需解压的）payload。
// F5 后默认是 v2 帧（[251][2][compress][code:4][mtSize:2][dataSize:4][mt][data]），
// 同时兼容 legacy v1（[compress][mtSize:2][dataSize:4][mt][data]）。
func decodeTCPResponseFrame(t *testing.T, frame []byte) (messageType string, payload []byte) {
	t.Helper()
	head := 7
	var compress byte
	var mtSize, dataSize int
	if len(frame) >= 2 && frame[0] == responseMagicNumber && frame[1] == byte(Logic) {
		head = 13
		if len(frame) < head {
			t.Fatalf("v2 frame too short: %d bytes", len(frame))
		}
		compress = frame[2]
		mtSize = int(binary.BigEndian.Uint16(frame[7:9]))
		dataSize = int(binary.BigEndian.Uint32(frame[9:13]))
	} else {
		if len(frame) < head {
			t.Fatalf("frame too short: %d bytes", len(frame))
		}
		compress = frame[0]
		mtSize = int(binary.BigEndian.Uint16(frame[1:3]))
		dataSize = int(binary.BigEndian.Uint32(frame[3:7]))
	}
	if len(frame) != head+mtSize+dataSize {
		t.Fatalf("frame size mismatch: have %d want %d", len(frame), head+mtSize+dataSize)
	}
	messageType = string(frame[head : head+mtSize])
	payload = frame[head+mtSize:]
	if compress == 1 {
		unzipped, err := util2.Unzip(payload)
		if err != nil {
			t.Fatalf("unzip payload: %v", err)
		}
		payload = unzipped
	}
	return messageType, payload
}

// frameCompressFlag 取一帧（v2/v1 自适应）的 compress 标志位。
func frameCompressFlag(t *testing.T, frame []byte) byte {
	t.Helper()
	if len(frame) >= 3 && frame[0] == responseMagicNumber && frame[1] == byte(Logic) {
		return frame[2]
	}
	if len(frame) < 1 {
		t.Fatalf("empty frame")
	}
	return frame[0]
}

// TestGetSendToClientData_NoAliasing_Sequential 是 P0-1 的核心确定性回归：
// 留存的帧在后续大量编码调用之后必须保持字节级不变。
func TestGetSendToClientData_NoAliasing_Sequential(t *testing.T) {
	srv := newTestServer()

	original := []byte("user-A-private-data-0123456789")
	frame := srv.getSendToClientData("game.Hello", 1, 0, original)
	snapshot := append([]byte(nil), frame...)

	// 大量后续编码——旧实现下这些调用会从池里拿到同一 buffer 并覆盖 frame。
	for i := 0; i < 500; i++ {
		filler := bytes.Repeat([]byte{byte(i)}, 64+i%128)
		_ = srv.getSendToClientData(fmt.Sprintf("other.M%d", i), int32(i), 0, filler)
	}

	if !bytes.Equal(frame, snapshot) {
		t.Fatalf("P0-1 regression: frame mutated by subsequent encodes\n want=%v\n got=%v", snapshot, frame)
	}
	mt, payload := decodeTCPResponseFrame(t, frame)
	if mt != "game.Hello" {
		t.Errorf("messageType = %q, want %q", mt, "game.Hello")
	}
	if !bytes.Equal(payload, original) {
		t.Errorf("payload corrupted: got %q want %q", payload, original)
	}
}

// TestGetSendToClientData_ConcurrentNoCrosstalk 模拟多连接并发响应：
// 每个 goroutine 编码带自己身份标记的载荷，持有一段时间后校验内容仍属于自己。
// 必须在 -race 下跑（go test -race）。
func TestGetSendToClientData_ConcurrentNoCrosstalk(t *testing.T) {
	srv := newTestServer()

	const (
		goroutines = 16
		iterations = 200
	)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// 每个 goroutine 的载荷用自己的 id 填充，长度互不相同，串包必现内容错乱
				payload := bytes.Repeat([]byte{byte(g + 1)}, 32+g*8)
				mt := fmt.Sprintf("mod%d.Method", g)
				frame := srv.getSendToClientData(mt, int32(i), 0, payload)

				// 模拟"帧在 writeChan 里排队"的窗口：先让别的 goroutine 有机会编码，
				// 再回头校验自己的帧。
				gotMt, gotPayload := decodeTCPResponseFrame(t, frame)
				if gotMt != mt {
					errCh <- fmt.Errorf("goroutine %d iter %d: messageType crosstalk: got %q want %q", g, i, gotMt, mt)
					return
				}
				if !bytes.Equal(gotPayload, payload) {
					errCh <- fmt.Errorf("goroutine %d iter %d: payload crosstalk", g, i)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestGetSendToClientData_CompressRoundTrip 验证 >= compressMinSize 的载荷
// 走压缩路径：compress 位为 1，解压后与原文一致。
func TestGetSendToClientData_CompressRoundTrip(t *testing.T) {
	srv := newTestServer()

	big := bytes.Repeat([]byte("abcdefgh"), compressMinSize/8+1) // > compressMinSize
	frame := srv.getSendToClientData("game.Big", 7, 0, big)

	if got := frameCompressFlag(t, frame); got != 1 {
		t.Fatalf("compress flag = %d, want 1 for payload of %d bytes", got, len(big))
	}
	mt, payload := decodeTCPResponseFrame(t, frame)
	if mt != "game.Big" {
		t.Errorf("messageType = %q", mt)
	}
	if !bytes.Equal(payload, big) {
		t.Errorf("decompressed payload mismatch: got %d bytes want %d bytes", len(payload), len(big))
	}
}

// TestGetSendToClientData_SmallNotCompressed 验证小载荷不压缩。
func TestGetSendToClientData_SmallNotCompressed(t *testing.T) {
	srv := newTestServer()
	small := []byte("tiny")
	frame := srv.getSendToClientData("game.Small", 1, 0, small)
	if got := frameCompressFlag(t, frame); got != 0 {
		t.Fatalf("compress flag = %d, want 0", got)
	}
	mt, payload := decodeTCPResponseFrame(t, frame)
	if mt != "game.Small" || !bytes.Equal(payload, small) {
		t.Errorf("frame round trip failed: mt=%q payload=%q", mt, payload)
	}
}

// TestGetSendToClientData_WSBranch 验证 WebSocket 分支：proto.Marshal 独立分配 +
// 压缩阈值统一为 >= compressMinSize + Zip 标志正确。
func TestGetSendToClientData_WSBranch(t *testing.T) {
	cfg := &ServerConfig{netType: netWebsocket}
	srv := &TCPServer{config: cfg}

	// 小载荷：不压缩
	small := []byte("ws-small")
	frame := srv.getSendToClientData("game.WS", 3, 9, small)
	resp := &WSResponse{}
	if err := proto.Unmarshal(frame, resp); err != nil {
		t.Fatalf("unmarshal WSResponse: %v", err)
	}
	if resp.Zip {
		t.Errorf("small payload should not be zipped")
	}
	if resp.MessageType != "game.WS" || resp.ReqId != 3 || resp.Code != 9 {
		t.Errorf("WSResponse meta mismatch: %+v", resp)
	}
	if !bytes.Equal(resp.Data, small) {
		t.Errorf("WSResponse data mismatch")
	}

	// 大载荷：压缩 + 解压一致
	big := bytes.Repeat([]byte("x"), compressMinSize)
	frame = srv.getSendToClientData("game.WS", 4, 0, big)
	resp = &WSResponse{}
	if err := proto.Unmarshal(frame, resp); err != nil {
		t.Fatalf("unmarshal WSResponse: %v", err)
	}
	if !resp.Zip {
		t.Errorf("payload of %d bytes should be zipped (>= compressMinSize)", len(big))
	}
	unzipped, err := util2.Unzip(resp.Data)
	if err != nil {
		t.Fatalf("unzip ws data: %v", err)
	}
	if !bytes.Equal(unzipped, big) {
		t.Errorf("ws unzipped payload mismatch")
	}
}
