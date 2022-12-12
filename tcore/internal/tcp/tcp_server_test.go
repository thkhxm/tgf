package tcp

import (
	"bufio"
	"bytes"
	"net"
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

func TestNetSocketServer(t *testing.T) {
	add, _ := net.ResolveTCPAddr("tcp", "192.168.1.90:8880")
	listen, err := net.ListenTCP("tcp", add)
	if err != nil {
		t.Logf("listen is err %v", err)
		return
	}
	for {
		tcp, _ := listen.AcceptTCP()
		tcp.SetNoDelay(true)         //无延迟
		tcp.SetKeepAlive(true)       //保持激活
		tcp.SetReadBuffer(1024)      //设置读缓冲区大小
		tcp.SetWriteBuffer(8 * 1024) //设置写缓冲区大小
		go handleConn(tcp, t)
	}
}

func TestStartTcpServer(t *testing.T) {
	server := NewDefaultTCPServer()
	server.Start()
}

func TestSlice(t *testing.T) {
	d := []byte{1, 2, 3, 4, 5}
	t.Logf("--->%v", d[0:1])
	t.Logf("--->%v", d[1:3])
}

func handleConn(con *net.TCPConn, t *testing.T) {
	r := bufio.NewReader(con)
	//tcp.SetReadDeadline(time.Now().Add(3 * time.Second)) //设置读超时
	for {
		buf := make([]byte, 1024)
		cnt, er := r.Read(buf)
		bytes.TrimRight(buf, "\x00")
		if er != nil {
			//t.Logf("tcp read error: %v", er)
			return
		}
		t.Logf("server accept message ,len %v data %v", cnt, string(buf))
	}

}
