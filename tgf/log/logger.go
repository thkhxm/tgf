package log

import (
	"fmt"
	"github.com/thkhxm/tgf"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

var logger *zap.Logger

var (
	defaultMaxSize = 100
	defaultMaxAge  = 5
)

func Info(msg string, params ...interface{}) {
	logger.Info(fmt.Sprintf(msg, params...))
}

func Debug(msg string, params ...interface{}) {
	logger.Debug(fmt.Sprintf(msg, params...))
}

func Error(msg string, params ...interface{}) {
	logger.Error(fmt.Sprintf(msg, params...))
}

func Warn(msg string, params ...interface{}) {
	logger.Warn(fmt.Sprintf(msg, params...))
}

func initLogger() {
	var (
		/*自定义时间格式*/
		customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
		}
		/*自定义日志级别显示*/
		customLevelEncoder = func(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(level.CapitalString())
		}
		/*自定义代码路径、行号输出*/
		customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString("[" + caller.TrimmedPath() + "]")
		}
		logLevel = tgf.GetStrConfig[string](tgf.EnvironmentLoggerLevel)
		logPath  = tgf.GetStrConfig[string](tgf.EnvironmentLoggerPath)
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
	if tgf.GetStrConfig[string](tgf.EnvironmentRuntimeModule) == tgf.RuntimeModuleDev {
		zapLoggerEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	//syncWriter = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout))

	syncWriter := zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(&lumberjack.Logger{
		Filename:  logPath,        // ⽇志⽂件路径
		MaxSize:   defaultMaxSize, // 单位为MB,默认为512MB
		MaxAge:    defaultMaxAge,  // 文件最多保存多少天
		LocalTime: true,           // 采用本地时间
		Compress:  false,          // 是否压缩日志
	}))

	//syncWriter = &zapcore.BufferedWriteSyncer{
	//	WS: zapcore.AddSync(&lumberjack.Logger{
	//		Filename:  "logs/app/app.log", // ⽇志⽂件路径
	//		MaxSize:   100,                                                                                                        // 单位为MB,默认为512MB
	//		MaxAge:    5,                                                                                                          // 文件最多保存多少天
	//		LocalTime: true,                                                                                                       // 采用本地时间
	//		Compress:  false,                                                                                                      // 是否压缩日志
	//	}),
	//	Size: 4096,
	//}
	//在原有日志基础上增加一层
	level, _ := zapcore.ParseLevel(logLevel)
	zapCore := zapcore.NewCore(zapcore.NewConsoleEncoder(zapLoggerEncoderConfig), syncWriter, level)
	logger = zap.New(zapCore, zap.AddCaller(), zap.AddCallerSkip(1))
	defer logger.Sync()
	Info("[init] 日志初始化完成 日志文件:%v 日志级别:%v", logPath, logLevel)
}
