package util

import (
	"github.com/bwmarrin/snowflake"
	"math/rand"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/25
//***************************************************

var Snowflake *snowflake.Node

func GenerateSnowflakeId() string {
	return Snowflake.Generate().Base64()
}

func initSnowFlake() {
	//初始化雪花算法Id
	source := rand.NewSource(time.Now().UnixNano())
	ran := rand.New(source)
	Snowflake, _ = snowflake.NewNode(ran.Int63n(1024))
}

var codes = []string{"0", "1", "2", "3", "4", "5",
	"6", "7", "8", "9", "A", "B", "C", "D", "E", "F", "G", "H", "I",
	"J", "K", "L", "M", "N", "O", "P", "Q", "R", "S", "T", "U", "V",
	"W", "X", "Y", "Z"}

//
//func GenerateKey(count int) []string {
//	var ()
//	mod := len(codes)
//	res := make([]string, count)
//	index := 0
//	for {
//		if index >= count {
//			break
//		}
//		fid := GenerateSnowflakeId()
//
//		// Check if fid length is less than 32, if so, append "0" to make it 32 characters long
//		if len(fid) < 32 {
//			c := 32 - len(fid)
//			for i := 0; i < c; i++ {
//				fid += codes[rand.Int31n(int32(len(codes)))]
//			}
//		}
//
//		//fid = strings.ReplaceAll(fid, "-", "")
//		s := strings.Builder{}
//		for i := 0; i < 8; i++ {
//			subStr := fid[i*4 : i*4+4]
//			x, _ := strconv.ParseInt(subStr, 16, 32)
//			subIndex := x % int64(mod)
//			item := codes[subIndex]
//			s.WriteString(item)
//		}
//		//fmt.Println("code->", s.String())
//		res[index] = s.String()
//		index++
//	}
//	return res
//}
