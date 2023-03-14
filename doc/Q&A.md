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

