# 关于 tgf
    tgf框架是使用golang开发的一套游戏分布式框架.

    属于开箱即用的项目框架,目前适用于中小型团队,独立开发者,快速开发使用.

    框架提供了一整套开发工具,并且定义了模块开发规范.

    开发者只需要关注业务逻辑即可,无需关心用户并发和节点状态等复杂情况.


[项目地址](https://github.com/thkhxm/tgf)  
[项目文档](https://thkhxm.github.io/tgf_writerside/starter-topic.html)  
[知乎博客](https://www.zhihu.com/people/tim-30-83/posts)  
[CSDN专栏](https://blog.csdn.net/thkhxm/category_12520142.html)  
[B站教程](https://space.bilibili.com/64497732/channel/seriesdetail?sid=3815364)


## 交流群
    QQ群:7400585

## 技术选型
    Golang开发版本:  1.21.1

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
| excelize   | Excel工具      | https://github.com/qax-os/excelize       |
| sonic      | json高性能工具 | https://github.com/bytedance/sonic/      |

## 基础架构图
![img](jiagoutu.png)

## 规划
    项目后续会更新系列教程文章和视频教程,并且开源项目案例.也会不断的更新和优化项目框架.
    欢迎大家加入qq群一起交流和探讨.