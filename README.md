# TFramework

#### 介绍
使用golang，搭建的分布式游戏框架。

#### 版本计划

1. ~~使用go net， 自定义tcp连接~~
2. 接入go-redis，嵌入相关配置
3. 接入mysql数据库
4. 接入时序型数据库（clickHouse之类的）
5. 增加goroutine的管理器
6. plugin管理
7. 自定义rpcx路由逻辑

#### 软件架构
软件架构说明


#### 安装教程

1.  xxxx
2.  xxxx
3.  xxxx

#### 使用说明

##### 配置文件

配置文件存放在 startup项目的config目录下

###### app.yaml

```yaml
server:
  profiles:
    active: local #指向运行环境或者运行节点的相关配置
```

###### app-local.yaml

```yaml
Server:
  Modules:
    - ModuleName: Gate #当前进程启动的模块名称
      ModuleVersion: 1.0.0 #当前模块的版本
      Address: 0.0.0.0 #绑定的地址
      Port: 8091 #绑定的端口
    - ModuleName: Chat #当前进程启动的模块名称
      ModuleVersion: 1.0.0 #当前模块的版本
      Address: 0.0.0.0 #绑定的地址
      Port: 8091 #绑定的端口      
  API:
    - ModuleName: Login #调用API的模块名称
      ModuleVersion: 1.0.0 #调用API的模块版本
  Discovery:
    Consul:
      - Address: 127.0.0.1 #consul地址
        Port: 8500 #consul端口
    ConsulPath: /tframework #consul的basePath
  TCP:
    Address: 0.0.0.0 #地址
    Port: 8880 #端口
    DeadLineTime: 300 #连接失效时间
```





##### 引入的第三方库

```
go get github.com/go-redsync/redsync	用 Redis 实现分布式互斥锁。
go get github.com/go-redis/redis/v9 	Redis官方包
go get -u github.com/panjf2000/gnet/v2  网络层框架
```

##### rpcx-ui

可视化consul服务的相关管理页，可以查看当前已经注册的服务。已经包含在项目中

```
You can run go build -o rpcx-ui *.go to create the executable file: rpcx-ui.
Put rpcx-ui、config.json、web、templates in a directory, for example, /opt/rpcx-ui， and then run ./rpcx-ui to start this http server.
You can visit http://localhost:8972/ to visit this GUI.
```



#### 参与贡献

1.  


#### 参考

1.  https://blog.csdn.net/inthat/article/details/122525921 
1.  https://github.com/smallnest/rpcx-ui  rpcx-ui仓库
