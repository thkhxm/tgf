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
// 兼容性说明：
// 本包**不替换**老的 `tgf.GetStrConfig` / `tgf.Environment` 常量，两套 API 并存。
// 老调用点走 `tgf.mapping`，新调用点走 `config.Current()`。两边都从同一批环境
// 变量读取（env tag 和老常量名对齐），所以读同一个值。
// v2 后续小版本里会给老 API 标 deprecated，等调用点迁移完再删。
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
	return cfg, nil
}

// Reload 重新读取环境变量并原子替换当前配置。
// 读者要么看到旧 *Config，要么看到新 *Config，不会出现字段一半新一半旧。
//
// 调用顺序：
//  1. 串行化锁（避免并发 Reload 重复工作）
//  2. loadFromEnv 构造新 Config
//  3. atomic.Pointer.Store 替换 current
//  4. 触发 OnReload 钩子（每个钩子 panic 被 recover，不影响后续）
func Reload() (*Config, error) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	cfg, err := loadFromEnv()
	if err != nil {
		return nil, err
	}
	current.Store(cfg)

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

// fillStruct 递归填充一个 struct value 的所有字段。
// 支持的类型：string, int, int32, int64, float64, bool, time.Duration, []string。
// 嵌套 struct 会递归下钻——顶层 Config 有 Logger / Redis / ... 嵌套 struct。
//
// env 优先级：os.Getenv(envTag) → default tag → 零值
func fillStruct(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)

		// 嵌套 struct 递归处理（包括 time.Duration 这类，time.Duration 是 int64
		// 的别名，会走下面的 kind 分支，不会进到这个递归）
		if field.Kind() == reflect.Struct && !isDurationType(field.Type()) {
			if err := fillStruct(field); err != nil {
				return err
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
			return fmt.Errorf("config: 必填字段 %v (env=%v) 未设置", ft.Name, envKey)
		}

		if err := assignField(field, raw, ft.Name, envKey); err != nil {
			return err
		}
	}
	return nil
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
	reloadHooksMu.Lock()
	reloadHooks = nil
	reloadHooksMu.Unlock()
}
