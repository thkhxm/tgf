package rpc

import (
	context2 "context"
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/edwingeng/doublejump"
	client2 "github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"golang.org/x/net/context"
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
	moduleName   string
	h            *doublejump.Hash
	servers      *hashmap.Map[string, string]
	cacheManager db.IAutoCacheService[string, string]
}

func (this *CustomSelector) clearAllUserCache() {
	var ()
	this.cacheManager = this.cacheManager.Reset()
}

func (this *CustomSelector) Select(ctx context.Context, servicePath, serviceMethod string, args interface{}) (selected string) {
	if sc, ok := ctx.(*share.Context); ok {
		size := this.servers.Len()
		switch size {
		case 0:
			return ""
		default:
			reqMetaData := sc.Value(share.ReqMetaDataKey).(map[string]string)
			//用户级别的请求
			uid := reqMetaData[tgf.ContextKeyUserId]
			if uid != "" {
				//判断之前的节点是否存活,如果存活,直接命中
				//先判断是否有携带节点信息
				selected = reqMetaData[servicePath]
				if this.checkServerAlive(selected) {
					return
				}

				//从本地缓存中获取用户的节点数据
				selected, _ = this.cacheManager.Get(uid)
				if this.checkServerAlive(selected) {
					return
				}

				switch reqMetaData[tgf.ContextKeyRPCType] {
				case tgf.RPCTip:
					//从数据缓存中获取用户的节点数据
					reqMetaDataKey := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uid)
					reqMetaCacheData, suc := db.GetMap[string, string](reqMetaDataKey)
					if !suc {
						reqMetaCacheData = make(map[string]string)
					}
					selected = reqMetaCacheData[servicePath]
					if this.checkServerAlive(selected) {
						//将节点数据，放入本地缓存
						this.cacheManager.Set(uid, selected)
						return
					}
					fallthrough
				default:
					//通过一致性hash的方式,命中一个活跃的业务节点
					key := client2.HashString(uid)
					selected, _ = this.h.Get(key).(string)
					reqMetaData[servicePath] = selected
					//将节点信息放入数据缓存中
					reqMetaDataKey := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uid)
					db.PutMap(reqMetaDataKey, servicePath, selected, reqMetaDataTimeout)
					//将节点数据，放入本地缓存
					this.cacheManager.Set(uid, selected)
					if UploadUserNodeInfo.ModuleName != servicePath {
						//推送协议通知用户网关
						if _, err := SendRPCMessage(ctx, UploadUserNodeInfo.New(&UploadUserNodeInfoReq{
							UserId:      uid,
							NodeId:      selected,
							ServicePath: servicePath,
						}, &UploadUserNodeInfoRes{ErrorCode: 0})); err != nil {
							log.Warn("[rpc] 节点更新异常 %v", err)
						}
					}
				}
				return
			}
		}
	}

	return ""
}
func (this *CustomSelector) UpdateServer(servers map[string]string) {
	// TODO: 新增虚拟节点，优化hash的命中分布
	clearUserCache := false
	for k, v := range servers {
		this.h.Add(k)
		if this.servers.Insert(k, v) {
			clearUserCache = true
		} else {
			this.servers.Set(k, v)
		}
	}

	this.servers.Range(func(k string, v string) bool {
		if servers[k] == "" { // remove
			this.h.Remove(k)
			clearUserCache = true
		}
		return true
	})

	if clearUserCache {
		this.clearAllUserCache()
		log.DebugTag("discovery", "moduleName=%v 更新服务节点 services=%v", this.moduleName, this.servers)
	}

}

func (this *CustomSelector) checkServerAlive(server string) (h bool) {
	var ()
	if server == "" {
		return false
	}

	_, h = this.servers.Get(server)
	return
}

func (this *CustomSelector) initStruct(moduleName string) {
	this.servers = hashmap.New[string, string]()
	this.h = doublejump.NewHash()
	this.moduleName = moduleName
	this.cacheManager = db.NewAutoCacheManager[string, string]()
}

type RPCXClientHandler struct {
}

func (this *RPCXClientHandler) PostCall(ctx context2.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) error {
	return nil
}

type ILoginCheck interface {
	CheckLogin(token string) (bool, string)
}

func NewCustomSelector(moduleName string) client2.Selector {
	res := &CustomSelector{}
	res.initStruct(moduleName)
	return res
}

func NewRPCXClientHandler() client2.PostCallPlugin {
	res := &RPCXClientHandler{}
	return res
}
