package db

// C4 / A1b · FailureQueue 的 payload 编解码。
//
// 设计原则：
//   - payload 可跨进程 replay，需要包含"是哪张表的哪批 values"——但不能包含
//     Val 具体类型（泛型擦除后已经是 []any，只能依赖调用方的 DDL 约定做解码）
//   - 当前只支持最小元数据：表名 + values 数量 + values JSON 序列化
//   - replay 侧 dispatch（E4 起为框架内置）：每个 longevity 管理器在 InitStruct
//     时按表名注册 replay flusher（failure_replay.go），ReplayPayload /
//     启动重放据此把 payload 分发到对应管理器的 flushBatch 真实落库。
//
// 格式：sonic.Marshal 的 JSON
//
//	{"table": "...", "count": N, "values": [...]}
//
// 选择 JSON 而不是 gob / protobuf 是为了：
//   - 跨语言可读
//   - 运维可以 cat/jq 看一眼现在队列里有什么
//   - 性能不是瓶颈——补偿队列只在故障路径上用

import (
	"fmt"

	"github.com/bytedance/sonic"
)

// FailurePayloadDoc 是 FailureQueue 里每条 payload 的逻辑结构。
// 供 encode/decode 双方共用，避免字段名写错。
type FailurePayloadDoc struct {
	Table  string `json:"table"`
	Count  int    `json:"count"`
	Values []any  `json:"values"`
}

// encodeFailurePayload 把一次 flush 的元数据打包成 FailurePayload 字节。
// 任何类型的 value 都会走 sonic 默认编码；time / 嵌套 struct / 数值都 OK，
// 但自定义二进制编码（比如 json.Marshaler 的实现）以调用方自己为准。
func encodeFailurePayload(table string, values []any, count int) (FailurePayload, error) {
	doc := FailurePayloadDoc{
		Table:  table,
		Count:  count,
		Values: values,
	}
	raw, err := sonic.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("db/failure: encode failed: %w", err)
	}
	return FailurePayload(raw), nil
}

// DecodeFailurePayload 是 encodeFailurePayload 的逆操作。
// 导出给业务 replay 侧使用——拿到 doc 后，按 doc.Table 分发到对应的 autoCacheManager
// 或者直接 execute 原生 SQL。
func DecodeFailurePayload(p FailurePayload) (*FailurePayloadDoc, error) {
	if len(p) == 0 {
		return nil, fmt.Errorf("db/failure: empty payload")
	}
	doc := &FailurePayloadDoc{}
	if err := sonic.Unmarshal(p, doc); err != nil {
		return nil, fmt.Errorf("db/failure: decode failed: %w", err)
	}
	return doc, nil
}

// resolveFailureQueue 返回 builder 里配置的 FailureQueue。
// E4：未配置（nil）时不再回落 Noop，而是回落到进程级默认 FileFailureQueue——
// 开箱即得"落库失败跨重启可恢复"。业务可用 WithLongevityFailureQueue 注入
// 自定义实现覆盖；显式传 NoopFailureQueue{} 可关闭补偿队列。
func (a *autoCacheManager[Key, Val]) resolveFailureQueue() FailureQueue {
	if a.builder == nil || a.builder.longevityFailureQueue == nil {
		return defaultFailureQueue()
	}
	return a.builder.longevityFailureQueue
}
