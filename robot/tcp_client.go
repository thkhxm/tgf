package robot

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/golang/protobuf/proto"
	util2 "github.com/smallnest/rpcx/util"
	hallapi "github.com/thkhxm/tgf/example/api/hall"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/util"
	"net"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/26
//***************************************************

type tcp struct {
	buf    *bufio.Reader
	client *net.TCPConn
}

func (t *tcp) Connect(address string) IRobot {
	add, err := net.ResolveTCPAddr("tcp", address)
	t.client, err = net.DialTCP("tcp", nil, add)
	if err != nil {
		log.InfoTag("robot", "client error: %v", err)
		panic(err)
	}
	t.buf = bufio.NewReader(t.client)
	//心跳
	util.Go(func() {
		for true {
			heartbeat := make([]byte, 0, 2)
			buff := bytes.NewBuffer(heartbeat)
			buff.WriteByte(250)
			buff.WriteByte(byte(rpc.Heartbeat))
			t.client.Write(buff.Bytes())
			log.InfoTag("robot", "client heartbeat data: %v", buff.Bytes())
			time.Sleep(time.Second * 10)
		}
	})

	//handler response
	util.Go(func() {
		for {
			// [1][1][2][4][n][n]
			// message type|compress|request method name size|data size|method name|data
			head, e := t.buf.Peek(1)
			if e != nil {
				log.InfoTag("robot", "client response data: %v", e)
				return
			}
			mt := head[0]
			//心跳响应，跳过这个包
			if mt == byte(rpc.Heartbeat) {
				t.buf.Discard(1)
				log.InfoTag("robot", "收到服务器响应的心跳包")
				continue
			}
			//非心跳包，先捕获头
			head, e = t.buf.Peek(8)
			if e != nil {
				log.InfoTag("robot", "client response data: %v", e)
				panic(e)
			}
			compress := head[1]
			requestSize := binary.BigEndian.Uint16(head[2:4])
			dataSize := binary.BigEndian.Uint32(head[4:8])
			allSize := 8 + uint32(requestSize) + dataSize
			//数据没接收完整
			if t.buf.Buffered() < int(allSize) {
				continue
			}
			data := make([]byte, allSize)
			n, e := t.buf.Read(data)
			if e != nil || n != int(allSize) {
				log.InfoTag("robot", "client read data : %v", e)
			}
			if compress == 1 {
				data, e = util2.Unzip(data)
				if e != nil {
					log.InfoTag("robot", "client data compress : %v", e)
				}
			}
			message := util.ConvertStringByByteSlice(data[8 : 8+requestSize])
			res := util.ConvertStringByByteSlice(data[8+requestSize:])
			log.InfoTag("robot", "收到服务器的响应数据 messageType:%v 数据:%v", message, res)
		}
	})
	//
	return t
}

func (t *tcp) RegisterCallbackMessage(messageType string, f CallbackLogic) IRobot {
	//TODO implement me
	panic("implement me")
}

func (t *tcp) Send(messageType string, v1 proto.Message) {
	data, _ := proto.Marshal(v1)
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
	t.client.Write(buff.Bytes())
	log.InfoTag("robot", "发送请求 messageType:%v 数据:%v", messageType, buff.Bytes())
}

func NewRobotTcp() IRobot {
	t := &tcp{}
	return t
}
