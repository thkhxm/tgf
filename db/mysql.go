package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"time"
)

// ErrMySQLNotAvailable D5: MySQL 未初始化或当前不可用。
// 读写路径拿不到连接时返回该错误（而不是 nil 连接引发 panic），
// 让 flushBatch 的重试与 toLongevity 的补偿队列（FailureQueue）真正接管故障。
var ErrMySQLNotAvailable = errors.New("mysql not available")

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/22
//***************************************************

var (
	pk     = "pk"
	ignore = "ignore"
	list   = "pkList"
)

var dbService *mysqlService

type Model struct {
	State uint8 //数据状态,0为删除,1为正常
}

func NewModel() Model {
	res := Model{
		State: 1,
	}
	return res
}

func (m *Model) Remove() {
	m.State = 0
}

// IsValid
// @Description: 是否有效
// @receiver m
// @return bool
func (m *Model) IsValid() bool {
	return m.State == 1
}

type IModel interface {
	GetTableName() string
	Remove()
}

type mysqlService struct {
	running     bool
	db          *sql.DB
	executeChan chan string
}

// isRunning 对 nil receiver 安全：dbService 未初始化（如 CacheModuleClose 或测试环境）
// 时直接返回 false，而不是 nil 解引用 panic。
func (m *mysqlService) isRunning() bool {
	return m != nil && m.running
}

// getConnection 兼容旧调用方（GetConn）：失败时返回 nil。
// 框架内部读写路径请改用 getMysqlConn，显式处理 error。
func (m *mysqlService) getConnection() *sql.Conn {
	conn, err := getMysqlConn()
	if err != nil {
		return nil
	}
	return conn
}

// getMysqlConn D5: 获取一条 MySQL 连接；MySQL 未初始化或不可用时返回 error
// 而不是 nil 连接，让调用方（queryOne/queryList/execBatchOnce）走 error 路径
// （日志、重试、补偿队列），而不是 nil 解引用击穿成进程级 panic。
func getMysqlConn() (*sql.Conn, error) {
	if !dbService.isRunning() {
		return nil, ErrMySQLNotAvailable
	}
	conn, err := dbService.db.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMySQLNotAvailable, err)
	}
	return conn, nil
}

func (m *mysqlService) AsyncExecuteUpdateOrCreate(sqlScript string) {
	var ()
	dbService.executeChan <- sqlScript
}

func GetConn() *sql.Conn {
	return dbService.getConnection()
}

func initMySql() {
	var (
		err      error
		d        *sql.DB
		userName = tgf.GetStrConfig[string](tgf.EnvironmentMySqlUser)
		password = tgf.GetStrConfig[string](tgf.EnvironmentMySqlPwd)
		hostName = tgf.GetStrConfig[string](tgf.EnvironmentMySqlAddr)
		port     = tgf.GetStrConfig[string](tgf.EnvironmentMySqlPort)
		database = tgf.GetStrConfig[string](tgf.EnvironmentMySqlDB)
	)

	dbService = new(mysqlService)
	dbService.executeChan = make(chan string)
	// 定义 MySQL 数据库连接信息
	dataSourceName := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?charset=utf8mb4&parseTime=True&loc=Local", userName, password, hostName, port, database)
	// 创建数据库连接池
	d, err = sql.Open("mysql", dataSourceName)
	// D5: err 检查必须紧跟 sql.Open——DSN 畸形时 d 为 nil，
	// 旧实现先对 nil 调 SetMaxIdleConns 导致启动期 panic。日志同时带上 err 本体。
	if err != nil {
		log.WarnTag("init", "mysql dataSourceName is wrong err=%v", err)
		return
	}
	// v2: 连接池参数从硬编码改为环境变量驱动，零配置时仍用 10/200/300s
	maxIdle := tgf.GetStrConfig[int](tgf.EnvironmentMySqlMaxIdleConns)
	if maxIdle <= 0 {
		maxIdle = 10
	}
	maxOpen := tgf.GetStrConfig[int](tgf.EnvironmentMySqlMaxOpenConns)
	if maxOpen <= 0 {
		maxOpen = 200
	}
	maxLifetime := tgf.GetStrConfig[int](tgf.EnvironmentMySqlConnMaxLifetimeSec)
	if maxLifetime <= 0 {
		maxLifetime = 300
	}
	d.SetMaxIdleConns(maxIdle)
	d.SetMaxOpenConns(maxOpen)
	d.SetConnMaxLifetime(time.Duration(maxLifetime) * time.Second)
	//defer db.Close()
	if err = d.Ping(); err != nil {
		log.WarnTag("init", "mysql unable to connect to database err=%v", err)
		return
	}
	dbService.running = true
	dbService.db = d
	log.InfoTag("init", "mysql is running hostName=%v port=%v database=%v", hostName, port, database)
}
