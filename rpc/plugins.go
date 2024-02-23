package rpc

import (
	context2 "context"
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/edwingeng/doublejump"
	client2 "github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"golang.org/x/net/context"
	"strings"
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
	reqMetaDataTimeout          = time.Hour * 24 * 7
	localNodeCacheTimeout int64 = 60 * 5
)

type CustomSelector struct {
	moduleName   string
	h            *doublejump.Hash
	servers      *hashmap.Map[string, string]
	cacheManager db.IAutoCacheService[string, string]
}

func (c *CustomSelector) clearAllUserCache() {
	var ()
	c.cacheManager = c.cacheManager.Reset()
}

func (c *CustomSelector) Select(ctx context.Context, servicePath, serviceMethod string, args interface{}) (selected string) {
	if sc, ok := ctx.(*share.Context); ok {
		size := c.servers.Len()
		switch size {
		case 0:
			return ""
		default:
			reqMetaData := sc.Value(share.ReqMetaDataKey).(map[string]string)
			selected = reqMetaData[servicePath]
			//用户级别的请求
			uid := reqMetaData[tgf.ContextKeyUserId]
			rpcTip := reqMetaData[tgf.ContextKeyRPCType]
			broadcasts := make([]string, 1)

			if uid != "" {
				broadcasts[0] = uid
			}
			var bindNode bool
			if rpcTip == tgf.RPCBroadcastTip {
				ids := reqMetaData[tgf.ContextKeyBroadcastUserIds]
				broadcasts = strings.Split(ids, ",")
				if !c.checkServerAlive(selected) {
					key := client2.HashString(fmt.Sprintf("%v", time.Now().UnixNano()))
					selected, _ = c.h.Get(key).(string)
				}
				bindNode = true
			}
			if len(broadcasts) > 0 && broadcasts[0] != "" {
				for _, uid := range broadcasts {
					var key uint64
					//先判断携带节点信息是否存活
					if c.checkServerAlive(selected) {
						if bindNode {
							c.processNode(ctx, uid, selected, reqMetaData, servicePath)
						}
						continue
					}

					//从本地缓存中获取用户的节点数据
					selected, _ = c.cacheManager.Get(uid)
					if c.checkServerAlive(selected) {
						continue
					}
					//如果上面的用户节点获取，没有命中，那么取当前请求模式
					//如果是rpc推送请求，
					//if rpcTip == tgf.RPCTip {
					//从数据缓存中获取用户的节点数据
					reqMetaDataKey := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uid)
					reqMetaCacheData, suc := db.GetMap[string, string](reqMetaDataKey)
					if !suc {
						reqMetaCacheData = make(map[string]string)
					}
					selected = reqMetaCacheData[servicePath]
					if c.checkServerAlive(selected) {
						//将节点数据，放入本地缓存
						if reqMetaData[tgf.ContextKeyCloseLocalCache] == "" {
							c.cacheManager.Set(uid, selected)
						}
						continue
					} else {
						//通过一致性hash的方式,命中一个活跃的业务节点
						key = client2.HashString(uid)
						selected, _ = c.h.Get(key).(string)
						reqMetaData[servicePath] = selected
						c.processNode(ctx, uid, selected, reqMetaData, servicePath)
					}
				}
			} else {
				if c.checkServerAlive(selected) {
					key := client2.HashString(fmt.Sprintf("%v", time.Now().Unix()))
					selected, _ = c.h.Get(key).(string)
					return
				}
				key := client2.HashString(fmt.Sprintf("%v", time.Now().UnixNano()))
				selected, _ = c.h.Get(key).(string)
			}
			return
		}
	}

	return ""
}
func (c *CustomSelector) processNode(ctx context.Context, uid string, selected string, reqMetaData map[string]string, servicePath string) {
	reqMetaDataKeyTemp := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uid)
	db.PutMap(reqMetaDataKeyTemp, servicePath, selected, reqMetaDataTimeout)
	if reqMetaData[tgf.ContextKeyCloseLocalCache] == "" {
		c.cacheManager.Set(uid, selected)
	}
	if UploadUserNodeInfo.ModuleName != servicePath {
		util.Go(func() {
			if _, err := SendRPCMessage(ctx, UploadUserNodeInfo.New(&UploadUserNodeInfoReq{
				UserId:      uid,
				NodeId:      selected,
				ServicePath: servicePath,
			}, &UploadUserNodeInfoRes{ErrorCode: 0})); err != nil {
				log.Warn("[rpc] 节点更新异常 %v", err)
			}
		})
	}
}

func (c *CustomSelector) UpdateServer(servers map[string]string) {
	// TODO: 新增虚拟节点，优化hash的命中分布
	var serverInfos string
	clearUserCache := false
	for k, v := range servers {
		if log.CheckLogTag("discovery") {
			serverInfos += fmt.Sprintf("%v:%v,", k, v)
		}
		c.h.Add(k)
		if c.servers.Insert(k, v) {
			clearUserCache = true
		} else {
			c.servers.Set(k, v)
		}
	}

	c.servers.Range(func(k string, v string) bool {
		if servers[k] == "" { // remove
			c.h.Remove(k)
			c.servers.Del(k)
			clearUserCache = true
		}
		return true
	})

	if clearUserCache {
		c.clearAllUserCache()
		log.DebugTag("discovery", "moduleName=%v 更新服务节点", c.moduleName)
	}
	log.DebugTag("discovery", "moduleName=%v 节点数据%v", c.moduleName, serverInfos)
}

func (c *CustomSelector) checkServerAlive(server string) (h bool) {
	var ()
	if server == "" {
		return false
	}

	_, h = c.servers.Get(server)
	return
}

func (c *CustomSelector) initStruct(moduleName string) {
	c.servers = hashmap.New[string, string]()
	c.h = doublejump.NewHash()
	c.moduleName = moduleName
	c.cacheManager = db.NewAutoCacheManager[string, string](localNodeCacheTimeout)
}

type RPCXClientHandler struct {
}

func (r *RPCXClientHandler) PreCall(ctx context2.Context, serviceName, methodName string, args interface{}) (interface{}, error) {
	log.DebugTag("trace-rpc", "发送 %v-%v 请求 , 参数 %v", serviceName, methodName, args)
	return args, nil
}

func (r *RPCXClientHandler) PostCall(ctx context2.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) error {
	log.DebugTag("trace-rpc", "执行 %v-%v 完毕 , 返回结果 %v ", servicePath, serviceMethod, reply)
	return err
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
