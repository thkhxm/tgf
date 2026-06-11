package rpc

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/cornelk/hashmap"
	"github.com/edwingeng/doublejump"
	client2 "github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/rpcx/v2/protocol"
	"github.com/thkhxm/rpcx/v2/server"
	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf/v2"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/db"
	"github.com/thkhxm/tgf/v2/exp/admin"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/util"
	"go.uber.org/zap"
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
	// version 字段期望格式 "major.sub"，但 Consul 注册串可能被外部篡改或
	// 测试里喂进残缺数据（历史 plugins_test.go 就这么跑），直接 Split 再取
	// [1] 会 panic。这里做显式边界检查：缺 sub 时回落为 0，缺 major 时直接
	// 返回空 info 让调用方丢弃条目。
	q, err := url.ParseQuery(data)
	if err != nil {
		return nil
	}
	parts := strings.Split(q.Get("version"), ".")
	var v, subv int32
	if len(parts) >= 1 && parts[0] != "" {
		v, _ = util.StrToAny[int32](parts[0])
	}
	if len(parts) >= 2 && parts[1] != "" {
		subv, _ = util.StrToAny[int32](parts[1])
	}
	return &ConsulServerInfo{
		State:      q.Get("state"),
		Version:    v,
		SubVersion: subv,
		NodeId:     q.Get("nodeId"),
	}
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
				log.WarnTagW("rpc", "节点更新异常", zap.String("uid", uid), zap.String("node", selected), zap.Error(err))
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
			log.DebugTagW("discovery", "remove server",
				zap.String("node", k), zap.String("nodeId", v.NodeId), zap.String("state", v.State))
		}
		if v.State == string(client2.ConsulServerStatePause) {
			c.h.Remove(k)
			log.DebugTagW("discovery", "server paused",
				zap.String("node", k), zap.String("state", v.State))
		}
		return true
	})

	if clearUserCache {
		c.clearAllUserCache()
		log.DebugTagW("discovery", "更新服务节点", log.Module(c.moduleName))
	}
	log.DebugTagW("discovery", "节点数据", log.Module(c.moduleName), zap.String("servers", serverInfos))
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
	// E 档配置读点迁移：bool 项直接读类型化字段（原 GetStrConfig[int32]==1 旧读法，
	// 新配置系统对 "true"/"yes" 等宽松写法的解析两套 API 同源同值）。
	c.pushGate = tgfconfig.Current().Gate.Push
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
	// B5 热路径迁移：trace 是高频且生产默认关闭的 tag。先用 CheckLogTag 短路，
	// 命中才做 sonic.MarshalString（最贵的一步）+ 零分配 *TagW 结构化输出，
	// 避免 tag 关闭时仍然无条件 marshal 整个 args。
	if log.CheckLogTag("trace") {
		argStr, _ := sonic.MarshalString(args)
		log.DebugTagWT("trace", traceId, "client 发送请求",
			log.NodeID(), log.Module(serviceName), log.Method(methodName), zap.String("args", argStr))
	}
	return nil
}

func (r *XClientHandler) PostCall(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) error {
	var traceId string
	if sc, ok := ctx.(*share.Context); ok {
		traceId = sc.GetReqMetaDataByKey(tgf.ContextKeyTRACEID)
	}
	if log.CheckLogTag("trace") {
		replyStr, _ := sonic.MarshalString(reply)
		log.DebugTagWT("trace", traceId, "client 接收响应",
			log.NodeID(), log.Module(servicePath), log.Method(serviceMethod), zap.String("reply", replyStr))
	}
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
	if log.CheckLogTag("trace") {
		argStr, _ := sonic.MarshalString(args)
		log.DebugTagWT("trace", traceId, "server 接收请求",
			log.NodeID(), log.Module(serviceName), log.Method(methodName), zap.String("args", argStr))
	}
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
	if log.CheckLogTag("trace") {
		replyStr, _ := sonic.MarshalString(reply)
		log.DebugTagWT("trace", traceId, "server 执行完毕",
			log.NodeID(), log.Module(servicePath), log.Method(serviceMethod),
			zap.Int64("costMs", d), zap.String("reply", replyStr))
	}
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

// ILoginCheck 是登录凭据校验接口（D7 / P0-6 起为 gate.Login 的强制接线点）。
//
// CheckLogin(token) 返回 (是否通过, 凭据绑定的 userId)：
//   - false → 登录被拒绝；
//   - true 且 userId 非空 → gate.Login 强制其与 LoginReq.UserId 一致（防冒充）；
//   - true 且 userId 为空 → 只验凭据有效性、不绑定身份（由业务实现自行保证安全）。
//
// 默认实现是 HMAC 签名 token（login_check.go hmacLoginCheck，密钥来自
// WithLoginTokenSecret / 环境变量 LoginTokenSecret，未配置时 fail-closed 拒绝）。
// 业务通过 Server.WithLoginCheck 注入自定义实现；Server.WithoutLoginCheck 显式
// 关闭校验（仅限可信环境）。
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
