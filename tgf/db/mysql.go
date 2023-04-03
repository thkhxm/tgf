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
	pk = "pk"
)

var dbService *mysqlService

type IModel interface {
	GetTableName() string
	GetTableValues() string
}

type Model struct {
	DD        string
	CreatedAt string
}

func (m Model) GetTableName() string {
	//TODO implement me
	panic("implement me")
}

func (m Model) GetTableValues() string {
	//TODO implement me
	panic("implement me")
}

type mysqlService struct {
	running bool
	db      *sql.DB
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

func GetConn() *sql.Conn {
	return dbService.getConnection()
}

func initMySql() {
	var (
		err      error
		db       *sql.DB
		userName = tgf.GetStrConfig[string](tgf.EnvironmentMySqlUser)
		password = tgf.GetStrConfig[string](tgf.EnvironmentMySqlPwd)
		hostName = tgf.GetStrConfig[string](tgf.EnvironmentMySqlAddr)
		port     = tgf.GetStrConfig[string](tgf.EnvironmentMySqlPort)
		database = tgf.GetStrConfig[string](tgf.EnvironmentMySqlDB)
	)
	dbService = new(mysqlService)

	// 定义 MySQL 数据库连接信息
	dataSourceName := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?charset=utf8", userName, password, hostName, port, database)
	// 创建数据库连接池
	db, err = sql.Open("mysql", dataSourceName)
	if err != nil {
		log.WarnTag("init", "mysql dataSourceName is wrong")
		return
	}
	//defer db.Close()
	if err = db.Ping(); err != nil {
		log.WarnTag("init", "mysql unable to connect to database")
		return
	}
	dbService.running = true
	dbService.db = db
	log.InfoTag("init", "mysql is running hostName=%v port=%v database=%v", hostName, port, database)
}
