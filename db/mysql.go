package db

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"time"
)

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

func (m *mysqlService) isRunning() bool {
	var ()
	return m.running
}
func (m *mysqlService) getConnection() *sql.Conn {
	var ()
	if !m.isRunning() {
		return nil
	}
	if conn, err := m.db.Conn(context.Background()); err == nil {
		return conn
	}
	return nil
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
	if err != nil {
		log.WarnTag("init", "mysql dataSourceName is wrong")
		return
	}
	//defer db.Close()
	if err = d.Ping(); err != nil {
		log.WarnTag("init", "mysql unable to connect to database")
		return
	}
	dbService.running = true
	dbService.db = d
	log.InfoTag("init", "mysql is running hostName=%v port=%v database=%v", hostName, port, database)
}
