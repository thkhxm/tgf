package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description D7 / P0-6 登录鉴权地基：ILoginCheck 强制接入 gate.Login
//2026/6/10
//***************************************************

// 背景（v3 审计 P0-6）：
//   v2 之前 gate.Login 直接信任客户端自报的 userId 完成身份绑定，LoginReq 没有
//   任何凭据字段，ILoginCheck 是恒真死桩且零调用点——任何能连上网关的 socket
//   都能冒充任意账号（顶号、读写他人存档）。
//
// v3 行为（默认安全 / fail-closed）：
//   - LoginReq 新增 Token 凭据字段（gate.go）；
//   - gate.Login 强制调用 ILoginCheck 校验 Token，校验失败拒绝登录；
//   - 默认实现是 HMAC-SHA256 签名 token（internal/jwt.go），密钥按以下优先级解析：
//       1. Server.WithLoginTokenSecret(secret) 显式注入
//       2. 环境变量 / .env.<module> 中的 `LoginTokenSecret`
//     密钥未配置时默认实现 **拒绝所有登录** 并打 Error 日志（fail-closed）；
//   - 业务可通过 Server.WithLoginCheck(c) 注入自定义校验（如标准 JWT / 平台 SDK）；
//   - 旧的"无鉴权"行为必须显式调用 Server.WithoutLoginCheck() 才保留（opt-out）。
//
// 业务迁移指引：
//   1. 配置密钥：.env.<module> 加 `LoginTokenSecret=<随机长字符串>`，或代码里
//      `WithLoginTokenSecret(...)`；
//   2. 登录服务在完成账号验证（账密/第三方 SDK）后用 rpc.GenerateLoginToken(userId, ttl)
//      签发 token 给客户端；
//   3. 客户端登录帧带上 token，业务侧调用 rpc.UserLoginWithToken(ctx, userId, token)
//      （或自行构造 LoginReq{..., Token: token}）；
//   4. 暂时无法接入的存量项目：显式 WithoutLoginCheck()（仅限内网/开发环境）。

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc/internal"
)

// EnvLoginTokenSecret 是默认 HMAC token 密钥的环境变量名。
// 通过 .env.<module>（godotenv 会注入进程环境）或部署环境变量配置。
// 注意：密钥属于敏感凭据，不要写进版本库——参考 D6 的 .env 治理规则。
const EnvLoginTokenSecret = "LoginTokenSecret"

var (
	// ErrLoginTokenRequired 登录请求缺少凭据（默认鉴权开启时 Token 必填）。
	ErrLoginTokenRequired = errors.New("tgf/rpc: 登录凭据缺失(LoginReq.Token 必填);如确需关闭鉴权请显式调用 Server.WithoutLoginCheck()")
	// ErrLoginCheckRejected 凭据校验未通过（签名/过期/自定义校验拒绝）。
	ErrLoginCheckRejected = errors.New("tgf/rpc: 登录凭据校验未通过")
	// ErrLoginUserIdMismatch 凭据合法但绑定的身份与请求 userId 不一致（冒充防护）。
	ErrLoginUserIdMismatch = errors.New("tgf/rpc: 登录凭据与请求 userId 不匹配")
)

// ---- 包级鉴权状态 ----
//
// 与 rpcserver_timeout.go 的设计取舍一致：鉴权配置是 package 级全局而非 Server
// 实例字段——gate.Login 运行在 GateService 上拿不到 Server 引用，且一个进程只会
// 有一套网关鉴权语义。多 Server 实例共享该配置（与超时/策略配置同性质）。

var (
	// loginCheckDisabled 显式 opt-out 标志。默认 false = 鉴权开启。
	loginCheckDisabled atomic.Bool
	// customLoginCheck 业务注入的自定义校验器。空 box 表示用默认 HMAC 实现。
	customLoginCheck atomic.Value // 存 loginCheckBox
	// loginTokenSecret 显式注入的密钥（优先于环境变量）。
	loginTokenSecret atomic.Value // 存 string
)

// loginCheckBox 统一 atomic.Value 的具体存储类型（不同 ILoginCheck 实现类型
// 直接存会触发 "inconsistently typed value" panic）。
type loginCheckBox struct{ c ILoginCheck }

// WithoutLoginCheck 显式关闭登录凭据校验，恢复 v2 之前"信任客户端自报 userId"
// 的行为。**仅限内网可信环境 / 本地开发**——公网部署关闭鉴权等于允许任意玩家
// 冒充任意账号。
func (s *Server) WithoutLoginCheck() *Server {
	loginCheckDisabled.Store(true)
	log.WarnTag("init", "登录鉴权已被显式关闭(WithoutLoginCheck)——客户端自报 userId 将被直接信任,仅限可信环境使用")
	return s
}

// WithLoginCheck 注入业务自定义的登录凭据校验器，替换默认的 HMAC token 实现。
// CheckLogin(token) 返回 (是否通过, 凭据绑定的 userId)：
//   - 返回 false → 登录被拒绝；
//   - 返回 true 且 userId 非空 → 框架强制其与 LoginReq.UserId 一致，否则拒绝（防冒充）；
//   - 返回 true 且 userId 为空 → 视为"只验凭据有效性,不绑定身份"，放行请求 userId
//     （此时身份绑定的安全性由业务校验器自行负责）。
//
// 传 nil 等价于恢复默认 HMAC 实现。
func (s *Server) WithLoginCheck(c ILoginCheck) *Server {
	customLoginCheck.Store(loginCheckBox{c: c})
	if c != nil {
		log.InfoTag("init", "装载自定义登录凭据校验器 %T", c)
	}
	return s
}

// WithLoginTokenSecret 显式注入默认 HMAC token 实现使用的密钥，
// 优先级高于环境变量 LoginTokenSecret。
func (s *Server) WithLoginTokenSecret(secret string) *Server {
	loginTokenSecret.Store(secret)
	if secret != "" {
		log.InfoTag("init", "装载登录token密钥(len=%d)", len(secret))
	}
	return s
}

// resolveLoginTokenSecret 按优先级解析密钥：显式注入 > 配置系统。没有则返回 nil。
//
// E 档配置读点迁移：原 os.Getenv 直读改走 tgfconfig.Current().Security
// （env 名 LoginTokenSecret 与 D 档常量逐字一致，存量部署不受影响；该 key 已
// 登记 tgf/config.go sensitiveEnvKeys，启动日志脱敏）。每次调用读 Current()
// 快照——经 tgfconfig.Reload()（admin POST /config/reload）可热轮换密钥。
func resolveLoginTokenSecret() []byte {
	if v, ok := loginTokenSecret.Load().(string); ok && v != "" {
		return []byte(v)
	}
	if v := tgfconfig.Current().Security.LoginTokenSecret; v != "" {
		return []byte(v)
	}
	return nil
}

// hmacLoginCheck 是 ILoginCheck 的默认实现：校验 internal.BuildLoginToken 签发的
// HMAC token。fail-closed：密钥未配置时拒绝一切登录并打 Error 日志。
type hmacLoginCheck struct{}

func (hmacLoginCheck) CheckLogin(token string) (bool, string) {
	secret := resolveLoginTokenSecret()
	if len(secret) == 0 {
		log.Error("[gate] 登录被拒:默认鉴权已开启但 %s 未配置。请配置密钥(WithLoginTokenSecret 或环境变量)、注入自定义校验(WithLoginCheck)、或显式关闭鉴权(WithoutLoginCheck)", EnvLoginTokenSecret)
		return false, ""
	}
	uid, err := internal.VerifyLoginToken(secret, token, time.Now())
	if err != nil {
		log.WarnTag("gate", "login token 校验失败 err=%v", err)
		return false, ""
	}
	return true, uid
}

// activeLoginCheck 返回当前生效的校验器：自定义优先，否则默认 HMAC。
func activeLoginCheck() ILoginCheck {
	if box, ok := customLoginCheck.Load().(loginCheckBox); ok && box.c != nil {
		return box.c
	}
	return hmacLoginCheck{}
}

// checkLoginCredential 是 gate.Login 的强制前置校验。
// 返回 nil 表示放行；任何非 nil error 都应使登录被拒绝。
func checkLoginCredential(args *LoginReq) error {
	if loginCheckDisabled.Load() {
		return nil
	}
	if args.Token == "" {
		return ErrLoginTokenRequired
	}
	ok, verifiedUid := activeLoginCheck().CheckLogin(args.Token)
	if !ok {
		return ErrLoginCheckRejected
	}
	if verifiedUid != "" && verifiedUid != args.UserId {
		return fmt.Errorf("%w: 凭据绑定 userId=%s 请求 userId=%s",
			ErrLoginUserIdMismatch, verifiedUid, args.UserId)
	}
	return nil
}

// GenerateLoginToken 用当前配置的密钥为 userId 签发一个 ttl 后过期的登录 token。
// 业务登录服务在完成账号验证后调用，把返回值下发给客户端。
func GenerateLoginToken(userId string, ttl time.Duration) (string, error) {
	secret := resolveLoginTokenSecret()
	if len(secret) == 0 {
		return "", internal.ErrLoginTokenSecretEmpty
	}
	return internal.BuildLoginToken(secret, userId, time.Now().Add(ttl))
}

// UserLoginWithToken 是携带凭据的 gate 登录入口（替代旧的 UserLogin）。
// 业务服务在验证账号后：
//
//	token, _ := rpc.GenerateLoginToken(userId, 12*time.Hour) // 或自定义体系签发
//	res, err := rpc.UserLoginWithToken(ctx, userId, token)
//
// 旧的 UserLogin(ctx, userId) 不带凭据——默认鉴权开启时会被 gate.Login 拒绝，
// 仅在 WithoutLoginCheck() 之后可用。
func UserLoginWithToken(ctx context.Context, userId, token string) (*LoginRes, error) {
	return SendRPCMessage(ctx, Login.New(&LoginReq{
		UserId:         userId,
		TemplateUserId: GetTemplateUserId(ctx),
		Token:          token,
	}, &LoginRes{}))
}

// resetLoginCheckForTest 恢复鉴权配置默认态（开启 + 无自定义校验 + 无显式密钥），
// 仅测试用。
func resetLoginCheckForTest() {
	loginCheckDisabled.Store(false)
	customLoginCheck.Store(loginCheckBox{})
	loginTokenSecret.Store("")
}
