package define

import "flag"

//***************************************************
//author tim.huang
//2022/8/18
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

// ***********************    var    ****************************

// CallDepth 日志深度，一般不修改，默认值即可
var CallDepth = flag.Int("log_depth", 3, "log depth")

// Modules 启动模块
var Modules = flag.String("modules", "Chat,Test", "startup modules")

// Address 服务器绑定地址，默认不修改
var Address = flag.String("address", "0.0.0.0", "server address")

// Port 端口如果是0，启动时会随机一个端口
var Port = flag.Int("port", 8081, "server port")

// ConfigPath 配置文件所在路径
var ConfigPath = flag.String("config_path", "./config/", "config path")

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//// ServerConfig
//// @Description: 服务器相关配置
//type Server struct {
//	Modules
//}

//***********************    struct_end    ****************************

func init() {

}
