// Package api 放置网关与业务服共享的 RPC 契约。
package api

import (
	"github.com/thkhxm/tgf/v2/rpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	UserModule       = "user"
	GetProfileMethod = "GetProfile"
	GetProfileType   = UserModule + "." + GetProfileMethod
)

// GetProfile 使用网关可达的 Args/Reply 契约。示例复用 protobuf
// wrappers，业务项目应换成自己的 .proto 生成类型。
var GetProfile = &rpc.ServiceAPI[
	*rpc.Args[*wrapperspb.StringValue],
	*rpc.Reply[*wrapperspb.StringValue],
]{
	ModuleName:  UserModule,
	Name:        GetProfileMethod,
	MessageType: GetProfileType,
}
