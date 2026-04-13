package config

// v2 新增 perf 配置项（RPC / DB / MySQL pool）的测试。
// 验证 struct tag 默认值和 env 覆盖都能正常工作。

import (
	"testing"
)

func TestConfig_V2NewSubConfigs_Defaults(t *testing.T) {
	resetForTest()
	defer resetForTest()
	// 清掉所有相关 env
	keys := []string{
		"MySqlMaxIdleConns", "MySqlMaxOpenConns", "MySqlConnMaxLifetimeSec",
		"RPCDefaultTimeoutMs", "TCPDeadLineSec", "TCPWriteTimeoutMs", "TCPSendChanTimeoutMs",
		"DBCacheTimeoutSec", "DBMemTimeoutSec",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}

	if cfg.MySQL.MaxIdleConns != 10 {
		t.Errorf("默认 MaxIdleConns 错: %d", cfg.MySQL.MaxIdleConns)
	}
	if cfg.MySQL.MaxOpenConns != 200 {
		t.Errorf("默认 MaxOpenConns 错: %d", cfg.MySQL.MaxOpenConns)
	}
	if cfg.MySQL.ConnMaxLifetimeSec != 300 {
		t.Errorf("默认 ConnMaxLifetimeSec 错: %d", cfg.MySQL.ConnMaxLifetimeSec)
	}
	if cfg.RPC.DefaultTimeoutMs != 5000 {
		t.Errorf("默认 RPCDefaultTimeoutMs 错: %d", cfg.RPC.DefaultTimeoutMs)
	}
	if cfg.RPC.TCPDeadLineSec != 60 {
		t.Errorf("默认 TCPDeadLineSec 错: %d", cfg.RPC.TCPDeadLineSec)
	}
	if cfg.RPC.TCPWriteTimeoutMs != 5000 {
		t.Errorf("默认 TCPWriteTimeoutMs 错: %d", cfg.RPC.TCPWriteTimeoutMs)
	}
	if cfg.RPC.TCPSendChanTimeoutMs != 3000 {
		t.Errorf("默认 TCPSendChanTimeoutMs 错: %d", cfg.RPC.TCPSendChanTimeoutMs)
	}
	if cfg.DB.CacheTimeoutSec != 259200 {
		t.Errorf("默认 DBCacheTimeoutSec 错: %d", cfg.DB.CacheTimeoutSec)
	}
	if cfg.DB.MemTimeoutSec != 10800 {
		t.Errorf("默认 DBMemTimeoutSec 错: %d", cfg.DB.MemTimeoutSec)
	}
}

func TestConfig_V2NewSubConfigs_EnvOverrides(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("MySqlMaxIdleConns", "20")
	t.Setenv("MySqlMaxOpenConns", "500")
	t.Setenv("MySqlConnMaxLifetimeSec", "600")
	t.Setenv("RPCDefaultTimeoutMs", "2000")
	t.Setenv("TCPDeadLineSec", "120")
	t.Setenv("TCPWriteTimeoutMs", "10000")
	t.Setenv("TCPSendChanTimeoutMs", "5000")
	t.Setenv("DBCacheTimeoutSec", "3600")
	t.Setenv("DBMemTimeoutSec", "600")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}

	if cfg.MySQL.MaxIdleConns != 20 {
		t.Errorf("MaxIdleConns 覆盖错: %d", cfg.MySQL.MaxIdleConns)
	}
	if cfg.MySQL.MaxOpenConns != 500 {
		t.Errorf("MaxOpenConns 覆盖错: %d", cfg.MySQL.MaxOpenConns)
	}
	if cfg.MySQL.ConnMaxLifetimeSec != 600 {
		t.Errorf("ConnMaxLifetimeSec 覆盖错: %d", cfg.MySQL.ConnMaxLifetimeSec)
	}
	if cfg.RPC.DefaultTimeoutMs != 2000 {
		t.Errorf("RPCDefaultTimeoutMs 覆盖错: %d", cfg.RPC.DefaultTimeoutMs)
	}
	if cfg.RPC.TCPDeadLineSec != 120 {
		t.Errorf("TCPDeadLineSec 覆盖错: %d", cfg.RPC.TCPDeadLineSec)
	}
	if cfg.RPC.TCPWriteTimeoutMs != 10000 {
		t.Errorf("TCPWriteTimeoutMs 覆盖错: %d", cfg.RPC.TCPWriteTimeoutMs)
	}
	if cfg.RPC.TCPSendChanTimeoutMs != 5000 {
		t.Errorf("TCPSendChanTimeoutMs 覆盖错: %d", cfg.RPC.TCPSendChanTimeoutMs)
	}
	if cfg.DB.CacheTimeoutSec != 3600 {
		t.Errorf("DBCacheTimeoutSec 覆盖错: %d", cfg.DB.CacheTimeoutSec)
	}
	if cfg.DB.MemTimeoutSec != 600 {
		t.Errorf("DBMemTimeoutSec 覆盖错: %d", cfg.DB.MemTimeoutSec)
	}
}

func TestConfig_V2LoggerLumberjack_Defaults(t *testing.T) {
	resetForTest()
	defer resetForTest()
	keys := []string{"LogMaxSize", "LogMaxAge", "LogMaxBackups", "LogCompress", "LogLocalTime", "LogTimeFormat", "LogServiceFile", "LogDBFile"}
	for _, k := range keys {
		t.Setenv(k, "")
	}

	cfg, _ := Load()

	if cfg.Logger.MaxSize != 512 {
		t.Errorf("默认 MaxSize 错: %d", cfg.Logger.MaxSize)
	}
	if cfg.Logger.MaxAge != 0 {
		t.Errorf("默认 MaxAge 错: %d", cfg.Logger.MaxAge)
	}
	if cfg.Logger.MaxBackups != 100 {
		t.Errorf("默认 MaxBackups 错: %d", cfg.Logger.MaxBackups)
	}
	if cfg.Logger.Compress {
		t.Errorf("默认 Compress 应为 false")
	}
	if !cfg.Logger.LocalTime {
		t.Errorf("默认 LocalTime 应为 true")
	}
	if cfg.Logger.TimeFormat != "2006-01-02 15:04:05.000" {
		t.Errorf("默认 TimeFormat 错: %q", cfg.Logger.TimeFormat)
	}
	if cfg.Logger.ServiceFile != "service/service.log" {
		t.Errorf("默认 ServiceFile 错: %q", cfg.Logger.ServiceFile)
	}
	if cfg.Logger.DBFile != "db/db.log" {
		t.Errorf("默认 DBFile 错: %q", cfg.Logger.DBFile)
	}
}

func TestConfig_V2LoggerLumberjack_EnvOverrides(t *testing.T) {
	resetForTest()
	defer resetForTest()

	t.Setenv("LogMaxSize", "256")
	t.Setenv("LogMaxAge", "7")
	t.Setenv("LogMaxBackups", "50")
	t.Setenv("LogCompress", "true")
	t.Setenv("LogLocalTime", "false")
	t.Setenv("LogTimeFormat", "2006/01/02")
	t.Setenv("LogServiceFile", "logs/svc.log")
	t.Setenv("LogDBFile", "logs/db.log")

	cfg, _ := Load()

	if cfg.Logger.MaxSize != 256 {
		t.Errorf("MaxSize 覆盖错: %d", cfg.Logger.MaxSize)
	}
	if cfg.Logger.MaxAge != 7 {
		t.Errorf("MaxAge 覆盖错: %d", cfg.Logger.MaxAge)
	}
	if cfg.Logger.MaxBackups != 50 {
		t.Errorf("MaxBackups 覆盖错: %d", cfg.Logger.MaxBackups)
	}
	if !cfg.Logger.Compress {
		t.Errorf("Compress 应为 true")
	}
	if cfg.Logger.LocalTime {
		t.Errorf("LocalTime 应为 false")
	}
	if cfg.Logger.TimeFormat != "2006/01/02" {
		t.Errorf("TimeFormat 覆盖错: %q", cfg.Logger.TimeFormat)
	}
	if cfg.Logger.ServiceFile != "logs/svc.log" {
		t.Errorf("ServiceFile 覆盖错: %q", cfg.Logger.ServiceFile)
	}
}
