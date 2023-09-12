package util

import (
	"fmt"
	"net"
	"strings"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

// GetLocalHost
// @Description: 获取本机ip
// @return ip
func GetLocalHost() (ip string) {
	//return GetLocalHost2()
	// 使用udp发起网络连接, 这样不需要关注连接是否可通, 随便填一个即可
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		fmt.Println(err)
		return
	}
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	// fmt.Println(localAddr.String())
	ip = strings.Split(localAddr.String(), ":")[0]
	return
}

func GetLocalHost2() (ip string) {
	addrList, err := net.InterfaceAddrs()
	if err != nil {
		panic(err)
	}
	for _, address := range addrList {
		if ipNet, ok := address.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				fmt.Println(ipNet.IP.String())
				return ipNet.IP.String()
			}
		}
	}
	return
}
