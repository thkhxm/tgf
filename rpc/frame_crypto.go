package rpc

// A8: KCP 网关的帧层 AEAD 加密。
//
// 这是 A8 "首版必须 AEAD" 的实现。默认算法 ChaCha20-Poly1305（无硬件 AES 的
// 移动端性能更稳）；预留 aeadSealer 接口的扩展点，C 档可以加 AES-GCM。
//
// 密钥协商：
//   - A8 骨架只支持"预共享密钥"模式——server 和 client 都配同一个 32 字节 key。
//   - Noise_NN / 动态密钥协商作为后续演进（见 A8 报告的"已知遗留"）。
//
// 帧格式（外层 = wire 格式）：
//   带 AEAD 时：[4 bytes uint32 payload_len(big-endian)][12 bytes nonce][sealed bytes]
//               其中 payload_len = len(nonce) + len(sealed)，sealed 末尾已含 Poly1305 tag
//   不带 AEAD 时：[4 bytes uint32 payload_len][tgf_frame_bytes]
//
// 长度前缀的作用是给接收方知道"一个完整应用层帧从哪里结束"——KCP 本身是可靠
// 字节流，没有天然的消息边界。

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// aeadNonceSize 是 ChaCha20-Poly1305 的 nonce 长度（固定 12 字节）。
	aeadNonceSize = 12
	// aeadKeySize 是 ChaCha20-Poly1305 的 key 长度（固定 32 字节）。
	aeadKeySize = 32
	// kcpFrameHeaderSize 是外层长度前缀（4 字节 big-endian uint32）。
	kcpFrameHeaderSize = 4
	// kcpMaxFrameSize 防御 DoS：单帧 payload 上限 1 MB。超过这个值读取立即中止。
	kcpMaxFrameSize = 1 * 1024 * 1024
)

// aeadSealer 把明文封装为上线帧 payload（不含 4 字节长度前缀）。
// 实现可以是加密的（sealedPayload = nonce || ciphertext || tag）或明文的。
type aeadSealer interface {
	// Seal 返回的字节是"外层长度前缀之后的全部内容"。
	Seal(plaintext []byte) ([]byte, error)
	// Open 解析 payload（不含长度前缀），返回明文。
	Open(payload []byte) ([]byte, error)
	// Enabled 表示是否真正启用了加密。false 时 Seal/Open 只做透传。
	Enabled() bool
}

// ---- 明文实现（用于未配密钥时的开发/内网模式）----

type plaintextSealer struct{}

func (plaintextSealer) Seal(plaintext []byte) ([]byte, error) { return plaintext, nil }
func (plaintextSealer) Open(payload []byte) ([]byte, error)   { return payload, nil }
func (plaintextSealer) Enabled() bool                         { return false }

// ---- ChaCha20-Poly1305 实现 ----

type chachaSealer struct {
	aead interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
		NonceSize() int
	}
}

func newChaChaSealer(key []byte) (*chachaSealer, error) {
	if len(key) != aeadKeySize {
		return nil, fmt.Errorf("tgf/rpc: AEAD key must be exactly %d bytes, got %d", aeadKeySize, len(key))
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("tgf/rpc: chacha20poly1305.New: %w", err)
	}
	return &chachaSealer{aead: aead}, nil
}

func (c *chachaSealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, aeadNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("tgf/rpc: rand nonce: %w", err)
	}
	// Seal 的 dst 我们传 nonce，让结果变成 nonce || ciphertext||tag 连续布局
	// （chacha20poly1305.Seal 会 append 到 dst 后面）。
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return sealed, nil
}

func (c *chachaSealer) Open(payload []byte) ([]byte, error) {
	if len(payload) < aeadNonceSize+chacha20poly1305.Overhead {
		return nil, errors.New("tgf/rpc: AEAD payload too short")
	}
	nonce := payload[:aeadNonceSize]
	ciphertext := payload[aeadNonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("tgf/rpc: AEAD open failed: %w", err)
	}
	return plaintext, nil
}

func (c *chachaSealer) Enabled() bool { return true }

// ---- 线级读写辅助 ----

// writeKCPFrame 把明文封装成 sealed → 加上 4 字节长度前缀 → 写到 w。
// 写入的总字节数 = 4 + len(sealed)。
func writeKCPFrame(w io.Writer, s aeadSealer, plaintext []byte) error {
	payload, err := s.Seal(plaintext)
	if err != nil {
		return err
	}
	if len(payload) > kcpMaxFrameSize {
		return fmt.Errorf("tgf/rpc: outbound frame too large: %d > %d", len(payload), kcpMaxFrameSize)
	}
	hdr := make([]byte, kcpFrameHeaderSize)
	binary.BigEndian.PutUint32(hdr, uint32(len(payload)))

	// 为避免两次 Write 被 KCP 分成两个段后对端读到一半，拼接到一个缓冲再写。
	buf := make([]byte, 0, kcpFrameHeaderSize+len(payload))
	buf = append(buf, hdr...)
	buf = append(buf, payload...)
	_, err = w.Write(buf)
	return err
}

// readKCPFrame 从 r 读一个完整的长度前缀帧，Open 后返回明文。
// 读到 EOF / 错误 / 长度超限时返回 error。
func readKCPFrame(r io.Reader, s aeadSealer) ([]byte, error) {
	hdr := make([]byte, kcpFrameHeaderSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	payloadLen := binary.BigEndian.Uint32(hdr)
	if payloadLen == 0 {
		return nil, errors.New("tgf/rpc: zero-length frame")
	}
	if payloadLen > kcpMaxFrameSize {
		return nil, fmt.Errorf("tgf/rpc: inbound frame too large: %d > %d", payloadLen, kcpMaxFrameSize)
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return s.Open(payload)
}
