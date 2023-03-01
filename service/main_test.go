package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/service/api/hall"
	hallpb "github.com/thkhxm/tgf/service/api/hall/pb"
	"net"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

func TestExampleService(t *testing.T) {
	// [1][1][2][2][n][n]
	// magic number|message type|request method name size|data size|method name|data
	//for i := 0; i < 10; i++ {
	//	go func() {
	add, err := net.ResolveTCPAddr("tcp", "127.0.0.1:8890")
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
	for {
		resBytes := make([]byte, 1024)
		client.Read(resBytes)
		t.Logf("response message : %v", resBytes)
	}

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
	buff.WriteByte(byte(rpc.Login))
	reqSizeLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqSizeLenByte, uint16(len(data)))
	buff.Write(reqSizeLenByte)
	buff.Write(data)
	return buff
}

func LogicByteTest() *bytes.Buffer {
	var msg = "hello world!   pb"
	data, _ := proto.Marshal(&hallpb.HallSayRequest{Msg: msg})
	reqName := []byte(fmt.Sprintf("%v.%v", hallapi.HallService.Name, "SayHello"))
	tmp := make([]byte, 0, 6+len(data)+len(reqName))
	buff := bytes.NewBuffer(tmp)
	buff.WriteByte(250)
	buff.WriteByte(byte(rpc.Logic))
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
