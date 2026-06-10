// Package config 是 tgf v2 的新配置系统。
//
// 旧路径（`tgf/config.go` + `tgf/define.go`）的痛点（路线图 C3 / #19）：
//   - 加一个新环境变量要改三处：`Environment` 常量 + 默认值常量 + `initMapping()` 登记
//   - 没有类型校验——`GetStrConfig[int]` 拿到非法值会静默返回 0
//   - 没有 required 检查
//   - 没有热更钩子
//
// 本包用 struct tag 驱动的反射加载器一次性解决这几个问题：
//
//	type Config struct {
//	    Logger  LoggerConfig
//	    Redis   RedisConfig
//	    MySQL   MySQLConfig
//	    Consul  ConsulConfig
//	    Service ServiceConfig
//	}
//
//	type LoggerConfig struct {
//	    Path        string `env:"LogPath"        default:"./log/tgf.log"`
//	    Level       string `env:"LogLevel"       default:"debug"`
//	    IgnoredTags string `env:"LogIgnoredTags" default:""`
//	}
//
// 加一个新字段只需要改 struct 定义一处，自动生效。
//
// 兼容性说明（E2 配置收敛后）：
// 本包是配置的**唯一解析真源**。老的 `tgf.GetStrConfig` / `tgf.GetStrListConfig`
// 已改造为读本包规范化快照（见 GetString / Snapshot）的薄适配层——同一个环境
// 变量只经过本包一次 parse pass，新旧 API 读到的永远是同一份解析结果。
// 特别地，bool 类变量（RedisCluster / GatePush / LogCompress / LogLocalTime）在
// 快照中统一规范化为 "1" / "0"，因此旧调用点 `GetStrConfig[int]` 读 "true" / "yes"
// 等宽松写法也能得到正确的 1（修复 V3 审计指出的双轨 bool 解析漂移）。
//
// 热更：config.Reload() 是显式触发 API（框架不内置信号处理）。tgf.InitConfig 会
// 通过 RegisterEnvFileLoader 注入"重读 .env.<module> 文件"的 loader，使
// "改 .env 文件 → 调 Reload() → 新值对新旧两套 API 同时生效"的语义成立。
// OnReload 订阅者在 Reload 成功后按注册顺序收到新 *Config。
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---- Config 结构 ----

// Config 是 tgf 所有配置的聚合根。C3 首版的字段和旧 Environment 常量完全对齐，
// 未来新增配置直接在这里加字段即可。
type Config struct {
	Logger  LoggerConfig
	Redis   RedisConfig
	MySQL   MySQLConfig
	Consul  ConsulConfig
	Service ServiceConfig
	Gate    GateConfig
	Runtime RuntimeConfig
	// v2 新增子配置
	RPC RPCConfig
	DB  DBConfig
	// E2 新增：登记 D 档遗留的 os.Getenv 直读凭据类变量
	Security SecurityConfig
}

// SecurityConfig E2 新增：凭据 / 安全类配置。
// 这两个变量在 D 档由 rpc/login_check.go、rpc/admin.go 以 os.Getenv 直读，
// E2 把它们登记进配置系统（默认值为空，保持 fail-closed 语义），
// 使 tgf.GetStrConfig 可安全读取（消除未注册 key 的 nil-deref 风险），
// 并让 Reload 后的新值能被统一观测。env 名保持与 D 档常量
// （rpc.EnvLoginTokenSecret / rpc.AdminTokenEnv）完全一致，存量部署不受影响。
type SecurityConfig struct {
	// LoginTokenSecret 网关默认登录鉴权（HMAC token）的密钥。
	// 为空时默认鉴权 fail-closed 拒绝登录（见 rpc/login_check.go）。
	LoginTokenSecret string `env:"LoginTokenSecret" default:""`
	// AdminToken admin 控制面的运维口令。
	// 为空时 admin 控制面 fail-closed 返回 503（见 rpc/admin.go）。
	AdminToken string `env:"ADMIN_TOKEN" default:""`
}

type LoggerConfig struct {
	Path        string `env:"LogPath" default:"./log/tgf.log"`
	Level       string `env:"LogLevel" default:"debug"`
	IgnoredTags string `env:"LogIgnoredTags" default:""`

	// v2 新增：lumberjack 滚动切割参数（之前是 log 包内部硬编码）
	MaxSize     int    `env:"LogMaxSize" default:"512"`             // 单个文件最大 MB
	MaxAge      int    `env:"LogMaxAge" default:"0"`                // 最多保留天数（0=不按时间删）
	MaxBackups  int    `env:"LogMaxBackups" default:"100"`          // 最多保留文件数
	Compress    bool   `env:"LogCompress" default:"false"`          // 滚动后是否 gzip 压缩
	LocalTime   bool   `env:"LogLocalTime" default:"true"`          // 归档文件名是否本地时间
	TimeFormat  string `env:"LogTimeFormat" default:"2006-01-02 15:04:05.000"`
	ServiceFile string `env:"LogServiceFile" default:"service/service.log"` // service tag 专用文件
	DBFile      string `env:"LogDBFile" default:"db/db.log"`                // db tag 专用文件
}

type RedisConfig struct {
	Addr     string `env:"RedisAddr" default:"127.0.0.1:6379"`
	Password string `env:"RedisPassword" default:""`
	DB       int    `env:"RedisDB" default:"1"`
	Cluster  bool   `env:"RedisCluster" default:"false"`
}

type MySQLConfig struct {
	User     string `env:"MySqlUser" default:"root"`
	Password string `env:"MySqlPwd" default:"123456"`
	Addr     string `env:"MySqlAddr" default:"127.0.0.1"`
	Port     string `env:"MySqlPort" default:"3306"`
	DB       string `env:"MySqlDB" default:"tgf"`

	// v2 新增：连接池参数（之前是 db/mysql.go 硬编码 10/200/300s）
	MaxIdleConns       int `env:"MySqlMaxIdleConns" default:"10"`
	MaxOpenConns       int `env:"MySqlMaxOpenConns" default:"200"`
	ConnMaxLifetimeSec int `env:"MySqlConnMaxLifetimeSec" default:"300"`
}

// RPCConfig v2 新增：RPC / 网关层运行参数。
type RPCConfig struct {
	// DefaultTimeoutMs 默认 RPC 调用超时（毫秒），原 5 秒硬编码
	DefaultTimeoutMs int `env:"RPCDefaultTimeoutMs" default:"5000"`
	// TCPDeadLineSec TCP/WS 连接读 idle 超时（秒），原 60 秒硬编码
	TCPDeadLineSec int `env:"TCPDeadLineSec" default:"60"`
	// TCPWriteTimeoutMs 网关连接写超时（毫秒），原 5 秒硬编码
	TCPWriteTimeoutMs int `env:"TCPWriteTimeoutMs" default:"5000"`
	// TCPSendChanTimeoutMs Send 推 writeChan 的最大等待时间（毫秒），原 3 秒硬编码
	TCPSendChanTimeoutMs int `env:"TCPSendChanTimeoutMs" default:"3000"`
}

// DBConfig v2 新增：DB 层默认参数。
type DBConfig struct {
	// CacheTimeoutSec Redis 缓存默认 TTL（秒），原 3 天硬编码
	CacheTimeoutSec int64 `env:"DBCacheTimeoutSec" default:"259200"`
	// MemTimeoutSec 内存缓存默认 TTL（秒），原 3 小时硬编码
	MemTimeoutSec int64 `env:"DBMemTimeoutSec" default:"10800"`
}

type ConsulConfig struct {
	Address string `env:"ConsulAddress" default:"127.0.0.1:8500"`
	Path    string `env:"ConsulPath" default:"/tgf"`
}

type ServiceConfig struct {
	Port    string `env:"ServicePort" default:"8082"`
	Address string `env:"ServiceAddress" default:"127.0.0.1"`
}

type GateConfig struct {
	Push bool `env:"GatePush" default:"true"`
}

type RuntimeConfig struct {
	// Module 取值 dev / test / release
	Module string `env:"RuntimeModule" default:"dev"`
}

// ---- 加载与热更 ----

// current 是当前活动配置的 atomic pointer。首次 Load 之前为 nil。
var current atomic.Pointer[Config]

// reloadMu 保证 Reload 串行执行。
var reloadMu sync.Mutex

// reloadHooks 注册的回调列表，在 Reload 成功后触发。
var (
	reloadHooksMu sync.RWMutex
	reloadHooks   []func(*Config)
)

// envSnapshot 是当前 Config 的"规范化字符串快照"：env key → canonical 字符串值。
// 它和 current 在 Load / LoadLenient / Reload 成功时同步原子替换，是旧
// tgf.GetStrConfig 适配层的读取源——保证新旧两套 API 读同一份解析结果。
// 规范化规则见 canonicalString（bool → "1"/"0"，数值 → 十进制等）。
var envSnapshot atomic.Pointer[map[string]string]

// envFileLoader 是 Reload 前重读 .env 文件的钩子（由 tgf.InitConfig 注入
// godotenv.Overload 闭包）。本包保持纯标准库，不直接依赖 godotenv。
var (
	envFileLoaderMu sync.RWMutex
	envFileLoader   func() error
)

// RegisterEnvFileLoader 注册"重读 .env 文件"的 loader，Reload 会在重新解析
// 环境变量之前调用它（典型实现：godotenv.Overload(".env.<module>")，用文件值
// 覆盖进程环境变量，使"改 .env 文件后热更"语义成立）。
// 传 nil 可注销 loader（测试隔离用）。loader 返回错误不会阻断 Reload——
// Reload 会打印警告并继续用当前进程环境变量解析。
func RegisterEnvFileLoader(fn func() error) {
	envFileLoaderMu.Lock()
	envFileLoader = fn
	envFileLoaderMu.Unlock()
}

// runEnvFileLoader 执行已注册的 env 文件 loader；未注册时为 no-op。
func runEnvFileLoader() error {
	envFileLoaderMu.RLock()
	fn := envFileLoader
	envFileLoaderMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

// Load 读取一次环境变量，根据 struct tag 填充默认值、做类型转换。
// 返回的 *Config 已经被设置为当前快照（和 Current() 等价）。
//
// 使用方式：
//
//	cfg, err := config.Load()
//	if err != nil {
//	    log.Fatalf("配置加载失败: %v", err)
//	}
//	log.Printf("redis addr = %s", cfg.Redis.Addr)
func Load() (*Config, error) {
	cfg, err := loadFromEnv()
	if err != nil {
		return nil, err
	}
	current.Store(cfg)
	storeSnapshot(cfg)
	return cfg, nil
}

// LoadLenient 是 Load 的宽容版本，用于进程启动路径（tgf.InitConfig）：
// 单个字段解析失败不会中断整体加载，而是回退该字段的 default 值并把错误
// 收集进返回值，保证启动方总能拿到一个完整可用的 Config。
//
// 与 Load（严格、fail-fast，适合业务显式校验）和 Reload（严格、失败保旧值，
// 适合运行期热更）的失败语义刻意不同：启动时"尽力而为 + 错误可观测"，
// 运行期"坏配置绝不吃进去"。
func LoadLenient() (*Config, []error) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	cfg := &Config{}
	var errs []error
	fillStructInto(reflect.ValueOf(cfg).Elem(), true, &errs)
	current.Store(cfg)
	storeSnapshot(cfg)
	return cfg, errs
}

// Reload 重新读取环境变量并原子替换当前配置。
// 读者要么看到旧 *Config，要么看到新 *Config，不会出现字段一半新一半旧。
// 解析失败时返回 error 且 current / 快照保持旧值——坏配置不会被热更吃进去。
//
// 这是框架的**显式热更触发 API**（框架不内置 SIGHUP 等信号处理）：
// 业务在自己的信号 handler / admin 路由 / 控制台命令里调用本函数即可。
//
// 调用顺序：
//  1. 串行化锁（避免并发 Reload 重复工作）
//  2. runEnvFileLoader 重读 .env 文件（tgf.InitConfig 注入；失败仅警告不阻断）
//  3. loadFromEnv 构造新 Config
//  4. atomic.Pointer.Store 替换 current 与规范化快照（旧 GetStrConfig 同步生效）
//  5. 触发 OnReload 钩子（每个钩子 panic 被 recover，不影响后续）
func Reload() (*Config, error) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	if err := runEnvFileLoader(); err != nil {
		fmt.Printf("[config] Reload 重读 env 文件失败(继续使用当前进程环境变量): %v\n", err)
	}

	cfg, err := loadFromEnv()
	if err != nil {
		return nil, err
	}
	current.Store(cfg)
	storeSnapshot(cfg)

	reloadHooksMu.RLock()
	hooks := make([]func(*Config), len(reloadHooks))
	copy(hooks, reloadHooks)
	reloadHooksMu.RUnlock()
	for _, h := range hooks {
		safeCallReload(h, cfg)
	}
	return cfg, nil
}

func safeCallReload(fn func(*Config), cfg *Config) {
	defer func() { _ = recover() }()
	fn(cfg)
}

// Current 返回当前快照。首次 Load 之前返回一个全零值的空配置——调用方可以
// 安全读取（全部是默认零值），但业务逻辑应该确保 Load 在任何 Current 调用前
// 已经执行过。
func Current() *Config {
	if c := current.Load(); c != nil {
		return c
	}
	return &Config{}
}

// OnReload 注册一个重载完成回调。按注册顺序调用，panic 被内部 recover。
func OnReload(fn func(*Config)) {
	if fn == nil {
		return
	}
	reloadHooksMu.Lock()
	reloadHooks = append(reloadHooks, fn)
	reloadHooksMu.Unlock()
}

// ---- 规范化字符串快照（旧 API 适配层的读取源）----

// GetString 按 env key 读取当前快照中的规范化字符串值。
// 第二个返回值为 false 表示该 key 未在 Config struct 中登记，或快照尚未建立
// （Load / LoadLenient / Reload 任一成功执行前）。
//
// 这是旧 tgf.GetStrConfig / tgf.GetStrListConfig 的底层读取 API：
// 值经过本包统一解析与规范化（bool → "1"/"0"），新旧 API 因此读同一份结果。
func GetString(envKey string) (string, bool) {
	mp := envSnapshot.Load()
	if mp == nil {
		return "", false
	}
	v, ok := (*mp)[envKey]
	return v, ok
}

// Snapshot 返回当前规范化快照的拷贝（env key → canonical 值），
// 供启动日志打印 / 诊断使用。快照未建立时返回空 map。
func Snapshot() map[string]string {
	out := make(map[string]string)
	if mp := envSnapshot.Load(); mp != nil {
		for k, v := range *mp {
			out[k] = v
		}
	}
	return out
}

// storeSnapshot 由 cfg 导出规范化快照并原子替换。
func storeSnapshot(cfg *Config) {
	m := exportEnvMap(cfg)
	envSnapshot.Store(&m)
}

// exportEnvMap 反射遍历 Config，把每个带 env tag 的字段导出为
// "env key → 规范化字符串"映射。
func exportEnvMap(cfg *Config) map[string]string {
	out := make(map[string]string, 64)
	collectEnvMap(reflect.ValueOf(cfg).Elem(), out)
	return out
}

// collectEnvMap 递归收集 struct 中带 env tag 字段的规范化值。
func collectEnvMap(v reflect.Value, out map[string]string) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)
		if field.Kind() == reflect.Struct && !isDurationType(field.Type()) {
			collectEnvMap(field, out)
			continue
		}
		envKey := ft.Tag.Get("env")
		if envKey == "" {
			continue
		}
		out[envKey] = canonicalString(field)
	}
}

// canonicalString 把一个已解析字段渲染回规范化字符串。
// 关键规则：bool 统一为 "1"/"0"——旧调用点用 GetStrConfig[int]/[int32] 读
// bool 类变量（RedisCluster / GatePush），规范化后 "true"/"yes" 等宽松写法
// 也能被旧路径正确解析，消除双轨漂移。
func canonicalString(field reflect.Value) string {
	if isDurationType(field.Type()) {
		return time.Duration(field.Int()).String()
	}
	switch field.Kind() {
	case reflect.String:
		return field.String()
	case reflect.Bool:
		if field.Bool() {
			return "1"
		}
		return "0"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(field.Uint(), 10)
	case reflect.Float32:
		return strconv.FormatFloat(field.Float(), 'f', -1, 32)
	case reflect.Float64:
		return strconv.FormatFloat(field.Float(), 'f', -1, 64)
	case reflect.Slice:
		if ss, ok := field.Interface().([]string); ok {
			return strings.Join(ss, ",")
		}
	}
	return fmt.Sprintf("%v", field.Interface())
}

// ---- 反射加载核心 ----

// loadFromEnv 通过反射遍历 Config 结构的所有字段（包括嵌套 struct），
// 为每个带 env tag 的字段读环境变量 + 应用 default + 做类型转换。
// 返回的错误目前只来自类型转换失败——env 缺失不算错误（默认值接管）。
func loadFromEnv() (*Config, error) {
	cfg := &Config{}
	if err := fillStruct(reflect.ValueOf(cfg).Elem()); err != nil {
		return nil, err
	}
	return cfg, nil
}

// fillStruct 递归填充一个 struct value 的所有字段（严格模式，遇错即停）。
// 支持的类型：string, int, int32, int64, float64, bool, time.Duration, []string。
// 嵌套 struct 会递归下钻——顶层 Config 有 Logger / Redis / ... 嵌套 struct。
//
// env 优先级：os.Getenv(envTag) → default tag → 零值
func fillStruct(v reflect.Value) error {
	var errs []error
	fillStructInto(v, false, &errs)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// fillStructInto 是 fillStruct 的实现核心，支持两种失败语义：
//   - lenient=false（严格）：遇到第一个错误即停止填充（Load / Reload 路径）；
//   - lenient=true（宽容）：解析失败的字段回退 default 值并继续，所有错误
//     收集进 errs（LoadLenient / 进程启动路径）。
func fillStructInto(v reflect.Value, lenient bool, errs *[]error) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)

		// 嵌套 struct 递归处理（包括 time.Duration 这类，time.Duration 是 int64
		// 的别名，会走下面的 kind 分支，不会进到这个递归）
		if field.Kind() == reflect.Struct && !isDurationType(field.Type()) {
			fillStructInto(field, lenient, errs)
			if !lenient && len(*errs) > 0 {
				return
			}
			continue
		}

		envKey := ft.Tag.Get("env")
		if envKey == "" {
			// 没有 env tag 的字段跳过
			continue
		}
		defaultVal := ft.Tag.Get("default")
		required := ft.Tag.Get("required") == "true"

		raw := os.Getenv(envKey)
		if raw == "" {
			raw = defaultVal
		}
		if raw == "" && required {
			*errs = append(*errs, fmt.Errorf("config: 必填字段 %v (env=%v) 未设置", ft.Name, envKey))
			if !lenient {
				return
			}
			continue
		}

		if err := assignField(field, raw, ft.Name, envKey); err != nil {
			if !lenient {
				*errs = append(*errs, err)
				return
			}
			// 宽容模式：坏值回退 default，错误收集后继续后续字段
			*errs = append(*errs, fmt.Errorf("%w（已回退默认值 %q）", err, defaultVal))
			if defaultVal != raw {
				if derr := assignField(field, defaultVal, ft.Name, envKey); derr != nil {
					// default 本身也非法属于代码 bug，字段保持零值并记录
					*errs = append(*errs, derr)
				}
			}
		}
	}
}

// assignField 把字符串 raw 按 field 的具体类型转换后赋值。
// 转换失败返回带字段名的错误，便于调用方定位哪一项配错。
func assignField(field reflect.Value, raw, fieldName, envKey string) error {
	if !field.CanSet() {
		return fmt.Errorf("config: 字段 %v 不可赋值", fieldName)
	}

	// time.Duration 优先——它的 Kind 是 Int64，要先判别
	if isDurationType(field.Type()) {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("config: 字段 %v (env=%v) 解析 duration 失败 raw=%q: %w", fieldName, envKey, raw, err)
		}
		field.SetInt(int64(d))
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)

	case reflect.Bool:
		b, err := parseBool(raw)
		if err != nil {
			return fmt.Errorf("config: 字段 %v (env=%v) 解析 bool 失败 raw=%q", fieldName, envKey, raw)
		}
		field.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("config: 字段 %v (env=%v) 解析 int 失败 raw=%q", fieldName, envKey, raw)
		}
		if field.OverflowInt(n) {
			return fmt.Errorf("config: 字段 %v (env=%v) 溢出", fieldName, envKey)
		}
		field.SetInt(n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("config: 字段 %v (env=%v) 解析 uint 失败 raw=%q", fieldName, envKey, raw)
		}
		if field.OverflowUint(n) {
			return fmt.Errorf("config: 字段 %v (env=%v) 溢出", fieldName, envKey)
		}
		field.SetUint(n)

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("config: 字段 %v (env=%v) 解析 float 失败 raw=%q", fieldName, envKey, raw)
		}
		field.SetFloat(f)

	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("config: 字段 %v (env=%v) 只支持 []string 切片", fieldName, envKey)
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		field.Set(reflect.ValueOf(out))

	default:
		return fmt.Errorf("config: 字段 %v 不支持的类型 %v", fieldName, field.Kind())
	}
	return nil
}

// parseBool 比 strconv.ParseBool 更宽松，接受 "0" / "1" / "true" / "false" / "yes" / "no"（大小写无关）。
func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off", "":
		return false, nil
	default:
		return false, fmt.Errorf("%q", raw)
	}
}

// isDurationType 判断一个反射类型是不是 time.Duration。
// time.Duration 的 underlying type 是 int64，用 Kind 判别会混淆，必须用类型身份。
var durationType = reflect.TypeOf(time.Duration(0))

func isDurationType(t reflect.Type) bool {
	return t == durationType
}

// ---- 测试辅助 ----

// resetForTest 清除所有全局状态，测试之间隔离。生产代码不应调用。
func resetForTest() {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	current.Store(nil)
	envSnapshot.Store(nil)
	reloadHooksMu.Lock()
	reloadHooks = nil
	reloadHooksMu.Unlock()
	envFileLoaderMu.Lock()
	envFileLoader = nil
	envFileLoaderMu.Unlock()
}
