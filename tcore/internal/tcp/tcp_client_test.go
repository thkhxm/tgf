package tcp

import (
	"bytes"
	"encoding/binary"
	"net"
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

	// [1][1][2][2][n][n]
	// magic number|message type|request method name size|data size|method name|data
	//for i := 0; i < 10; i++ {
	//	go func() {
	add, err := net.ResolveTCPAddr("tcp", "192.168.1.90:8880")
	client, err := net.DialTCP("tcp", nil, add)
	if err != nil {
		t.Logf("client error: %v", err)
		return
	}
	//for i := 0; i < 100; i++ {
	//var msg = "say hello - " + strconv.Itoa(i)
	//Login
	loginBuff := LoginByteTest()
	cnt, er := client.Write(loginBuff.Bytes())
	t.Logf("send login message : %v", loginBuff.Bytes())
	//Logic
	buff := LogicByteTest()
	cnt, er = client.Write(buff.Bytes())
	if er != nil {
		t.Logf("write len %v error : %v", cnt, er)
	}
	t.Logf("send logic message : %v", buff.Bytes())

	//for {
	//	resBytes := make([]byte, 1024)
	//	client.Read(resBytes)
	//	t.Logf("response message : %v", resBytes)
	//}

	//	}
	//}()
	//time.Sleep(time.Second * 3)
	//buf := make([]byte, 1024)
	//rcnt, er2 := client.Read(buf)
	//if er2 != nil {
	//	t.Logf("write len %v error : %v", rcnt, er)
	//}
	//t.Logf("callback message : %v", string(buf))
	//}
	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}

func LoginByteTest() *bytes.Buffer {
	var token = "token-test2022"
	data := []byte(token)
	tmp := make([]byte, 0, 4+len(data))
	buff := bytes.NewBuffer(tmp)
	buff.WriteByte(250)
	buff.WriteByte(byte(Login))
	reqSizeLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqSizeLenByte, uint16(len(data)))
	buff.Write(reqSizeLenByte)
	buff.Write(data)
	return buff
}

func LogicByteTest() *bytes.Buffer {
	var msg = "say hello - "
	data := []byte(msg)
	reqName := []byte("Chat.StartFightService")
	tmp := make([]byte, 0, 6+len(data)+len(reqName))
	buff := bytes.NewBuffer(tmp)
	buff.WriteByte(250)
	buff.WriteByte(byte(Logic))
	reqLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqLenByte, uint16(len(reqName)))
	buff.Write(reqLenByte)
	reqSizeLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqSizeLenByte, uint16(len(data)))
	buff.Write(reqSizeLenByte)
	buff.Write(reqName)
	buff.Write(data)
	return buff
}
