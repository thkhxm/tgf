# tgf

## 项目介绍

​	tgf框架是使用golang开发的一套游戏分布式框架,支持全球一服.项目采用了rpcx做为底层rpc的通讯,consul提供服务注册发现.定义了一整套的模块开发规范.

## 技术选型

**go1.19**

| 技术       | 说明           | 仓库地址                                 |
| ---------- | -------------- | ---------------------------------------- |
| rpcx       | 底层rpc的实现  | https://github.com/smallnest/rpcx        |
| redis      | 提供数据缓存   | https://redis.io/                        |
| hashmap    | 线程安全的集合 | https://github.com/cornelk/hashmap       |
| ants       | 高性能go协程池 | https://github.com/panjf2000/ants        |
| redislock  | 分布式redis锁  | https://github.com/bsm/redislock         |
| snowflake  | 雪花算法       | https://github.com/bwmarrin/snowflake    |
| doublejump | 一致性hash     | https://github.com/edwingeng/doublejump  |
| godotenv   | 环境变量工具   | https://github.com/joho/godotenv         |
| zap        | 日志框架       | https://go.uber.org/zap                  |
| lumberjack | 日志切割工具   | https://gopkg.in/natefinch/lumberjack.v2 |

## 基础架构图

![image-20230228031100624](http://oss.yamigame.net/picgo/image-20230228031100624.png)

#### 参与贡献

1.  Fork 本仓库
2.  新建 Feat_xxx 分支
3.  提交代码
4.  新建 Pull Request


#### 特技

1.  使用 Readme\_XXX.md 来支持不同的语言，例如 Readme\_en.md, Readme\_zh.md
2.  Gitee 官方博客 [blog.gitee.com](https://blog.gitee.com)
3.  你可以 [https://gitee.com/explore](https://gitee.com/explore) 这个地址来了解 Gitee 上的优秀开源项目
4.  [GVP](https://gitee.com/gvp) 全称是 Gitee 最有价值开源项目，是综合评定出的优秀开源项目
5.  Gitee 官方提供的使用手册 [https://gitee.com/help](https://gitee.com/help)
6.  Gitee 封面人物是一档用来展示 Gitee 会员风采的栏目 [https://gitee.com/gitee-stars/](https://gitee.com/gitee-stars/)
