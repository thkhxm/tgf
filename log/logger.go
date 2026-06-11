package log

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/thkhxm/tgf/v2"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

var logger *zap.Logger
var slogger *zap.SugaredLogger

// atomicLevel 是所有 core 共享的可热更日志级别。
//
// E3/C9 说明：v1 把 level 在 initLogger 里 ParseLevel 后写死进每个 core，运行期
// 改 LogLevel 不生效。改用 zap.AtomicLevel 后，`tgfconfig.OnReload` 订阅者在
// config.Reload() 成功时调 SetLevel 即可热更级别，无需重建整套 logger——这是
// C9 注释里"直读 os.Getenv 让运行期 reload 生效"承诺的正确落地方式（用配置系统
// 的 Reload 链路，而不是脱离配置系统裸读环境变量）。
var atomicLevel = zap.NewAtomicLevelAt(zapcore.DebugLevel)

// reloadHookOnce 保证 tgfconfig.OnReload 订阅只注册一次（initLogger 可能被
// 重复调用——例如测试或显式重建）。
var reloadHookOnce atomic.Bool

// ---- lumberjack 滚动参数（initLogger 时从统一配置系统读取，无配置走硬编码默认） ----
//
// v2 暴露前这些都是 private 硬编码。现在通过 EnvironmentLogger* 环境变量可配置，
// 由 tgf/config 包统一解析（见 CONFIG 契约）。对应 LoggerConfig 字段：
//
//	LogMaxSize      MaxSize      单个文件最大 MB         默认 512
//	LogMaxAge       MaxAge       最多保留天数            默认 0（不按时间删）
//	LogMaxBackups   MaxBackups   最多保留文件数          默认 100
//	LogCompress     Compress     滚动后是否 gzip 压缩    默认 false
//	LogLocalTime    LocalTime    归档文件名是否本地时间  默认 true
//	LogTimeFormat   TimeFormat   日志时间戳格式          默认 "2006-01-02 15:04:05.000"
//	LogServiceFile  ServiceFile  service tag 专用文件名  默认 "service/service.log"
//	LogDBFile       DBFile       db tag 专用文件名       默认 "db/db.log"
//
// 这些是"启动期一次性项"——滚动切割参数只在 newCore 构造 lumberjack 时读一次，
// 运行期改它们需要重建 core（重新 initLogger）。唯一支持原子热更的是日志级别
// （走 atomicLevel + tgfconfig.OnReload），见 atomicLevel 注释。
var (
	runtimeMaxSize    = 512
	runtimeMaxAge     = 0
	runtimeMaxBackups = 100
	runtimeCompress   = false
	runtimeLocalTime  = true
	runtimeTimeFormat = "2006-01-02 15:04:05.000"
	ignoredTags       map[string]bool
)

const (
	GAMETAG    = "game"
	DBTAG      = "db"
	SERVICETAG = "service"
)

// B5 说明 — 日志热路径的两条优化原则：
//
//  1. 所有 `*Tag` / `*` 家族在执行 `fmt.Sprintf` 之前，先用 `logger.Check(level, "")`
//     过一遍 zap 的 level/sampling 过滤器。当 LogLevel 被设置为 WARN 时，所有
//     `DebugTag` / `InfoTag` 调用的 Sprintf 会被跳过——这是最大的热路径收益点。
//     之前版本只做了 tag 过滤，tag 未过滤时仍然会 Sprintf 再让 zap 丢弃。
//
//  2. 新代码建议直接用 `*W` 后缀的 zap.Field 版本（`InfoTagW` / `DebugTagW` 等）。
//     这些函数完全不触碰 `fmt.Sprintf`，调用方手写 `zap.String("k", v)` 避免
//     interface{} 装箱。老的 `InfoTag(...)` 式 API 会保留到 v2 发布后一版，
//     在 CHANGELOG 标注 deprecated。

// checkTag 是 tag 过滤 + level 过滤的统一入口。返回值：
//   - nil 表示消息应被完全丢弃（tag 过滤 / level 过滤命中任一）
//   - 非 nil 的 CheckedEntry 表示应当执行 Sprintf 并调 Write
//
// 这个函数是 B5 的热路径，必须零分配：所有分支都走 pointer 比较和 map 读取，
// 不做 interface 装箱、不分配闭包。
func checkTag(level zapcore.Level, tag string) *zapcore.CheckedEntry {
	// tag 过滤放在 level 过滤前——map 读取比 zap 的 level 比较稍贵一点，但
	// 实际上 ignoredTags 命中率很低，而 level 过滤命中率可能很高；调换顺序
	// 在 "log level WARN、DebugTag 调用" 的最常见场景下可以少一次 map lookup。
	// 不过为了保持语义清晰（先判断 tag，再让 zap 判断 level），这里还是
	// tag → level 的顺序。如果未来 profiling 显示这是瓶颈，再调换。
	if ignoredTags != nil && ignoredTags[tag] {
		return nil
	}
	return logger.Check(level, "")
}

// ---- Sprintf 风格的老 API（带 level + tag 双重前置检查） ----

func Info(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.InfoLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func InfoTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.InfoLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

func Debug(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.DebugLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func DebugTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.DebugLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

func Warn(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.WarnLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func WarnTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.WarnLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

func Error(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.ErrorLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func ErrorTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.ErrorLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

// ---- zap.Field 风格的新 API（零分配热路径） ----

// InfoW / DebugW / WarnW / ErrorW 是对 `zap.Logger.Info(msg, fields...)` 的直通，
// 避免 Sprintf 分配。msg 固定字符串，字段用 zap.String / zap.Int 等手写传入。
//
// 示例：
//
//	log.DebugW("接收到连接", zap.String("addr", addr), zap.Int("fd", fd))
func InfoW(msg string, fields ...zap.Field)  { logger.Info(msg, fields...) }
func DebugW(msg string, fields ...zap.Field) { logger.Debug(msg, fields...) }
func WarnW(msg string, fields ...zap.Field)  { logger.Warn(msg, fields...) }
func ErrorW(msg string, fields ...zap.Field) { logger.Error(msg, fields...) }

// InfoTagW 等系列带 tag 过滤。tag 命中 ignoredTags 时完全短路。
func InfoTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Info(msg, append(fields, zap.String("tag", tag))...)
}

func DebugTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Debug(msg, append(fields, zap.String("tag", tag))...)
}

func WarnTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Warn(msg, append(fields, zap.String("tag", tag))...)
}

func ErrorTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Error(msg, append(fields, zap.String("tag", tag))...)
}

// ---- E3-log 结构化字段对接 ----
//
// 目标：让 traceId / userId / nodeId 等链路关键字段以**结构化 zap.Field** 输出
// （JSON 编码器下成为独立 key），便于 Loki / ELK 直接按字段提取与检索，而不是
// 被 Sprintf 进 message 文本里靠正则捞。
//
// 这些是预置的零分配字段构造器（直接转发 zap.XxxString，无装箱、无 Sprintf），
// 框架热路径迁移到 *W/*TagW 系列时配套使用。例如 db 落库日志：
//
//	log.DebugTagW(log.DBTAG, "落库完成",
//	    log.TraceID(traceId), zap.String("db", "tgf"), zap.Int32("count", n))
//
// 而不是：log.DebugTag("db", "落库完成 trace=%s db=%s count=%d", traceId, "tgf", n)

// 链路 / 业务关键字段的标准 key 名——集中定义避免各包各写一套，保证 Loki/ELK
// 侧字段名一致可聚合。
const (
	FieldTraceID = "traceId"
	FieldUserID  = "userId"
	FieldNodeID  = "nodeId"
	FieldModule  = "module"
	FieldMethod  = "name"
	FieldAddr    = "addr"
)

// TraceID 构造 traceId 结构化字段。空 traceId 也照常输出（空串），保证字段位置
// 稳定，便于下游按存在性聚合。
func TraceID(traceId string) zap.Field { return zap.String(FieldTraceID, traceId) }

// UserID 构造 userId 结构化字段。
func UserID(userId string) zap.Field { return zap.String(FieldUserID, userId) }

// NodeID 构造 nodeId 结构化字段（默认填当前进程 NodeId）。
func NodeID() zap.Field { return zap.String(FieldNodeID, tgf.NodeId) }

// Module 构造 module 结构化字段。
func Module(module string) zap.Field { return zap.String(FieldModule, module) }

// Method 构造 method/name 结构化字段。
func Method(name string) zap.Field { return zap.String(FieldMethod, name) }

// Addr 构造 addr 结构化字段。
func Addr(addr string) zap.Field { return zap.String(FieldAddr, addr) }

// InfoTagWT / DebugTagWT / WarnTagWT / ErrorTagWT 是 *TagW 的"带 traceId"快捷
// 版本：把 traceId 作为一等结构化字段前置，省去调用方每次手写 log.TraceID(...)。
// tag 命中 ignoredTags 时完全短路（零分配）。
//
// 这是 E3-log 推荐的网关 / RPC / DB 热路径写法——一条请求从网关到服务到 DB 的
// 全链路日志只要带同一个 traceId，Loki/ELK 即可按 traceId 串起整条调用链。
func InfoTagWT(tag, traceId, msg string, fields ...zap.Field) {
	tagWriteWithTrace(zapcore.InfoLevel, tag, traceId, msg, fields)
}

func DebugTagWT(tag, traceId, msg string, fields ...zap.Field) {
	tagWriteWithTrace(zapcore.DebugLevel, tag, traceId, msg, fields)
}

func WarnTagWT(tag, traceId, msg string, fields ...zap.Field) {
	tagWriteWithTrace(zapcore.WarnLevel, tag, traceId, msg, fields)
}

func ErrorTagWT(tag, traceId, msg string, fields ...zap.Field) {
	tagWriteWithTrace(zapcore.ErrorLevel, tag, traceId, msg, fields)
}

// tagWriteWithTrace 是 *TagWT 系列的公共实现：tag 短路 + level 前置过滤后，
// 把 traceId 与 tag 作为结构化字段一并写出。level 过滤命中时连 traceId 字段都
// 不构造，保持热路径零开销。
func tagWriteWithTrace(level zapcore.Level, tag, traceId, msg string, fields []zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	if ce := logger.Check(level, msg); ce != nil {
		ce.Write(append(fields, zap.String(FieldTraceID, traceId), zap.String("tag", tag))...)
	}
}

// ---- 其它现有 API（保持不变，都是结构化字段调用，没 Sprintf 问题） ----

func SLogger() *zap.SugaredLogger {
	return slogger
}

func Game(userId, tag, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.InfoLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag), zap.String(FieldUserID, userId))
	}
}

func DB(traceId, dbName, script string, count int32) {
	logger.Debug(script, zap.String("tag", DBTAG), zap.String(FieldNodeID, tgf.NodeId), zap.String("db", dbName), zap.Int32("count", count), zap.String(FieldTraceID, traceId))
}

func Service(module, name, version, userId string, consume int64, code int32) {
	logger.Debug("", zap.String("tag", SERVICETAG),
		zap.String(FieldUserID, userId),
		zap.String(FieldModule, module),
		zap.String(FieldMethod, name),
		zap.String("version", version),
		zap.Int64("consume", consume),
		zap.Int32("code", code),
	)
}

// CheckLogTag 保留给老代码/外部调用兼容。新代码应当直接用 `*TagW` 系列。
func CheckLogTag(tag string) bool {
	return !ignoredTags[tag]
}

// loadLumberjackConfig 从统一配置系统（tgf/config）拉取 v2 新增的 log 配置项。
//
// CONFIG 契约：这些是"启动期一次性项"，走 tgfconfig.Current().Logger 读类型化
// 字段，无需字符串转换。tgf.InitConfig（包 init 自动执行）已保证 Current() 非
// 零值；纯导入 log 包而未导入 tgf 包的极端场景下 Current() 返回全零值 Config，
// 由 sanitizeLoggerConfig 兜底回硬编码默认，保证零配置行为不变。
//
// 历史说明（C9 → E3）：v1 这里裸读 os.Getenv，注释声称"让运行期 reload 生效"，
// 但 initLogger 整个生命周期只调一次，该理由空转，且与配置系统形成第三条读取
// 途径（mapping / os.Getenv / 新 Config 并存）。E3 收敛为统一走 Current()，运行
// 期热更改由 atomicLevel + tgfconfig.OnReload 承担（见 applyReloadableConfig）。
func loadLumberjackConfig() {
	lc := sanitizeLoggerConfig(tgfconfig.Current().Logger)
	runtimeMaxSize = lc.MaxSize
	runtimeMaxAge = lc.MaxAge
	runtimeMaxBackups = lc.MaxBackups
	runtimeCompress = lc.Compress
	runtimeLocalTime = lc.LocalTime
	runtimeTimeFormat = lc.TimeFormat
}

// sanitizeLoggerConfig 对 LoggerConfig 做边界兜底：当字段为零值（典型场景：未
// 导入 tgf 包导致 Current() 返回空 Config）时回退到硬编码默认值，保证行为与 v1
// 零配置一致。MaxSize/MaxBackups 必须为正、MaxAge 必须非负、TimeFormat 非空。
func sanitizeLoggerConfig(lc tgfconfig.LoggerConfig) tgfconfig.LoggerConfig {
	if lc.MaxSize <= 0 {
		lc.MaxSize = 512
	}
	if lc.MaxAge < 0 {
		lc.MaxAge = 0
	}
	if lc.MaxBackups <= 0 {
		lc.MaxBackups = 100
	}
	if strings.TrimSpace(lc.TimeFormat) == "" {
		lc.TimeFormat = "2006-01-02 15:04:05.000"
	}
	if strings.TrimSpace(lc.ServiceFile) == "" {
		lc.ServiceFile = "service/service.log"
	}
	if strings.TrimSpace(lc.DBFile) == "" {
		lc.DBFile = "db/db.log"
	}
	if strings.TrimSpace(lc.Path) == "" {
		lc.Path = "./log/tgf.log"
	}
	return lc
}

// applyReloadableConfig 是 tgfconfig.OnReload 的订阅者：config.Reload() 成功后
// 把可热更项原子地应用到运行态。当前可热更的只有日志级别（atomicLevel.SetLevel）
// ——滚动切割参数（MaxSize 等）改动需要重建 lumberjack，不在热更范围（如业务确有
// 需求可在回调里再调 initLogger，但这会丢历史 sink，故默认不做）。
func applyReloadableConfig(c *tgfconfig.Config) {
	if c == nil {
		return
	}
	level := parseLogLevel(c.Logger.Level)
	atomicLevel.SetLevel(level)
	// ignoredTags 同样可热更：Reload 后重建过滤集合（map 整体替换，读侧无锁
	// 读取的是替换前/后某一个完整 map，不会撕裂）。
	ignoredTags = buildIgnoredTags(c.Logger.IgnoredTags)
}

// parseLogLevel 解析日志级别字符串，空值 / 非法值回退 DebugLevel。
//
// 注意 zapcore.ParseLevel("") 会返回 InfoLevel（无错误），但配置系统的 LogLevel
// 默认值是 "debug"，且本框架历来以 debug 为兜底级别——因此这里把空串也显式
// 归一到 Debug，避免"显式清空 LogLevel 反而升到 Info"的反直觉行为。
func parseLogLevel(s string) zapcore.Level {
	s = strings.TrimSpace(s)
	if s == "" {
		return zapcore.DebugLevel
	}
	level, err := zapcore.ParseLevel(s)
	if err != nil {
		return zapcore.DebugLevel
	}
	return level
}

// buildIgnoredTags 把逗号分隔的 tag 列表构造为查找 map。空输入返回非 nil 空 map
// （checkTag 对 nil/空 map 都安全，但统一返回非 nil 便于推理）。
func buildIgnoredTags(raw string) map[string]bool {
	m := make(map[string]bool)
	for _, tag := range strings.Split(raw, ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			m[tag] = true
		}
	}
	return m
}

func initLogger() {
	// 从统一配置系统拉取所有可配置的 log 项（CONFIG 契约：Current().Logger）。
	// 启动期一次性项（滚动切割参数）落到 runtime* 变量；可热更项（级别 / ignoredTags）
	// 走 atomicLevel + ignoredTags，并由 applyReloadableConfig 在 Reload 时刷新。
	lc := sanitizeLoggerConfig(tgfconfig.Current().Logger)
	loadLumberjackConfig()

	var (
		timeFormat = runtimeTimeFormat
		/*自定义时间格式*/
		customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format(timeFormat))
		}
		/*自定义日志级别显示*/
		customLevelEncoder = func(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(level.CapitalString())
		}
		/*自定义代码路径、行号输出*/
		customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString("[" + caller.TrimmedPath() + "]")
		}
		logLevel = lc.Level
		logPath  = lc.Path
	)

	zapLoggerEncoderConfig := zapcore.EncoderConfig{
		TimeKey:          "time",
		LevelKey:         "level",
		NameKey:          "logger",
		CallerKey:        "caller",
		MessageKey:       "message",
		StacktraceKey:    "stacktrace",
		EncodeCaller:     customCallerEncoder,
		EncodeTime:       customTimeEncoder,
		EncodeLevel:      customLevelEncoder,
		EncodeDuration:   zapcore.SecondsDurationEncoder,
		LineEnding:       "\n",
		ConsoleSeparator: " ",
	}

	//Dev环境,日志级别使用带颜色的标识
	if tgfconfig.Current().Runtime.Module == tgf.RuntimeModuleDev {
		zapLoggerEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// 级别交给 atomicLevel——所有 core 共享同一个 LevelEnabler，Reload 时
	// SetLevel 即对全部 sink 生效（含 stdout 与三个专用文件）。
	atomicLevel.SetLevel(parseLogLevel(logLevel))
	ignoredTags = buildIgnoredTags(lc.IgnoredTags)

	//在原有日志基础上增加一层
	st := newCore(logPath, zapLoggerEncoderConfig, true)
	zapCoreGame := newCore(logPath, zapLoggerEncoderConfig, false)
	basePath := filepath.Dir(logPath)
	// 子 tag 专用文件路径——可配置，默认 service/service.log 和 db/db.log（CONFIG 契约）
	serviceFile := lc.ServiceFile
	dbFile := lc.DBFile
	zapCoreService := newCore(filepath.Join(basePath, filepath.FromSlash(serviceFile)), zapLoggerEncoderConfig, false)
	zapCoreDB := newCore(filepath.Join(basePath, filepath.FromSlash(dbFile)), zapLoggerEncoderConfig, false)
	// 创建一个映射，将标签映射到对应的Core
	taggedCores := map[string]zapcore.Core{
		DBTAG:      &TaggedCore{Core: zapCoreDB, Tag: DBTAG, Pass: true},
		GAMETAG:    &TaggedCore{Core: zapCoreGame, Tag: ""},
		SERVICETAG: &TaggedCore{Core: zapCoreService, Tag: SERVICETAG, Pass: true},
	}
	logger = zap.New(zapcore.NewTee(taggedCores[DBTAG], taggedCores[GAMETAG], taggedCores[SERVICETAG], st), zap.AddCaller(), zap.AddCallerSkip(1))
	slogger = logger.Sugar()
	defer logger.Sync()

	// 注册 Reload 订阅者（仅一次）：config.Reload() 成功后热更日志级别 / ignoredTags。
	// 这取代了 v1 在 loadLumberjackConfig 里裸读 os.Getenv 的伪热更——现在改 .env
	// 文件 → tgfconfig.Reload() → applyReloadableConfig 即可让级别原子生效。
	if reloadHookOnce.CompareAndSwap(false, true) {
		tgfconfig.OnReload(applyReloadableConfig)
	}

	InfoTag("init", "日志初始化完成日志文件:%s 日志级别:%v", logPath, logLevel)
}

// 为每个日志类型（game, system, all）创建一个专门的Core实例。
//
// 级别统一使用包级 atomicLevel（zap.AtomicLevel 实现 zapcore.LevelEnabler），
// 因此 Reload 时 atomicLevel.SetLevel 对所有 core 同步生效，无需重建 logger。
func newCore(logPath string, zapLoggerEncoderConfig zapcore.EncoderConfig, stdout bool) zapcore.Core {
	//如果logPath文件夹不存在则创建
	if _, err := os.Stat(filepath.Dir(logPath)); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(logPath), os.ModePerm)
	}

	wys := make([]zapcore.WriteSyncer, 0, 2)
	if stdout {
		wys = append(wys, zapcore.AddSync(os.Stdout))
		syncWriter := zapcore.NewMultiWriteSyncer(wys...)
		return zapcore.NewCore(zapcore.NewConsoleEncoder(zapLoggerEncoderConfig), syncWriter, atomicLevel)
	}
	wy := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logPath,           // ⽇志⽂件路径
		MaxBackups: runtimeMaxBackups, // 最多保留文件数（v2 可配置）
		MaxSize:    runtimeMaxSize,    // 单个文件最大 MB（v2 可配置）
		MaxAge:     runtimeMaxAge,     // 文件最多保存多少天（v2 可配置）
		LocalTime:  runtimeLocalTime,  // 归档文件名是否本地时间（v2 可配置）
		Compress:   runtimeCompress,   // 是否 gzip 压缩滚动文件（v2 可配置）
	})
	wys = append(wys, wy)
	syncWriter := zapcore.NewMultiWriteSyncer(wys...)
	return zapcore.NewCore(zapcore.NewJSONEncoder(zapLoggerEncoderConfig), syncWriter, atomicLevel)
}

type TaggedCore struct {
	Core        zapcore.Core
	Tag         string
	AllowedTags map[string]zapcore.Core
	Pass        bool
}

func (t *TaggedCore) Enabled(lvl zapcore.Level) bool {
	return t.Core.Enabled(lvl)
}

// With 派生一个携带 fields 的新 TaggedCore。
//
// E3-log 修复（V3 审计 P3）：旧实现漏拷 Pass 字段，导致 logger.With(fields)
// 派生出的 db/service core 丢失 Pass:true——这两个 core 依赖 Pass 绕过 level
// 门控，保证 DEBUG 级的 log.DB / log.Service 条目在高 level 配置下仍写入专用
// 文件。漏拷后派生 logger 的 db/service 条目会重新受 level 过滤，高 level 下
// 静默丢失。必须把 Pass 一并透传。
func (t *TaggedCore) With(fields []zapcore.Field) zapcore.Core {
	return &TaggedCore{
		Core:        t.Core.With(fields),
		Tag:         t.Tag,
		AllowedTags: t.AllowedTags,
		Pass:        t.Pass,
	}
}

func (t *TaggedCore) Check(entry zapcore.Entry, checkedEntry *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if t.Pass || t.Core.Enabled(entry.Level) {
		return checkedEntry.AddCore(entry, t)
	}
	return nil
}

func (t *TaggedCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// 在这里可以根据标签过滤逻辑处理日志条目
	// 例如，你可以在fields中查找特定的标签字段，并根据这个标签决定是否调用t.Core.Write
	if t.Tag == "" {
		return t.Core.Write(entry, fields)
	}

	for _, field := range fields {
		if field.Key == "tag" && field.String == t.Tag {
			return t.Core.Write(entry, fields)
		}
	}
	return nil
}

func (t *TaggedCore) Sync() error {
	return t.Core.Sync()
}
