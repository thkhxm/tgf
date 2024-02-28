package admin

import (
	"encoding/base64"
	"github.com/bytedance/sonic"
	"github.com/rpcxio/libkv"
	"github.com/rpcxio/libkv/store"
	"github.com/rpcxio/libkv/store/consul"
	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/tgf"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/20
//***************************************************

type ConsulData struct {
	ID         string      `json:"id"`
	ModuleName string      `json:"moduleName"`
	Address    string      `json:"address"`
	NodeId     string      `json:"nodeId"`
	Group      string      `json:"group"`
	Metadata   []*MetaData `json:"metadata"`
	Tags       []string    `json:"tags"`
}

type MetaData struct {
	Key  string `json:"key"`
	Data string `json:"data"`
}

var consulRegistry *ConsulRegistry

type ConsulRegistry struct {
	kv            store.Store
	baseURL       string
	registryURL   string
	StateCallBack func(name, address string, state client.ConsulServerState)
}

func (r *ConsulRegistry) InitRegistry() {
	consul.Register()
	r.baseURL = tgf.GetStrConfig[string](tgf.EnvironmentConsulPath)
	if strings.HasPrefix(r.baseURL, "/") {
		r.baseURL = r.baseURL[1:]
	}
	r.registryURL = tgf.GetStrConfig[string](tgf.EnvironmentConsulAddress)
	//

	kv, err := libkv.NewStore(store.CONSUL, []string{r.registryURL}, nil)
	if err != nil {
		log.Printf("cannot create etcd registry: %v", err)
		return
	}
	r.kv = kv

	return
}

func (r *ConsulRegistry) fetchServices() []*ConsulData {
	var services []*ConsulData
	kvs, err := r.kv.List(r.baseURL)
	if err != nil {
		log.Printf("failed to list services %s: %v", r.baseURL, err)
		return services
	}

	for _, value := range kvs {

		nodes, err := r.kv.List(value.Key)
		if err != nil {
			log.Printf("failed to list %s: %v", value.Key, err)
			continue
		}

		for _, n := range nodes {
			key := n.Key[:]
			i := strings.LastIndex(key, "/")
			serviceName := strings.TrimPrefix(key[0:i], r.baseURL)
			var serviceAddr string
			fields := strings.Split(key, "/")
			if fields != nil && len(fields) > 1 {
				serviceAddr = fields[len(fields)-1]
			}
			v, err := url.ParseQuery(string(n.Value[:]))
			if err != nil {
				log.Println("etcd value parse failed. error: ", err.Error())
				continue
			}
			state := "n/a"
			group := ""
			state = v.Get("state")
			if state == "" {
				state = "active"
			}

			version := "v" + v.Get("version")

			group = v.Get("group")
			id := base64.StdEncoding.EncodeToString([]byte(serviceName + "^" + serviceAddr))
			md := string(n.Value[:])
			mdv, _ := url.ParseQuery(md)
			var metaData []*MetaData
			for k, v := range mdv {
				metaData = append(metaData, &MetaData{
					Key:  k,
					Data: v[0],
				})
			}
			service := &ConsulData{ID: id, ModuleName: serviceName[1:],
				Address: strings.Split(serviceAddr, "@")[1], Metadata: metaData,
				Tags: []string{state, version}, Group: group,
				NodeId: v.Get("nodeId")}
			services = append(services, service)
		}
	}
	return services
}

func (r *ConsulRegistry) DeactivateService(writer http.ResponseWriter, request *http.Request) {
	var name, address string
	var kv *store.KVPair
	var err error
	id := request.PathValue("id")
	did, _ := base64.StdEncoding.DecodeString(id)
	id = string(did)
	name = strings.Split(id, "^")[0]
	address = strings.Split(id, "^")[1]
	key := path.Join(r.baseURL, name, address)
	result := "fail"
	defer func() {
		if err == nil {
			result = "success"
		}
		writer.Write([]byte(result))
	}()

	kv, err = r.kv.Get(key)
	if err != nil {
		return
	}

	v, err := url.ParseQuery(string(kv.Value[:]))
	if err != nil {
		log.Println("etcd value parse failed. err ", err.Error())
		return
	}
	v.Set("state", string(client.ConsulServerStateInActive))
	err = r.kv.Put(kv.Key, []byte(v.Encode()), &store.WriteOptions{IsDir: false})
	if err != nil {
		log.Println("etcd set failed, err : ", err.Error())
	}
	if r.StateCallBack != nil {
		r.StateCallBack(name[1:], address, client.ConsulServerStateInActive)
	}
	return
}

func (r *ConsulRegistry) PauseService(writer http.ResponseWriter, request *http.Request) {
	var name, address string
	var kv *store.KVPair
	var err error
	id := request.PathValue("id")
	did, _ := base64.StdEncoding.DecodeString(id)
	id = string(did)
	name = strings.Split(id, "^")[0]
	address = strings.Split(id, "^")[1]
	key := path.Join(r.baseURL, name, address)
	result := "fail"
	defer func() {
		if err == nil {
			result = "success"
		}
		writer.Write([]byte(result))
	}()

	kv, err = r.kv.Get(key)
	if err != nil {
		return
	}

	v, err := url.ParseQuery(string(kv.Value[:]))
	if err != nil {
		log.Println("etcd value parse failed. err ", err.Error())
		return
	}
	v.Set("state", string(client.ConsulServerStatePause))
	err = r.kv.Put(kv.Key, []byte(v.Encode()), &store.WriteOptions{IsDir: false})
	if err != nil {
		log.Println("etcd set failed, err : ", err.Error())
	}
	if r.StateCallBack != nil {
		r.StateCallBack(name[1:], address, client.ConsulServerStatePause)
	}
	return
}

func (r *ConsulRegistry) ActivateService(writer http.ResponseWriter, request *http.Request) {
	var name, address string
	var kv *store.KVPair
	var err error
	result := "fail"
	id := request.PathValue("id")
	did, _ := base64.StdEncoding.DecodeString(id)
	id = string(did)
	name = strings.Split(id, "^")[0]
	address = strings.Split(id, "^")[1]
	defer func() {
		if err == nil {
			result = "success"
		}
		writer.Write([]byte(result))
	}()

	key := path.Join(r.baseURL, name, address)
	kv, err = r.kv.Get(key)

	v, err := url.ParseQuery(string(kv.Value[:]))
	if err != nil {
		log.Println("etcd value parse failed. err ", err.Error())
		return
	}
	v.Set("state", string(client.ConsulServerStateActive))
	err = r.kv.Put(kv.Key, []byte(v.Encode()), &store.WriteOptions{IsDir: false})
	if err != nil {
		log.Println("etcdv3 put failed. err: ", err.Error())
	}
	if r.StateCallBack != nil {
		r.StateCallBack(name[1:], address, client.ConsulServerStateActive)
	}
	return
}

func (r *ConsulRegistry) updateMetadata(name, address string, metadata string) error {
	key := path.Join(r.baseURL, name, address)
	err := r.kv.Put(key, []byte(metadata), &store.WriteOptions{IsDir: false})
	return err
}

func (r *ConsulRegistry) ConsulList(writer http.ResponseWriter, request *http.Request) {
	services := r.fetchServices()
	jsonData, _ := sonic.Marshal(services)
	writer.Write(jsonData)
}
