package db

// C4 / A1b · FailureQueue 的 payload 编解码。
//
// 设计原则：
//   - payload 可跨进程 replay，需要包含"是哪张表的哪批 values"——但不能包含
//     Val 具体类型（泛型擦除后已经是 []any，只能依赖调用方的 DDL 约定做解码）
//   - 当前只支持最小元数据：表名 + values 数量 + values JSON 序列化
//   - replay 侧由业务决定怎么根据 tableName 找到对应 autoCacheManager 再执行
//     flushFn。框架不自动做 dispatch——因为泛型实例化在启动时已经完成，
//     一个进程里可能有多个 sqlBuilder[Val]，映射逻辑留给业务方自己管理
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

// resolveFailureQueue 返回 builder 里配置的 FailureQueue，nil 回落到 Noop。
// 把 nil check 抽成方法方便 toLongevity 内直接用。
func (a *autoCacheManager[Key, Val]) resolveFailureQueue() FailureQueue {
	if a.builder == nil || a.builder.longevityFailureQueue == nil {
		return NoopFailureQueue{}
	}
	return a.builder.longevityFailureQueue
}
