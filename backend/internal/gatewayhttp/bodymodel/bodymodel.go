// Package bodymodel 负责出站请求体顶层 model 字段的比对与定点改写。
// 抽出成独立职责,既服务官方直发的"仅在 alias≠上游 model 时才改写"接缝,
// 也服务通用出站的 model 归一。
package bodymodel

import "encoding/json"

// ModelMatches 报告 body 顶层 model 是否已等于目标上游 model;相等则无需改写。
// 无法解析视为不相等(交给改写路径处理)。
func ModelMatches(body []byte, upstreamModelID string) bool {
	var obj struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &obj) != nil {
		return false
	}
	return obj.Model == upstreamModelID
}

// RewriteModel 把 body 顶层 model 定点改写为 upstreamModelID;ok=false 表示解析失败、
// 应退回原 body。只触碰顶层 model 键,其它字段保留(重排由 json.Marshal 决定)。
func RewriteModel(body []byte, upstreamModelID string) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body, false
	}
	if obj == nil { // 合法 JSON null/非对象 → 解出 nil map,禁止赋值(否则 panic)
		return body, false
	}
	modelRaw, err := json.Marshal(upstreamModelID)
	if err != nil {
		return body, false
	}
	obj["model"] = modelRaw
	out, err := json.Marshal(obj)
	if err != nil {
		return body, false
	}
	return out, true
}
