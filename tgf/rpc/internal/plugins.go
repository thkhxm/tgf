package internal

import (
	context2 "context"
	"fmt"
	"github.com/edwingeng/doublejump"
	client2 "github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"golang.org/x/net/context"
	"sort"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

var (
	reqMetaDataTimeout = time.Hour * 24 * 3
)

type CustomSelector struct {
	h       *doublejump.Hash
	servers []string
}

func (this *CustomSelector) Select(ctx context.Context, servicePath, serviceMethod string, args interface{}) string {
	if sc, ok := ctx.(*share.Context); ok {
		size := len(this.servers)
		switch size {
		case 0:
			return ""
		default:
			reqMetaData := sc.Value(share.ReqMetaDataKey).(map[string]string)
			//用户级别的请求
			uid := reqMetaData[tgf.ContextKeyUserId]
			if uid != "" {
				//判断之前的节点是否存活,如果存活,直接命中
				selected := reqMetaData[servicePath]
				if selected != "" && this.checkServerAlive(selected) {
					return selected
				}
				//通过一致性hash的方式,命中一个活跃的业务节点
				key := client2.HashString(uid)
				selected, _ = this.h.Get(key).(string)
				reqMetaData[servicePath] = selected
				reqMetaDataKey := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uid)
				db.PutMap(reqMetaDataKey, servicePath, selected, reqMetaDataTimeout)
				return selected
			}
		}
	}

	return ""
}
func (this *CustomSelector) UpdateServer(servers map[string]string) {
	// TODO: 新增虚拟节点，优化hash的命中分布
	ss := make([]string, 0, len(servers))
	for k := range servers {
		this.h.Add(k)
		ss = append(ss, k)
	}

	sort.Slice(ss, func(i, j int) bool { return ss[i] < ss[j] })

	for _, k := range this.servers {
		if servers[k] == "" { // remove
			this.h.Remove(k)
		}
	}
	this.servers = ss
	log.Info("更新服务节点%v", this.servers)
}

func (this *CustomSelector) checkServerAlive(server string) bool {
	var ()
	for _, s := range this.servers {
		if s == server {
			return true
		}
	}
	return false
}

func (this *CustomSelector) initStruct() {
	this.servers = make([]string, 0, 0)
	this.h = doublejump.NewHash()
}

type RPCXClientHandler struct {
}

func (this *RPCXClientHandler) PostCall(ctx context2.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) error {
	return nil
}

type ILoginCheck interface {
	CheckLogin(token string) (bool, string)
}

func NewCustomSelector() client2.Selector {
	res := &CustomSelector{}
	res.initStruct()
	return res
}

func NewRPCXClientHandler() client2.PostCallPlugin {
	res := &RPCXClientHandler{}
	return res
}
