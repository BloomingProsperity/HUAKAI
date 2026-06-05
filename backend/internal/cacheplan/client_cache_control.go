// Package cacheplan holds non-frozen helpers that support the gateway
// cache_control breakpoint planner without living in the frozen
// internal/gateway package.
//
// The single responsibility here is a self-contained detector that answers:
// "did the client already place any cache_control field anywhere in this
// Anthropic Messages request body?" The gateway egress path consults this
// before auto-injecting breakpoints, so a client that manages its own
// cache_control is never touched.
//
// This package intentionally does NOT import internal/gateway: gateway
// imports cacheplan (one direction), so the detection logic must be wholly
// self-contained JSON traversal — no network, no IO, no body mutation.
package cacheplan

import "encoding/json"

// HasAnyCacheControl reports whether the Anthropic Messages request body
// carries at least one cache_control field in system, any message content
// block, or any tool definition.
//
// It is deliberately permissive: any JSON object reachable under the
// inspected paths that contains a "cache_control" key counts as
// client-supplied, regardless of that object's shape or value. On invalid or
// empty JSON it returns false (no client cache_control detected) — the caller
// then leaves such bodies untouched anyway, and a malformed body is the
// upstream's problem, not ours to mutate.
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

// systemHasCacheControl handles the three legal system shapes: plain string
// (never has cache_control), single content-block object, or array of blocks.
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

// messagesHaveCacheControl walks every message and, for array-form content,
// every content block, looking for a cache_control field.
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
		// A cache_control directly on the message object also counts.
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

// toolsHaveCacheControl inspects each tool definition object.
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
