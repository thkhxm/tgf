# Q&A

#### 不同开发人员如何隔离项目的环境？

```go
	// EnvironmentConsulPath consul路径
	//
	// 默认使用/tgf,如需区分不同环境可以使用自定义的不同的路径 例如 /test 或者 /dev /tim
	EnvironmentConsulPath Environment = "ConsulPath"

	// 只需要设置当前的环境变量ConsulPath修改为自己的路径即可,可以在启动目录的.env.*的配置文件中加入一条
	// ConsulPath=/tim
	// 代码在tgf/define.go中
```

Context创建说明

```
如果是调用gate网关，并且没有存储用户的context或者请求的context的话，就需要使用
NewCacheUserContext() 这个函数会获取当前用户最新的节点信息,保证数据准确响应

正常情况下逻辑节点的rpc请求 使用
NewUserRPCContext(userId string) 这个函数会初始化一个常规的context，在底层会使用缓存的节点信息进行快速调用

重新绑定用户节点和推送对应消息，一般用户业务逻辑节点的分配，可以使用
NewBindRPCContext(user ...string) 这个函数会重新绑定用户当前请求的这个业务节点，并且将所有用户分配到该节点中。一般情况下，不需要开发人员手动绑定

```

