// Package cacheplan 存放非冻结的辅助函数, 用于支撑 gateway 的 cache_control
// breakpoint 规划器, 同时又不必放进冻结的 internal/gateway 包。
//
// 这里的唯一职责是一个自包含的探测器, 回答一个问题:
// 「客户端是否已经在这份 Anthropic Messages 请求 body 的任意位置放置了
// cache_control 字段?」gateway 出口路径在自动注入 breakpoint 之前会查询它,
// 这样自行管理 cache_control 的客户端就永远不会被改动。
//
// 本包刻意不 import internal/gateway: 是 gateway import cacheplan(单向),
// 因此探测逻辑必须是完全自包含的 JSON 遍历 —— 不联网、不做 IO、不改 body。
package cacheplan

import "encoding/json"

// HasAnyCacheControl 报告 Anthropic Messages 请求 body 是否在 system、任意消息
// content block 或任意 tool 定义中, 至少携带一个 cache_control 字段。
//
// 它刻意宽松: 在被检查路径下可达的、含有 "cache_control" 键的任意 JSON 对象,
// 无论其形状或取值如何, 都算作客户端提供。JSON 非法或为空时返回 false(未检测到
// 客户端 cache_control)—— 反正调用方也会原样保留这类 body, 而格式错误的 body
// 是 upstream 的问题, 不该由我们去改。
func HasAnyCacheControl(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || root == nil {
		return false
	}
	return systemHasCacheControl(root["system"]) ||
		messagesHaveCacheControl(root["messages"]) ||
		toolsHaveCacheControl(root["tools"])
}

// systemHasCacheControl 处理 system 的三种合法形状: 纯字符串
// (绝不会带 cache_control)、单个 content-block 对象, 或 block 数组。
func systemHasCacheControl(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if obj, ok := decodeObject(raw); ok {
		return objectHasCacheControl(obj)
	}
	if arr, ok := decodeArray(raw); ok {
		return anyBlockHasCacheControl(arr)
	}
	return false
}

// messagesHaveCacheControl 遍历每条消息, 并对数组形式的 content 遍历每个
// content block, 查找 cache_control 字段。
func messagesHaveCacheControl(raw json.RawMessage) bool {
	arr, ok := decodeArray(raw)
	if !ok {
		return false
	}
	for _, item := range arr {
		message, ok := decodeObject(item)
		if !ok {
			continue
		}
		// 直接挂在消息对象上的 cache_control 也算数。
		if objectHasCacheControl(message) {
			return true
		}
		content := message["content"]
		if blocks, ok := decodeArray(content); ok && anyBlockHasCacheControl(blocks) {
			return true
		}
	}
	return false
}

// toolsHaveCacheControl 检查每个 tool 定义对象。
func toolsHaveCacheControl(raw json.RawMessage) bool {
	arr, ok := decodeArray(raw)
	if !ok {
		return false
	}
	return anyBlockHasCacheControl(arr)
}

func anyBlockHasCacheControl(blocks []json.RawMessage) bool {
	for _, b := range blocks {
		obj, ok := decodeObject(b)
		if !ok {
			continue
		}
		if objectHasCacheControl(obj) {
			return true
		}
	}
	return false
}

func objectHasCacheControl(obj map[string]json.RawMessage) bool {
	_, ok := obj["cache_control"]
	return ok
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func decodeArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	return arr, true
}
