package rpc

// A8 单测：
//   1. frame_crypto.go 的 AEAD 正确性（密钥对 → Seal/Open roundtrip；坏密钥 → Open 失败）
//   2. decodeTgfBinaryFrame 对正常/异常输入的行为
//   3. kcpFramedConn 的真实端到端：起一个 kcp.Listener，客户端 Dial 后发送 Logic/Heartbeat 帧，
//      服务端包成 IConn 后 ReadFrame 断言结果；同时验证明文和 AEAD 两种模式

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/kcp-go"
)

// ---- frame_crypto.go 单测 ----

func TestPlaintextSealer_Passthrough(t *testing.T) {
	s := plaintextSealer{}
	if s.Enabled() {
		t.Errorf("plaintextSealer.Enabled should be false")
	}
	data := []byte("hello world")
	sealed, err := s.Seal(data)
	if err != nil || !bytes.Equal(sealed, data) {
		t.Errorf("Seal passthrough failed: %v", err)
	}
	opened, err := s.Open(sealed)
	if err != nil || !bytes.Equal(opened, data) {
		t.Errorf("Open passthrough failed: %v", err)
	}
}

func TestChaChaSealer_SealOpenRoundtrip(t *testing.T) {
	key := make([]byte, aeadKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	s, err := newChaChaSealer(key)
	if err != nil {
		t.Fatalf("newChaChaSealer: %v", err)
	}
	if !s.Enabled() {
		t.Errorf("Enabled should be true")
	}
	plaintext := []byte("sensitive payload 1234567890")
	sealed, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(sealed, plaintext) {
		t.Errorf("sealed should not equal plaintext (no nonce prefix?)")
	}
	// 长度 = nonce 12 + plaintext + tag 16
	if len(sealed) < aeadNonceSize+len(plaintext)+16 {
		t.Errorf("sealed too short: %d", len(sealed))
	}
	opened, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Errorf("roundtrip mismatch: got %q want %q", opened, plaintext)
	}
}

func TestChaChaSealer_WrongKeyFailsOpen(t *testing.T) {
	key1 := make([]byte, aeadKeySize)
	key2 := make([]byte, aeadKeySize)
	_, _ = rand.Read(key1)
	_, _ = rand.Read(key2)

	s1, _ := newChaChaSealer(key1)
	s2, _ := newChaChaSealer(key2)
	sealed, _ := s1.Seal([]byte("secret"))
	if _, err := s2.Open(sealed); err == nil {
		t.Errorf("Open with wrong key should fail")
	}
}

func TestNewChaChaSealer_WrongKeySize(t *testing.T) {
	if _, err := newChaChaSealer(make([]byte, 16)); err == nil {
		t.Errorf("expected error for 16-byte key, got nil")
	}
	if _, err := newChaChaSealer(nil); err == nil {
		t.Errorf("expected error for nil key")
	}
}

func TestChaChaSealer_OpenTooShort(t *testing.T) {
	key := make([]byte, aeadKeySize)
	s, _ := newChaChaSealer(key)
	if _, err := s.Open([]byte("short")); err == nil {
		t.Errorf("Open on too-short payload should fail")
	}
}

// ---- decodeTgfBinaryFrame 单测 ----

func TestDecodeTgfBinaryFrame_Heartbeat(t *testing.T) {
	frame := []byte{requestMagicNumber, byte(Heartbeat)}
	fi, err := decodeTgfBinaryFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fi.MessageType != Heartbeat {
		t.Errorf("type = %v, want Heartbeat", fi.MessageType)
	}
}

func TestDecodeTgfBinaryFrame_Logic(t *testing.T) {
	raw := EncodeTgfBinaryFrame("player", "Move", []byte("data-payload"))
	fi, err := decodeTgfBinaryFrame(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fi.MessageType != Logic || fi.Module != "player" || fi.Method != "Move" {
		t.Errorf("decoded = %+v", fi)
	}
	if string(fi.Data) != "data-payload" {
		t.Errorf("Data = %q", fi.Data)
	}
}

func TestDecodeTgfBinaryFrame_MagicMismatch(t *testing.T) {
	if _, err := decodeTgfBinaryFrame([]byte{0x00, byte(Heartbeat)}); err == nil {
		t.Errorf("expected error for bad magic")
	}
}

func TestDecodeTgfBinaryFrame_UnknownType(t *testing.T) {
	if _, err := decodeTgfBinaryFrame([]byte{requestMagicNumber, 0xFF}); err == nil {
		t.Errorf("expected error for unknown type")
	}
}

func TestDecodeTgfBinaryFrame_ShortHeader(t *testing.T) {
	if _, err := decodeTgfBinaryFrame([]byte{requestMagicNumber}); err == nil {
		t.Errorf("expected error for 1-byte frame")
	}
}

// ---- writeKCPFrame / readKCPFrame 单测（buffer 往返）----

type rwBuffer struct {
	buf bytes.Buffer
}

func (b *rwBuffer) Read(p []byte) (int, error)  { return b.buf.Read(p) }
func (b *rwBuffer) Write(p []byte) (int, error) { return b.buf.Write(p) }

func TestKCPFrameIO_PlaintextRoundtrip(t *testing.T) {
	rw := &rwBuffer{}
	s := plaintextSealer{}
	msg := []byte("frame payload")
	if err := writeKCPFrame(rw, s, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readKCPFrame(rw, s)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Errorf("roundtrip mismatch: %q vs %q", got, msg)
	}
}

func TestKCPFrameIO_AEADRoundtrip(t *testing.T) {
	key := make([]byte, aeadKeySize)
	rand.Read(key)
	s, _ := newChaChaSealer(key)
	rw := &rwBuffer{}
	msg := []byte("encrypted frame payload content")
	if err := writeKCPFrame(rw, s, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	// wire bytes 应当看起来是密文（不包含明文序列）
	if bytes.Contains(rw.buf.Bytes(), msg) {
		t.Errorf("wire should not contain plaintext")
	}
	got, err := readKCPFrame(rw, s)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Errorf("AEAD roundtrip mismatch")
	}
}

func TestKCPFrameIO_TooLargeFrameRejected(t *testing.T) {
	rw := &rwBuffer{}
	// 直接构造一个假装长度超限的 header
	rw.buf.WriteByte(0xFF)
	rw.buf.WriteByte(0xFF)
	rw.buf.WriteByte(0xFF)
	rw.buf.WriteByte(0xFF) // len = 4GB
	if _, err := readKCPFrame(rw, plaintextSealer{}); err == nil {
		t.Errorf("expected error for oversized frame")
	}
}

// ---- kcpFramedConn 端到端（真实 kcp.Listener + kcp.Dial）----

// allocKCPPort 从 OS 拿一个可用端口。KCP 是 UDP，`net.ListenUDP` 能取。
func allocKCPPort(t *testing.T) string {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	c, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	closeRPCResource(t, "temporary UDP listener", c)
	return strconv.Itoa(port)
}

// startTestKCPServer 起一个 kcp.Listener，accept 后把 session 包成 kcpFramedConn
// 发送到 ready 通道。测试里负责 defer close(done) 让 handler goroutine 退出。
func startTestKCPServer(t *testing.T, port string, key []byte) (builder *KCPServerConfig, ready chan *kcpFramedConn, done chan struct{}) {
	t.Helper()
	builder = NewKCPBuilder(port)
	if len(key) == aeadKeySize {
		builder = builder.WithAEADKey(key)
	}
	ready = make(chan *kcpFramedConn, 1)
	done = make(chan struct{})

	addr := fmt.Sprintf("127.0.0.1:%s", port)
	listener, err := kcp.ListenWithOptions(addr, nil, 0, 0)
	if err != nil {
		t.Fatalf("kcp listen: %v", err)
	}

	go func() {
		<-done
		_ = listener.Close()
	}()
	go func() {
		for {
			session, err := listener.AcceptKCP()
			if err != nil {
				return
			}
			session.SetNoDelay(1, 10, 2, 1)
			session.SetStreamMode(true)
			session.SetACKNoDelay(true)
			fc, connErr := newKCPFramedConn(session, builder)
			if connErr != nil {
				_ = session.Close()
				continue
			}
			select {
			case ready <- fc:
			default:
			}
		}
	}()
	return builder, ready, done
}

func TestKCPFramedConn_ReadFrame_HeartbeatPlaintext(t *testing.T) {
	port := allocKCPPort(t)
	_, ready, done := startTestKCPServer(t, port, nil)
	defer close(done)

	// 客户端 Dial
	client, err := kcp.DialWithOptions("127.0.0.1:"+port, nil, 0, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.SetNoDelay(1, 10, 2, 1)
	client.SetStreamMode(true)
	client.SetACKNoDelay(true)
	defer closeRPCResource(t, "KCP client", client)

	// 客户端写一个心跳帧（明文模式 sealer）
	if writeErr := writeKCPFrame(client, plaintextSealer{}, EncodeTgfHeartbeatFrame()); writeErr != nil {
		t.Fatalf("client write: %v", writeErr)
	}

	var serverFC *kcpFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatalf("server accept timeout")
	}

	_ = serverFC.SetReadDeadline(time.Now().Add(3 * time.Second))
	fi, err := serverFC.ReadFrame()
	if err != nil {
		t.Fatalf("server ReadFrame: %v", err)
	}
	if fi.MessageType != Heartbeat {
		t.Errorf("want Heartbeat, got %v", fi.MessageType)
	}
}

func TestKCPFramedConn_ReadFrame_LogicWithAEAD(t *testing.T) {
	key := make([]byte, aeadKeySize)
	rand.Read(key)
	port := allocKCPPort(t)
	builder, ready, done := startTestKCPServer(t, port, key)
	defer close(done)

	// 客户端 Dial + 配同一个 key
	client, err := kcp.DialWithOptions("127.0.0.1:"+port, nil, 0, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.SetNoDelay(1, 10, 2, 1)
	client.SetStreamMode(true)
	client.SetACKNoDelay(true)
	defer closeRPCResource(t, "KCP client", client)

	clientFC, err := newKCPFramedConn(client, builder)
	if err != nil {
		t.Fatalf("client conn init: %v", err)
	}

	// 客户端发送 Logic 帧
	payload := []byte("hello-kcp-aead")
	raw := EncodeTgfBinaryFrame("player", "Move", payload)
	if writeErr := clientFC.WriteFrame(raw); writeErr != nil {
		t.Fatalf("client WriteFrame: %v", writeErr)
	}

	var serverFC *kcpFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatalf("server accept timeout")
	}

	_ = serverFC.SetReadDeadline(time.Now().Add(3 * time.Second))
	fi, err := serverFC.ReadFrame()
	if err != nil {
		t.Fatalf("server ReadFrame: %v", err)
	}
	if fi.MessageType != Logic || fi.Module != "player" || fi.Method != "Move" {
		t.Errorf("decoded frame: %+v", fi)
	}
	if string(fi.Data) != string(payload) {
		t.Errorf("payload mismatch: %q vs %q", fi.Data, payload)
	}
}

func TestKCPFramedConn_AEADMismatchFails(t *testing.T) {
	keyServer := make([]byte, aeadKeySize)
	keyClient := make([]byte, aeadKeySize)
	rand.Read(keyServer)
	rand.Read(keyClient)

	port := allocKCPPort(t)
	_, ready, done := startTestKCPServer(t, port, keyServer)
	defer close(done)

	client, err := kcp.DialWithOptions("127.0.0.1:"+port, nil, 0, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.SetNoDelay(1, 10, 2, 1)
	client.SetStreamMode(true)
	client.SetACKNoDelay(true)
	defer closeRPCResource(t, "KCP client", client)

	clientBuilder := NewKCPBuilder("0").WithAEADKey(keyClient)
	clientFC, err := newKCPFramedConn(client, clientBuilder)
	if err != nil {
		t.Fatalf("client conn init: %v", err)
	}

	raw := EncodeTgfBinaryFrame("a", "B", []byte("x"))
	if writeErr := clientFC.WriteFrame(raw); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	var serverFC *kcpFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatalf("server accept timeout")
	}
	_ = serverFC.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = serverFC.ReadFrame()
	if err == nil {
		t.Errorf("server should fail to decode frame encrypted with wrong key")
	}
}

// TestKCPFramedConn_MultipleFramesPreserveOrder 验证连续发多个 Logic 帧时
// 服务端按序读到（和 wsFramedConn 的对应测试对齐）。
func TestKCPFramedConn_MultipleFramesPreserveOrder(t *testing.T) {
	port := allocKCPPort(t)
	_, ready, done := startTestKCPServer(t, port, nil)
	defer close(done)

	client, err := kcp.DialWithOptions("127.0.0.1:"+port, nil, 0, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.SetNoDelay(1, 10, 2, 1)
	client.SetStreamMode(true)
	client.SetACKNoDelay(true)
	defer closeRPCResource(t, "KCP client", client)

	const N = 5
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			raw := EncodeTgfBinaryFrame("m", "s", []byte{byte(i)})
			if err := writeKCPFrame(client, plaintextSealer{}, raw); err != nil {
				t.Errorf("client write %d: %v", i, err)
				return
			}
		}
	}()

	var serverFC *kcpFramedConn
	select {
	case serverFC = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatalf("server accept timeout")
	}

	for i := 0; i < N; i++ {
		_ = serverFC.SetReadDeadline(time.Now().Add(3 * time.Second))
		fi, err := serverFC.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame %d: %v", i, err)
		}
		if len(fi.Data) != 1 || fi.Data[0] != byte(i) {
			t.Errorf("frame %d data = %v, want [%d]", i, fi.Data, i)
		}
	}
	wg.Wait()
}
