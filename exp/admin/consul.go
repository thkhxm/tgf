package admin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/rpcxio/libkv"
	"github.com/rpcxio/libkv/store"
	"github.com/rpcxio/libkv/store/consul"
	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/tgf/v2"
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

type ConsulRegistry struct {
	kv            store.Store
	baseURL       string
	registryURL   string
	StateCallBack func(name, address string, state client.ConsulServerState)
}

func (r *ConsulRegistry) InitRegistry() {
	consul.Register()
	r.baseURL = strings.TrimPrefix(tgf.GetStrConfig[string](tgf.EnvironmentConsulPath), "/")
	r.registryURL = tgf.GetStrConfig[string](tgf.EnvironmentConsulAddress)

	kv, err := libkv.NewStore(store.CONSUL, []string{r.registryURL}, nil)
	if err != nil {
		log.Printf("cannot create consul registry: %v", err)
		return
	}
	r.kv = kv
}

func (r *ConsulRegistry) fetchServices() []*ConsulData {
	services := make([]*ConsulData, 0)
	if r == nil || r.kv == nil {
		log.Printf("cannot list consul services: registry is not initialized")
		return services
	}

	kvs, err := r.kv.List(r.baseURL)
	if err != nil {
		log.Printf("failed to list services %s: %v", r.baseURL, err)
		return services
	}

	for _, value := range kvs {
		if value == nil {
			continue
		}
		nodes, listErr := r.kv.List(value.Key)
		if listErr != nil {
			log.Printf("failed to list %s: %v", value.Key, listErr)
			continue
		}

		baseServiceName := strings.TrimPrefix(value.Key, r.baseURL)
		for _, node := range nodes {
			if node == nil {
				continue
			}
			key := node.Key
			separator := strings.LastIndex(key, "/")
			if separator <= 0 || separator == len(key)-1 {
				log.Printf("skip malformed consul service key %q", key)
				continue
			}
			serviceName := strings.TrimPrefix(key[:separator], r.baseURL)
			if serviceName == "" || baseServiceName != serviceName {
				continue
			}
			serviceAddr := key[separator+1:]
			addressParts := strings.SplitN(serviceAddr, "@", 2)
			if len(addressParts) != 2 || addressParts[1] == "" {
				log.Printf("skip malformed consul service address %q", serviceAddr)
				continue
			}

			metadata, parseErr := url.ParseQuery(string(node.Value))
			if parseErr != nil {
				log.Printf("consul value parse failed: %v", parseErr)
				continue
			}
			state := metadata.Get("state")
			if state == "" {
				state = "active"
			}

			metaData := make([]*MetaData, 0, len(metadata))
			for key, values := range metadata {
				if len(values) == 0 {
					continue
				}
				metaData = append(metaData, &MetaData{Key: key, Data: values[0]})
			}
			services = append(services, &ConsulData{
				ID:         base64.StdEncoding.EncodeToString([]byte(serviceName + "^" + serviceAddr)),
				ModuleName: strings.TrimPrefix(serviceName, "/"),
				Address:    addressParts[1],
				NodeId:     metadata.Get("nodeId"),
				Group:      metadata.Get("group"),
				Metadata:   metaData,
				Tags:       []string{state, "v" + metadata.Get("version")},
			})
		}
	}
	return services
}

func decodeServiceID(encodedID string) (name, address string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(encodedID)
	if err != nil {
		return "", "", fmt.Errorf("decode service id: %w", err)
	}
	parts := strings.SplitN(string(decoded), "^", 2)
	if len(parts) != 2 {
		return "", "", errors.New("invalid service id: missing separator")
	}
	name, address = parts[0], parts[1]
	moduleName := strings.TrimPrefix(name, "/")
	if moduleName == "" || moduleName == "." || moduleName == ".." ||
		strings.Contains(moduleName, "/") || path.Clean(moduleName) != moduleName ||
		address == "" || address == "." || address == ".." ||
		strings.Contains(address, "/") || path.Clean(address) != address {
		return "", "", errors.New("invalid service id: malformed name or address")
	}
	return name, address, nil
}

func (r *ConsulRegistry) setServiceState(encodedID string, state client.ConsulServerState) error {
	if r == nil || r.kv == nil {
		return errors.New("consul registry is not initialized")
	}
	name, address, err := decodeServiceID(encodedID)
	if err != nil {
		return err
	}

	key := path.Join(r.baseURL, name, address)
	kv, err := r.kv.Get(key)
	if err != nil {
		return fmt.Errorf("get consul service %q: %w", key, err)
	}
	if kv == nil {
		return fmt.Errorf("get consul service %q: empty result", key)
	}
	values, err := url.ParseQuery(string(kv.Value))
	if err != nil {
		return fmt.Errorf("parse consul service %q metadata: %w", key, err)
	}
	values.Set("state", string(state))
	if err = r.kv.Put(kv.Key, []byte(values.Encode()), &store.WriteOptions{IsDir: false}); err != nil {
		return fmt.Errorf("update consul service %q: %w", key, err)
	}
	if r.StateCallBack != nil {
		r.StateCallBack(strings.TrimPrefix(name, "/"), address, state)
	}
	return nil
}

func writeResult(writer http.ResponseWriter, result string) error {
	if _, err := writer.Write([]byte(result)); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

func (r *ConsulRegistry) changeServiceState(writer http.ResponseWriter, request *http.Request, state client.ConsulServerState) {
	result := "fail"
	if err := r.setServiceState(request.PathValue("id"), state); err != nil {
		log.Printf("change consul service state to %s failed: %v", state, err)
	} else {
		result = "success"
	}
	if err := writeResult(writer, result); err != nil {
		log.Printf("change consul service state response failed: %v", err)
	}
}

func (r *ConsulRegistry) DeactivateService(writer http.ResponseWriter, request *http.Request) {
	r.changeServiceState(writer, request, client.ConsulServerStateInActive)
}

func (r *ConsulRegistry) PauseService(writer http.ResponseWriter, request *http.Request) {
	r.changeServiceState(writer, request, client.ConsulServerStatePause)
}

func (r *ConsulRegistry) ActivateService(writer http.ResponseWriter, request *http.Request) {
	r.changeServiceState(writer, request, client.ConsulServerStateActive)
}

func (r *ConsulRegistry) ConsulList(writer http.ResponseWriter, _ *http.Request) {
	if err := writeJSONResponse(writer, r.fetchServices()); err != nil {
		log.Printf("list consul services response failed: %v", err)
	}
}
