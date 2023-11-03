package db

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
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
)

var dbService *mysqlService

type Model struct {
	State uint8
}

func NewModel() Model {
	res := Model{
		State: 0,
	}
	return res
}

func (m *Model) Remove() {
	m.State = 1
}

type IModel interface {
	GetTableName() string
}

type mysqlService struct {
	running     bool
	db          *sql.DB
	executeChan chan string
}

func (this *mysqlService) isRunning() bool {
	var ()
	return this.running
}
func (this *mysqlService) getConnection() *sql.Conn {
	var ()
	if !this.isRunning() {
		return nil
	}
	if conn, err := this.db.Conn(context.Background()); err == nil {
		return conn
	}
	return nil
}

func (this *mysqlService) AsyncExecuteUpdateOrCreate(sqlScript string) {
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
	dataSourceName := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?charset=utf8", userName, password, hostName, port, database)
	// 创建数据库连接池
	d, err = sql.Open("mysql", dataSourceName)
	d.SetMaxIdleConns(10)
	d.SetMaxOpenConns(50)
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
