package rpc

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/cornelk/hashmap"
	"github.com/edwingeng/doublejump"
	client2 "github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/rpcx/protocol"
	"github.com/thkhxm/rpcx/server"
	"github.com/thkhxm/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/exp/admin"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"net/url"
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

type ConsulServerInfo struct {
	State      string
	Version    int32
	SubVersion int32
	NodeId     string
}

func newConsulServerInfo(data string) *ConsulServerInfo {
	if q, a := url.ParseQuery(data); a == nil {
		v, _ := util.StrToAny[int32](strings.Split(q.Get("version"), ".")[0])
		subv, _ := util.StrToAny[int32](strings.Split(q.Get("version"), ".")[1])
		return &ConsulServerInfo{
			State:      q.Get("state"),
			Version:    v,
			SubVersion: subv,
			NodeId:     q.Get("nodeId"),
		}
	}
	return nil
}

type CustomSelector struct {
	moduleName   string
	h            *doublejump.Hash
	servers      *hashmap.Map[string, *ConsulServerInfo]
	cacheManager db.IAutoCacheService[string, string]
	pushGate     bool
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
			sc.Lock()
			defer sc.Unlock()
			selected = reqMetaData[servicePath]
			//用户级别的请求
			userId := reqMetaData[tgf.ContextKeyUserId]
			rpcTip := reqMetaData[tgf.ContextKeyRPCType]
			broadcasts := make([]string, 1)

			if userId != "" {
				broadcasts[0] = userId
			} else {
				// 判断是否首次登录，查看templeUserId是否存在
				templeUserId := reqMetaData[tgf.ContextKeyTemplateUserId]
				if templeUserId != "" {
					broadcasts[0] = templeUserId
				}
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
		c.cacheManager.Set(selected, uid)
	}
	if c.pushGate && UploadUserNodeInfo.ModuleName != servicePath {
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
		if v == "" {
			continue
		}
		if log.CheckLogTag("discovery") {
			serverInfos += fmt.Sprintf("%v:%v,", k, v)
		}
		c.h.Add(k)
		if c.servers.Insert(k, newConsulServerInfo(v)) {
			clearUserCache = true
		} else {
			c.servers.Set(k, newConsulServerInfo(v))
		}
	}

	c.servers.Range(func(k string, v *ConsulServerInfo) bool {
		if servers[k] == "" { // remove
			c.h.Remove(k)
			c.servers.Del(k)
			clearUserCache = true
			log.DebugTag("discovery", "remove server %v", v)
		}
		if v.State == string(client2.ConsulServerStatePause) {
			c.h.Remove(k)
			log.DebugTag("discovery", "server %v state is %v", v, v.State)
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
	c.servers = hashmap.New[string, *ConsulServerInfo]()
	c.h = doublejump.NewHash()
	c.moduleName = moduleName
	c.pushGate = tgf.GetStrConfig[int32](tgf.EnvironmentGatePush) == 1
	c.cacheManager = db.NewAutoCacheManager[string, string](localNodeCacheTimeout)
}

type XClientHandler struct {
}

func (r *XClientHandler) PreCall(ctx context.Context, serviceName, methodName string, args interface{}) error {
	var traceId string
	if sc, ok := ctx.(*share.Context); ok {
		traceId = sc.GetReqMetaDataByKey(tgf.ContextKeyTRACEID)
		sc.SetValue(tgf.ContextKeyNodeId, tgf.NodeId)
	}
	argStr, _ := sonic.MarshalString(args)
	log.DebugTag("trace", "[%s] client [%s] 发送 [%v-%v] 请求 , 参数 [%v]", traceId, tgf.NodeId, serviceName, methodName, argStr)
	return nil
}

func (r *XClientHandler) PostCall(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) error {
	var traceId string
	if sc, ok := ctx.(*share.Context); ok {
		traceId = sc.GetReqMetaDataByKey(tgf.ContextKeyTRACEID)
	}
	replyStr, _ := sonic.MarshalString(reply)
	log.DebugTag("trace", "[%s] client [%s] 接收 [%v-%v] 响应 , 返回结果 [%v] ", traceId, tgf.NodeId, servicePath, serviceMethod, replyStr)
	return err
}

type XServerHandler struct {
}

func (r *XServerHandler) PreCall(ctx context.Context, serviceName, methodName string, args interface{}) (result interface{}, e error) {
	var traceId string
	if sc, ok := ctx.(*share.Context); ok {
		traceId = sc.GetReqMetaDataByKey(tgf.ContextKeyTRACEID)
		sc.SetValue("timestamp", time.Now().UnixMilli())
	}
	argStr, _ := sonic.MarshalString(args)
	log.DebugTag("trace", "[%s] server [%s] 接收 [%v-%v] 请求 , 参数 [%v]", traceId, tgf.NodeId, serviceName, methodName, argStr)
	return args, nil
}

func (r *XServerHandler) PostCall(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) (result interface{}, e error) {
	var d int64
	var traceId string
	if sc, ok := ctx.(*share.Context); ok {
		traceId = sc.GetReqMetaDataByKey(tgf.ContextKeyTRACEID)
		t := sc.Value("timestamp")
		if t != nil {
			d = time.Now().UnixMilli() - t.(int64)
		}
	}
	replyStr, _ := sonic.MarshalString(reply)
	log.DebugTag("trace", "[%s] server [%s] 执行 [%v-%v] 完毕 耗时[%d], 返回结果 [%v] ", traceId, tgf.NodeId, servicePath, serviceMethod, d, replyStr)
	return reply, err
}

// PostReadRequest counts read
func (r *XServerHandler) PostReadRequest(ctx context.Context, m *protocol.Message, e error) error {
	sp := m.ServicePath
	sm := m.ServiceMethod

	if sp == "" {
		return nil
	}
	admin.PointRPCRequest(sp, sm)
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
	res := &XClientHandler{}
	return res
}

func NewRPCXServerHandler() server.PostCallPlugin {
	res := &XServerHandler{}
	return res
}
