package db

import (
	"context"
	"database/sql"
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

var dbService *mysqlService

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

	if this.isRunning() {
		return nil
	}

	if conn, err := this.db.Conn(context.Background()); err == nil {
		return conn
	}
	return nil
}

func initMySql() {
	var (
		err error
		db  *sql.DB
	)
	dbService = new(mysqlService)

	// 定义 MySQL 数据库连接信息
	dataSourceName := "username:password@tcp(hostname:port)/database_name?charset=utf8"
	// 创建数据库连接池
	db, err = sql.Open("mysql", dataSourceName)
	if err != nil {
		log.WarnTag("init", "mysql dataSourceName is wrong")
		return
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		log.WarnTag("init", "mysql unable to connect to database")
		panic(err.Error())
	}
	dbService.running = true
}
