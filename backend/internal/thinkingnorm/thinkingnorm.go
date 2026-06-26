// Package thinkingnorm 在转发前对 Anthropic 扩展思考(extended-thinking)请求
// 做归一化,使其满足 Anthropic 的硬性合法性约束:当启用 thinking 时,
// Anthropic 要求 temperature == 1,且 tool_choice 的 type 为 auto/none —— 任何
// 其它取值都会返回 400。没有这一步,客户端的 thinking 请求就会直接失败。
// 这是一项正确性归一化,而非语义变更:否则该请求本来就不可能成功。
package thinkingnorm

import "encoding/json"

// NormalizeThinkingValidity 返回已强制施加 Anthropic thinking 约束的 body。
// 对于没有激活 thinking 字段的 body、已经合法的 body,或解析失败的 body,
// 都原样返回(逐字节相同、fail-safe)—— 只有携带非法 temperature/tool_choice
// 的 thinking 请求才会被重新编码。
func NormalizeThinkingValidity(body []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	thinkingRaw, ok := m["thinking"]
	if !ok {
		return body
	}
	var thinking map[string]json.RawMessage
	if err := json.Unmarshal(thinkingRaw, &thinking); err != nil {
		return body
	}
	var ttype string
	_ = json.Unmarshal(thinking["type"], &ttype)
	if ttype != "enabled" && ttype != "adaptive" && ttype != "auto" {
		return body // thinking 未激活 -> 不施加约束
	}

	changed := false

	// (1) 扩展思考下 temperature 必须恰好为 1。
	if tempRaw, ok := m["temperature"]; ok {
		var temp float64
		if json.Unmarshal(tempRaw, &temp) == nil && temp != 1 {
			if v, err := json.Marshal(1); err == nil {
				m["temperature"] = v
				changed = true
			}
		}
	}

	// (2) 扩展思考下 tool_choice 的 type 必须是 auto 或 none。
	if tcRaw, ok := m["tool_choice"]; ok {
		var tc map[string]json.RawMessage
		if json.Unmarshal(tcRaw, &tc) == nil {
			var tctype string
			_ = json.Unmarshal(tc["type"], &tctype)
			if tctype != "" && tctype != "auto" && tctype != "none" {
				if v, err := json.Marshal(map[string]string{"type": "auto"}); err == nil {
					m["tool_choice"] = v
					changed = true
				}
			}
		}
	}

	if !changed {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
