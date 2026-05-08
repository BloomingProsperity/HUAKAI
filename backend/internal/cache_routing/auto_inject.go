// auto_inject.go — Track C: 自动 cache_control 注入。
//
// 客户端如果只发 string-form system prompt 而不知道 Anthropic 缓存机制,
// HUAKAI 看 prompt 够大就替它加 cache_control:{type:"ephemeral"} marker
// → vendor 端缓存这段 prefix → 后续相同 prefix 命中。
//
// 与 Track B sticky routing 互补：
//   - Track B 让相同 prefix 路由到同 account（缓存命中前置）
//   - Track C 让客户端不知道也能享受缓存（无客户端代码改动即生效）
//
// 安全约束:
//   - 客户端**已存在** cache_control marker → 尊重不覆盖
//   - prompt 短于 minTokens 阈值 → 不注入（vendor 不缓存小于 1024 token 的 block）
//   - 非 Anthropic Messages 形态 body (OpenAI shape 等) → 不动
//
// 不做:
//   - 不动 messages array (用户每轮内容不同, 没价值)
//   - 不动 tools (Anthropic 缓存 tool 数组要看大小, 默认不注入避免过度优化)
//   - 不破坏 RawMessage 透传一致性 (U7 字段透传矩阵)
package cache_routing

import (
	"bytes"
	"encoding/json"
)

// DefaultMinSystemBytesForCache 是触发自动 cache_control 注入的 system prompt
// 字节阈值。
//
// Anthropic 缓存最小 1024 token 才生效；按 1 token ≈ 3-4 字节英文/中文混合
// 估算, 取保守 4096 字节阈值（≈ 1024 token）。低于此值注入是浪费(vendor
// 拒绝缓存)。
const DefaultMinSystemBytesForCache = 4096

// AutoInjectSystemCacheControl 给 Anthropic Messages 形 body 自动加
// cache_control:{ephemeral} 到 system 字段, 让长 system prompt 触发 vendor
// 缓存。
//
// 行为:
//   1. body 非 JSON object → 原样返回
//   2. 缺 system 字段 → 原样返回
//   3. system 字节小于 minBytes → 原样返回（vendor 不缓存太短的 block）
//   4. system 是 string 形态 → wrap 为 [{type:"text",text:..,cache_control:..}]
//   5. system 是 array 形态:
//      - 最末 block 已有 cache_control → 尊重 caller, 不覆盖
//      - 否则给最末 block 加 cache_control
//
// minBytes 0 → 用 DefaultMinSystemBytesForCache。
func AutoInjectSystemCacheControl(body []byte, minBytes int) []byte {
	if minBytes <= 0 {
		minBytes = DefaultMinSystemBytesForCache
	}
	if len(body) == 0 {
		return body
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || top == nil {
		return body
	}
	sysRaw, ok := top["system"]
	if !ok || len(sysRaw) == 0 {
		return body
	}
	// 阈值检查（用 raw bytes 长度近似 token 数, 保守估算）
	if len(sysRaw) < minBytes {
		return body
	}

	// case 1: system 是 string —— 转 array 形并加 cache_control
	var sysStr string
	if err := json.Unmarshal(sysRaw, &sysStr); err == nil {
		blockList := []map[string]json.RawMessage{
			{
				"type":          json.RawMessage(`"text"`),
				"text":          sysRaw,
				"cache_control": json.RawMessage(`{"type":"ephemeral"}`),
			},
		}
		newSys, mErr := json.Marshal(blockList)
		if mErr != nil {
			return body
		}
		top["system"] = newSys
		// sonnet SHOULD_FIX: 不能丢 outer marshal err — 错误时 nil body 会
		// 让 SigV4 hash 空 payload，请求被拒。fail-safe 返回原 body。
		out, err := json.Marshal(top)
		if err != nil {
			return body
		}
		return out
	}

	// case 2: system 是 array —— 最末 block 加 cache_control（除非已有）
	//
	// 用 []json.RawMessage 而不是 []map[...] 解，避免 codex BLOCKING B2
	// 触发的 nil-map panic：`system: [{...long}, null]` unmarshal 后
	// blocks[lastIdx] 是 nil map，赋值 cache_control 会 panic。
	var blocks []json.RawMessage
	if err := json.Unmarshal(sysRaw, &blocks); err != nil {
		return body
	}
	if len(blocks) == 0 {
		return body
	}
	// 任意已有 cache_control 标记都尊重 caller 意图（codex BLOCKING B3
	// 加固）：扫所有 block 而不只末块，避免破坏 caller 的整体缓存策略。
	for _, rawBlock := range blocks {
		if isJSONNullOrNonObject(rawBlock) {
			continue
		}
		if blockHasCacheControl(rawBlock) {
			return body
		}
	}
	lastIdx := len(blocks) - 1
	lastBlock := blocks[lastIdx]
	// 末块若是 null/string/non-object → 不能挂 cache_control，no-op
	var lastObj map[string]json.RawMessage
	if err := json.Unmarshal(lastBlock, &lastObj); err != nil || lastObj == nil {
		return body
	}
	lastObj["cache_control"] = json.RawMessage(`{"type":"ephemeral"}`)
	relastBlock, err := json.Marshal(lastObj)
	if err != nil {
		return body
	}
	blocks[lastIdx] = relastBlock
	newSys, mErr := json.Marshal(blocks)
	if mErr != nil {
		return body
	}
	top["system"] = newSys
	// sonnet SHOULD_FIX: 同上 — outer marshal 失败时返回原 body 而不是 nil。
	out, err := json.Marshal(top)
	if err != nil {
		return body
	}
	return out
}

// HasCacheControlMarker 是 body 是否含 cache_control marker 的快速 prefilter。
//
// **lossy heuristic, 不是正确性闸门**（codex SHOULD_FIX 1）：用户消息正文
// 字面含 "cache_control" 时会 false-positive，但只用作"是否值得跑完整
// unmarshal/marshal" 的优化决策——false-positive 安全（多 skip 一次注入），
// false-negative 才危险（多注入一次但 AutoInjectSystemCacheControl 内部仍有
// 末块/全块 cache_control 检查兜底）。
//
// 不可用于：correctness gating（"已有 marker → 拒绝处理"这类决策）。
func HasCacheControlMarker(body []byte) bool {
	return bytes.Contains(body, []byte(`"cache_control"`))
}

// isJSONNullOrNonObject 判断一个 JSON RawMessage 是否是 null / 非 object 形态。
// 用于过滤掉 system array 中的 null / string 元素（避免 nil-map 赋值 panic）。
func isJSONNullOrNonObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return true
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	return trimmed[0] != '{'
}

// blockHasCacheControl 检查单个 system block (raw object bytes) 是否声明了
// cache_control 字段。
func blockHasCacheControl(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return false
	}
	_, has := obj["cache_control"]
	return has
}
