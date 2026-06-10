package rpc

// A2 phase2/phase4 单元测试：
//   phase2: tcpFramedConn 的帧解码（net.Pipe 驱动）
//   phase4: wsFramedConn 的真实 HTTP Upgrade + ReadFrame/WriteFrame 往返
//           （httptest.Server + gorilla websocket client）

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

// writeIntoPipe 把 buf 通过 net.Pipe 异步写入 reader 端。
// 返回 server（reader 用）和等待写完的回调。
func writeIntoPipe(t *testing.T, buf []byte) (server net.Conn, waitWrite func()) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer client.Close()
		_, _ = client.Write(buf)
	}()
	return server, func() { <-done }
}

func TestTCPFramedConn_ReadFrame_Heartbeat(t *testing.T) {
	server, wait := writeIntoPipe(t, []byte{requestMagicNumber, byte(Heartbeat)})
	defer server.Close()

	fc := newTCPFramedConn(server, 0, 0)
	frame, err := fc.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame err: %v", err)
	}
	if frame.MessageType != Heartbeat {
		t.Errorf("want Heartbeat, got %v", frame.MessageType)
	}
	if len(frame.Module)+len(frame.Method)+len(frame.Data) != 0 {
		t.Errorf("Heartbeat frame should carry no payload, got %+v", frame)
	}
	wait()
}

func TestTCPFramedConn_ReadFrame_Logic(t *testing.T) {
	method := "player.Move"
	payload := []byte("hello-world-payload")

	// [1:magic][1:type][2:methodSize][2:dataSize][n:method][n:data]
	buf := []byte{requestMagicNumber, byte(Logic)}
	msLen := make([]byte, 2)
	binary.BigEndian.PutUint16(msLen, uint16(len(method)))
	buf = append(buf, msLen...)
	dsLen := make([]byte, 2)
	binary.BigEndian.PutUint16(dsLen, uint16(len(payload)))
	buf = append(buf, dsLen...)
	buf = append(buf, method...)
	buf = append(buf, payload...)

	server, wait := writeIntoPipe(t, buf)
	defer server.Close()

	fc := newTCPFramedConn(server, 0, 0)
	frame, err := fc.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame err: %v", err)
	}
	if frame.MessageType != Logic {
		t.Errorf("want Logic, got %v", frame.MessageType)
	}
	if frame.Module != "player" {
		t.Errorf("module = %q, want %q", frame.Module, "player")
	}
	if frame.Method != "Move" {
		t.Errorf("method = %q, want %q", frame.Method, "Move")
	}
	if string(frame.Data) != string(payload) {
		t.Errorf("data mismatch: got %q want %q", frame.Data, payload)
	}
	wait()
}

func TestTCPFramedConn_ReadFrame_MagicMismatch(t *testing.T) {
	server, wait := writeIntoPipe(t, []byte{0x00, 0x01})
	defer server.Close()

	fc := newTCPFramedConn(server, 0, 0)
	_, err := fc.ReadFrame()
	if err == nil {
		t.Fatalf("expected error for magic mismatch, got nil")
	}
	wait()
}

func TestTCPFramedConn_ReadFrame_UnknownType(t *testing.T) {
	server, wait := writeIntoPipe(t, []byte{requestMagicNumber, 0xFF})
	defer server.Close()

	fc := newTCPFramedConn(server, 0, 0)
	_, err := fc.ReadFrame()
	if err == nil {
		t.Fatalf("expected error for unknown message type, got nil")
	}
	wait()
}

func TestTCPFramedConn_ReadFrame_EOF(t *testing.T) {
	// 空管道：立刻关闭，ReadFrame 应返回 io.EOF（或包装的 err）。
	client, server := net.Pipe()
	_ = client.Close()
	defer server.Close()

	fc := newTCPFramedConn(server, 0, 0)

	// 加个保险 deadline 防止测试挂死
	done := make(chan error, 1)
	go func() {
		_, err := fc.ReadFrame()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error after EOF, got nil")
		}
		if !errors.Is(err, io.EOF) && err.Error() != "io: read/write on closed pipe" {
			// 接受两种返回：真正的 io.EOF 或 pipe 关闭错误
			t.Logf("got acceptable error after close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("ReadFrame did not return after peer close")
	}
}

// TestTCPFramedConn_WriteFrame_Passthrough 验证 WriteFrame 就是透传底层 Write。
func TestTCPFramedConn_WriteFrame_Passthrough(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	payload := []byte("phase2-write-check")
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(client, buf); err != nil {
			got <- nil
			return
		}
		got <- buf
	}()

	fc := newTCPFramedConn(server, 0, 0)
	if err := fc.WriteFrame(payload); err != nil {
		t.Fatalf("WriteFrame err: %v", err)
	}
	select {
	case b := <-got:
		if string(b) != string(payload) {
			t.Errorf("payload mismatch: %q vs %q", b, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("peer did not receive write within 2s")
	}
}

// TestTCPFramedConn_EncodeResponse E6：编码下沉后，TCP 适配器自带二进制响应帧
// 编码且不被识别为 WS 传输（isWSConn 用于 handleConn 的 meta 历史行为差异）。
func TestTCPFramedConn_EncodeResponse(t *testing.T) {
	_, server := net.Pipe()
	defer server.Close()
	fc := newTCPFramedConn(server, 0, 0)
	if isWSConn(fc) {
		t.Errorf("tcpFramedConn 不应被识别为 WS 传输")
	}
	frame := fc.EncodeResponse("game.Hi", 1, 0, []byte("payload"))
	want := encodeBinaryResponseFrame("game.Hi", []byte("payload"))
	if !bytes.Equal(frame, want) {
		t.Errorf("tcpFramedConn.EncodeResponse 应输出二进制响应帧")
	}
}

// ============================================================================
// A2-phase4: wsFramedConn 真实 WebSocket 端到端测试
//
// 用 httptest.Server 起一个 HTTP 端口，在 handler 里调 wsFramedConnFromRequest
// 做真实的 WS Upgrade；用 gorilla websocket 客户端连上去发二进制消息，验证
// ReadFrame 能正确解码 WSMessage、WriteFrame 能被客户端收到。
//
// 这些测试是 conn.go 唯一覆盖真实网络栈的部分——前面的 tcpFramedConn 测试都
// 走 net.Pipe 内存管道，不涉及 kernel。
// ============================================================================

// startWSTestServer 起一个 httptest.Server，在 handler 里做 upgrade 并把
// server 端的 wsFramedConn 发送到 ready 通道。handler 会一直阻塞直到 done
// 通道被关闭，这样客户端的连接不会因 handler 返回而失效。
// 调用方负责 defer 掉 srv.Close() 和 close(done)。
func startWSTestServer(t *testing.T) (srv *httptest.Server, ready chan *wsFramedConn, done chan struct{}) {
	t.Helper()
	ready = make(chan *wsFramedConn, 1)
	done = make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ic, err := wsFramedConnFromRequest(w, r, 5*time.Second, 2*time.Second)
		if err != nil {
			t.Errorf("server upgrade: %v", err)
			return
		}
		wfc, ok := ic.(*wsFramedConn)
		if !ok {
			t.Errorf("server IConn is not *wsFramedConn, got %T", ic)
			return
		}
		ready <- wfc
		<-done // 阻塞 handler 保持连接存活直到测试主动关闭
	}))
	return srv, ready, done
}

// dialWSClient 把 http://127.0.0.1:xxxx 转成 ws://127.0.0.1:xxxx 并建立客户端连接。
func dialWSClient(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return c
}

// TestWSFramedConn_ReadFrame_Binary 验证服务端 wsFramedConn.ReadFrame 能正确
// 解码客户端发来的 WSMessage binary 帧。
func TestWSFramedConn_ReadFrame_Binary(t *testing.T) {
	srv, ready, done := startWSTestServer(t)
	defer srv.Close()
	defer close(done)

	client := dialWSClient(t, srv)
	defer client.Close()

	var serverFC *wsFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("server wsFramedConn not ready within 2s")
	}

	// 客户端发一条 WSMessage binary
	msg := &WSMessage{
		Module:      "player",
		ServiceName: "Move",
		Data:        []byte("payload-xyz"),
		ReqId:       42,
	}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatalf("client write: %v", err)
	}

	// 服务端 ReadFrame
	frame, err := serverFC.ReadFrame()
	if err != nil {
		t.Fatalf("server ReadFrame: %v", err)
	}
	if frame.MessageType != Logic {
		t.Errorf("MessageType = %v, want Logic", frame.MessageType)
	}
	if frame.Module != "player" {
		t.Errorf("Module = %q, want player", frame.Module)
	}
	if frame.Method != "Move" {
		t.Errorf("Method = %q, want Move", frame.Method)
	}
	if string(frame.Data) != "payload-xyz" {
		t.Errorf("Data = %q, want payload-xyz", frame.Data)
	}
	if frame.ReqId != 42 {
		t.Errorf("ReqId = %d, want 42", frame.ReqId)
	}
}

// TestWSFramedConn_WriteFrame_ClientReceives 验证服务端 WriteFrame 能被客户端
// 以 binary message 读到。
func TestWSFramedConn_WriteFrame_ClientReceives(t *testing.T) {
	srv, ready, done := startWSTestServer(t)
	defer srv.Close()
	defer close(done)

	client := dialWSClient(t, srv)
	defer client.Close()

	var serverFC *wsFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("server wsFramedConn not ready within 2s")
	}

	// 服务端写一条
	payload := []byte("server-to-client-reply")
	if err := serverFC.WriteFrame(payload); err != nil {
		t.Fatalf("server WriteFrame: %v", err)
	}

	// 客户端读
	mt, got, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client ReadMessage: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Errorf("message type = %v, want BinaryMessage", mt)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q want %q", got, payload)
	}
}

// TestWSFramedConn_ClientCloseReturnsError 验证客户端主动关闭后，
// 服务端 ReadFrame 返回 error（通常是 CloseError / unexpectedEOF）。
func TestWSFramedConn_ClientCloseReturnsError(t *testing.T) {
	srv, ready, done := startWSTestServer(t)
	defer srv.Close()
	defer close(done)

	client := dialWSClient(t, srv)

	var serverFC *wsFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("server wsFramedConn not ready within 2s")
	}

	// 客户端主动关闭
	_ = client.Close()

	// 服务端 ReadFrame 应返回 error——加 3 秒 deadline 防止挂住
	errCh := make(chan error, 1)
	go func() {
		_, err := serverFC.ReadFrame()
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("expected error after client close, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("ReadFrame did not return within 3s after client close")
	}
}

// TestWSFramedConn_TransportAndEncode E6：WS 适配器被 isWSConn 识别为 WS 传输，
// 且 EncodeResponse 输出 WSResponse proto 格式。
func TestWSFramedConn_TransportAndEncode(t *testing.T) {
	srv, ready, done := startWSTestServer(t)
	defer srv.Close()
	defer close(done)

	client := dialWSClient(t, srv)
	defer client.Close()

	var serverFC *wsFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("server wsFramedConn not ready within 2s")
	}

	if !isWSConn(serverFC) {
		t.Errorf("wsFramedConn 应被识别为 WS 传输")
	}
	frame := serverFC.EncodeResponse("game.WS", 3, 9, []byte("ws-payload"))
	resp := &WSResponse{}
	if err := proto.Unmarshal(frame, resp); err != nil {
		t.Fatalf("EncodeResponse 应输出 WSResponse proto: %v", err)
	}
	if resp.MessageType != "game.WS" || resp.ReqId != 3 || resp.Code != 9 ||
		!bytes.Equal(resp.Data, []byte("ws-payload")) {
		t.Errorf("WSResponse 字段不符: %+v", resp)
	}
}

// TestWSFramedConn_MultipleFramesPreserveOrder 验证连续发多个帧服务端按序读到。
func TestWSFramedConn_MultipleFramesPreserveOrder(t *testing.T) {
	srv, ready, done := startWSTestServer(t)
	defer srv.Close()
	defer close(done)

	client := dialWSClient(t, srv)
	defer client.Close()

	var serverFC *wsFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("server wsFramedConn not ready within 2s")
	}

	const N = 10
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			msg := &WSMessage{
				Module:      "m",
				ServiceName: "s",
				Data:        []byte{byte(i)},
				ReqId:       int32(i),
			}
			raw, _ := proto.Marshal(msg)
			if err := client.WriteMessage(websocket.BinaryMessage, raw); err != nil {
				t.Errorf("client write %d: %v", i, err)
				return
			}
		}
	}()

	for i := 0; i < N; i++ {
		frame, err := serverFC.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame %d: %v", i, err)
		}
		if frame.ReqId != int32(i) {
			t.Errorf("frame %d ReqId = %d, want %d", i, frame.ReqId, i)
		}
	}
	wg.Wait()
}
