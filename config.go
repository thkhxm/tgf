package tgf

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/util"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

// E2 配置收敛说明：
//
// 本文件历史上维护一份独立的 mapping（Environment → 默认值 + os.Getenv 快照），
// 与 tgf/config 包的 struct tag 解析构成"双轨"——同一个 bool 变量
// （RedisCluster / GatePush）写 "true" 时新系统解析为 true、旧路径 ParseInt
// 失败静默得 0（V3 审计 P1）。E2 后双轨合一：
//
//   - tgf/config 包是唯一 parse pass（默认值也以 struct tag 为唯一真源）；
//   - GetStrConfig / GetStrListConfig 改为读 config.GetString 的规范化快照
//     （bool 统一为 "1"/"0"），签名与读取语义保持向后兼容；
//   - config.Reload()（显式热更 API）原子替换快照后，旧 API 同步读到新值，
//     无需任何同步钩子；InitConfig 还注入了"重读 .env.<module> 文件"的
//     loader，使"改 .env 文件 → Reload() → 生效"链路成立；
//   - 未注册 key 不再 nil-deref panic，而是兜底直读 os.Getenv 并打一次警告。

// initConfigOnce 防止 InitConfig 重入（旧实现重复调用会因 flag 重复注册 panic）。
var initConfigOnce sync.Once

// sensitiveEnvKeys 启动日志打印时需要脱敏的配置项（值替换为 ******）。
var sensitiveEnvKeys = map[string]bool{
	string(EnvironmentLoginTokenSecret): true,
	string(EnvironmentAdminToken):       true,
	string(EnvironmentRedisPassword):    true,
	string(EnvironmentMySqlPwd):         true,
}

// warnedConfigKeys 用于"未注册 key / 类型转换失败"警告的去重（每个 key 只打一次）。
var warnedConfigKeys sync.Map

// warnConfigOnce 对同一 key 只打印一次警告，避免热路径刷屏。
func warnConfigOnce(key, format string, args ...any) {
	if _, loaded := warnedConfigKeys.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	fmt.Printf(format, args...)
	fmt.Println()
}

// GetStrConfig 读取一个已登记配置项的值（薄适配层）。
//
// 值来自 tgf/config 包的规范化快照——与 config.Current() 是同一次解析的产物，
// 因此新旧两套 API 永远一致。bool 类变量（RedisCluster / GatePush 等）在快照中
// 统一为 "1"/"0"，用 GetStrConfig[int] 读取宽松写法（"true"/"yes"/"on"）也能
// 得到正确结果。
//
// 未在 config.Config 中登记的 key（业务自定义 Environment 常量）兜底直读
// os.Getenv 并打一次警告——不再像旧实现那样 nil-deref panic。
func GetStrConfig[T int | int32 | string | int64 | float32 | float64](env Environment) (res T) {
	raw, ok := tgfconfig.GetString(string(env))
	if !ok {
		raw = os.Getenv(string(env))
		warnConfigOnce("unregistered:"+string(env),
			"[warn] [tgf/config.go] 配置项 %v 未在 config.Config 中登记,已兜底直读环境变量(建议登记进 tgf/config 包)", env)
	}
	res, err := util.StrToAny[T](raw)
	if err != nil && raw != "" {
		warnConfigOnce("convert:"+string(env),
			"[warn] [tgf/config.go] 配置项 %v 的值 %q 转换失败(返回零值): %v", env, raw, err)
	}
	return
}

// GetStrListConfig 读取一个逗号分隔的配置项并切分为字符串切片。
// 与 GetStrConfig 同源（规范化快照）。各分段会 trim 空白；
// 空配置返回空切片（旧实现会返回 [""]——长度 1 的含空串切片，V3 审计 P2）。
func GetStrListConfig(env Environment) (res []string) {
	raw, ok := tgfconfig.GetString(string(env))
	if !ok {
		raw = os.Getenv(string(env))
	}
	res = make([]string, 0)
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			res = append(res, s)
		}
	}
	return
}

// InitConfig 初始化配置：按 TGFMODULE 加载 .env.<module> 文件，然后执行
// tgf/config 包的统一解析（LoadLenient——单字段配错回退默认值并打印错误，
// 不会让进程起不来）。幂等：重复调用只生效一次（旧实现重复调用会 panic）。
//
// 运行模式只取 TGFMODULE 环境变量（缺省 dev）。旧版宣称支持 -TGFMODULE
// 命令行 flag，但从未调用 flag.Parse，恒为默认值（V3 审计 P2 死代码），已移除。
func InitConfig() {
	initConfigOnce.Do(doInitConfig)
}

func doInitConfig() {
	env := os.Getenv("TGFMODULE")
	if env == "" {
		env = RuntimeModuleDev
	}
	fmt.Printf("[tgf/init.go] 当前运行模式 [TGFMODULE] 为 %v", env)
	fmt.Println()
	fileName := ".env." + env
	// 加载环境配置文件（不覆盖已存在的环境变量——部署注入的 env 优先于文件）
	err := godotenv.Load(fileName)
	if err != nil {
		fmt.Printf("[init] [tgf/init.go] 找不到指定的env文件 %v", fileName)
		fmt.Println()
	}

	// E2：给 config.Reload 注入"重读 .env 文件"的 loader，使
	// "改 .env.<module> → config.Reload() → 新值生效"的热更语义成立。
	// 注意 Reload 用的是 Overload（文件值覆盖进程环境变量）——热更的主诉求
	// 是"改文件生效"；文件中不存在的 key 不受影响。文件不存在（纯环境变量
	// 部署）时为 no-op。
	tgfconfig.RegisterEnvFileLoader(func() error {
		if _, statErr := os.Stat(fileName); statErr != nil {
			return nil
		}
		return godotenv.Overload(fileName)
	})

	// 唯一 parse pass：解析 + 建立规范化快照（GetStrConfig 的读取源）。
	_, errs := tgfconfig.LoadLenient()
	for _, e := range errs {
		fmt.Printf("[init] [tgf/config.go] 配置解析错误(已回退默认值): %v", e)
		fmt.Println()
	}
	printConfigSnapshot()
}

// printConfigSnapshot 打印当前配置快照（凭据类配置脱敏），保持旧版
// initMapping 的启动可观测性。
func printConfigSnapshot() {
	snapshot := tgfconfig.Snapshot()
	keys := make([]string, 0, len(snapshot))
	for k := range snapshot {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := snapshot[k]
		if sensitiveEnvKeys[k] && val != "" {
			val = "******"
		}
		fmt.Printf("env=%v val=%v\n", k, val)
	}
}
