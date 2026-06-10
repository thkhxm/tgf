package internal

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description D7 / P0-6 登录鉴权地基：HMAC 签名 login token 的生成与校验
//2026/6/10
//***************************************************

// 历史：本文件曾是恒返回 true 的 LoginCheck 死桩（`return true, token`），
// 全仓零调用点，却给开发者制造了"框架已校验登录"的安全假象——v3 审计定级 P0-6。
// 现在替换为真实的 HMAC-SHA256 签名 token 实现，由 rpc 包的默认 ILoginCheck
// （rpc/login_check.go）接入 gate.Login 强制调用。
//
// token 格式（三段，点号分隔）：
//
//	base64url(userId) . expireUnix . hex(HMAC-SHA256(secret, 前两段))
//
// 设计要点：
//   - userId 做 base64url 编码，避免 userId 含 "." 时解析歧义；
//   - 签名覆盖 userId 与过期时间——客户端无法伪造身份或续期；
//   - 校验顺序：先验签（hmac.Equal 常数时间比较）再验过期，错误可区分；
//   - 这是"地基级"实现：无撤销列表、无刷新机制。需要完整会话管理的业务
//     可通过 rpc.Server.WithLoginCheck 注入自定义实现（如标准 JWT + JWKS）。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrLoginTokenSecretEmpty 表示签发/校验时密钥未配置。
	ErrLoginTokenSecretEmpty = errors.New("tgf/rpc: login token secret 未配置")
	// ErrLoginTokenMalformed 表示 token 结构非法（段数、编码、时间戳格式）。
	ErrLoginTokenMalformed = errors.New("tgf/rpc: login token 格式非法")
	// ErrLoginTokenSignature 表示签名不匹配（被篡改或密钥不一致）。
	ErrLoginTokenSignature = errors.New("tgf/rpc: login token 签名不匹配")
	// ErrLoginTokenExpired 表示 token 已过期。
	ErrLoginTokenExpired = errors.New("tgf/rpc: login token 已过期")
	// ErrLoginTokenEmptyUserId 表示签发时 userId 为空。
	ErrLoginTokenEmptyUserId = errors.New("tgf/rpc: login token userId 不能为空")
)

// BuildLoginToken 用 secret 为 userId 签发一个在 expireAt 过期的 login token。
func BuildLoginToken(secret []byte, userId string, expireAt time.Time) (string, error) {
	if len(secret) == 0 {
		return "", ErrLoginTokenSecretEmpty
	}
	if userId == "" {
		return "", ErrLoginTokenEmptyUserId
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(userId)) +
		"." + strconv.FormatInt(expireAt.Unix(), 10)
	return payload + "." + signLoginPayload(secret, payload), nil
}

// VerifyLoginToken 校验 token 的签名与有效期，成功返回其中绑定的 userId。
// now 由调用方传入便于测试注入时钟；生产路径传 time.Now()。
func VerifyLoginToken(secret []byte, token string, now time.Time) (string, error) {
	if len(secret) == 0 {
		return "", ErrLoginTokenSecretEmpty
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrLoginTokenMalformed
	}
	payload := parts[0] + "." + parts[1]
	// 先验签（常数时间比较），后看内容——未通过签名的数据一律不解析语义。
	expect := signLoginPayload(secret, payload)
	if !hmac.Equal([]byte(expect), []byte(parts[2])) {
		return "", ErrLoginTokenSignature
	}
	expireUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", ErrLoginTokenMalformed
	}
	if now.Unix() > expireUnix {
		return "", ErrLoginTokenExpired
	}
	uid, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrLoginTokenMalformed
	}
	if len(uid) == 0 {
		return "", ErrLoginTokenMalformed
	}
	return string(uid), nil
}

// signLoginPayload 计算 payload 的 HMAC-SHA256 十六进制签名。
func signLoginPayload(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
