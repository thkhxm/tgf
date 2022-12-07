package tcp

import (
	"net"
	"strconv"
	"sync"
	"testing"
)

//***************************************************
//author tim.huang
//2022/12/6
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func TestNetSocketClient(t *testing.T) {
	add, err := net.ResolveTCPAddr("tcp", "192.168.1.90:8880")
	client, err := net.DialTCP("tcp", nil, add)
	if err != nil {
		t.Logf("client error: %v", err)
		return
	}

	for i := 0; i < 10; i++ {
		var msg = "say hello - " + strconv.Itoa(i)
		cnt, er := client.Write([]byte(msg))
		if er != nil {
			t.Logf("write len %v error : %v", cnt, er)
		}
		t.Logf("send message : %v", msg)
		//time.Sleep(time.Second * 3)
		//buf := make([]byte, 1024)
		//rcnt, er2 := client.Read(buf)
		//if er2 != nil {
		//	t.Logf("write len %v error : %v", rcnt, er)
		//}
		//t.Logf("callback message : %v", string(buf))
	}
	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}
