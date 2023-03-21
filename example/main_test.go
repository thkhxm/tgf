package main_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/service/api/hall"
	hallpb "github.com/thkhxm/tgf/service/api/hall/pb"
	"github.com/thkhxm/tgf/util"
	"net"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

func TestExampleService(t *testing.T) {
	// [1][1][2][2][n][n]
	// magic number|message type|request method name size|data size|method name|data
	for i := 0; i < 1; i++ {
		//x := i
		util.Go(func() {
			add, err := net.ResolveTCPAddr("tcp", "127.0.0.1:8891")
			client, err := net.DialTCP("tcp", nil, add)
			if err != nil {
				t.Logf("client error: %v", err)
				return
			}
			//token := fmt.Sprintf("token-testAccount-t%v", x)
			token := "token-testAccount-03"
			//Login
			loginBuff := LoginByteTest(token)
			cnt, er := client.Write(loginBuff.Bytes())
			//client.Write(loginBuff.Bytes())
			t.Logf("send login message : %v", loginBuff.Bytes())
			for a := 0; a < 1; a++ {
				buff := LogicByteTest(fmt.Sprintf("send message %v", a))
				cnt, er = client.Write(buff.Bytes())
				if er != nil {
					t.Logf("write len %v error : %v", cnt, er)
				}
				t.Logf("send logic message : %v", buff.Bytes())
			}
			//Logic

			//for {
			//	resBytes := make([]byte, 1024)
			//	client.Read(resBytes)
			//	t.Logf("response message : %v", resBytes)
			//}

			wg := sync.WaitGroup{}
			wg.Add(1)
			wg.Wait()
		})
	}
	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}

func LoginByteTest(token string) *bytes.Buffer {
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

func LogicByteTest(msg string) *bytes.Buffer {
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
