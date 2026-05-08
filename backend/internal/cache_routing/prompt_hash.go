// Package cache_routing — Track B: 给 sticky routing 提供 prompt-prefix
// hash, 让相同 prefix 的请求在 session_hash 维度被路由到同一个 provider
// account, 最大化 vendor (Anthropic / OpenAI / Bedrock) prompt cache 命中率。
//
// 设计:
//   - 输入: HUAKAI 收到的客户端 raw request body (JSON object)
//   - 输出: stable hex string, 同 (system, tools) prefix → 同 hash
//   - 用作: pool.SelectionRequest.SessionHash, 进而 sticky_bindings 锁定账号
//
// 核心权衡:
//   - 只 hash (system, tools) — 不 hash messages, 因为 messages 末尾 (用户
//     新消息) 总在变, 但 vendor 缓存 key 是 prefix; messages 不进 hash 让
//     同一 conversation 不同轮次仍稳定路由到同一账号
//   - tools 进 hash 必要: tools 改变 = vendor cache key 改变
//   - JSON 字段顺序: 先抽取 system+tools 的 raw bytes 再 hash, 排除顶层
//     字段顺序变化（不同客户端 SDK 序列化顺序不同）
//
// 不做:
//   - 不 hash 整个 body (会让每条不同用户消息都生成新 hash → 没法 sticky)
//   - 不做 token-level fuzzy match (cache 是 byte-exact, 字符级差异都 miss)
//   - 不持久化 (本包只算 hash, 持久化 sticky 已存在 sticky_bindings 表)
package cache_routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PromptHashEmpty 是输入无法解析时的稳定空值——避免 caller 误把不同
// "解析失败"请求都当成同一 hash 路由（用空字符串而非 0-hash）。
const PromptHashEmpty = ""

// ComputePromptHash 从 raw JSON body 抽 system + tools 计算 SHA-256 hex。
//
// 行为:
//   - body 非合法 JSON object → 返回 PromptHashEmpty (caller 跳过 sticky)
//   - 缺 system + 缺 tools → 返回 PromptHashEmpty (无前缀可缓存)
//   - 系统提示和工具列表都按 raw bytes 进 hash（保留原 JSON 子结构语义）
//   - 双 SHA-256 抽取避免顶层字段顺序差异 (字段顺序不影响 hash)
//
// 返回 64 字符 lowercase hex (sha256), 或空串 (PromptHashEmpty)。
func ComputePromptHash(rawBody []byte) string {
	if len(rawBody) == 0 {
		return PromptHashEmpty
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &top); err != nil || top == nil {
		return PromptHashEmpty
	}

	systemRaw := top["system"]
	toolsRaw := top["tools"]

	// 都缺则无 prefix 可 hash, 不参与 sticky (短 prompt 路径走 round-robin)
	if len(systemRaw) == 0 && len(toolsRaw) == 0 {
		return PromptHashEmpty
	}

	h := sha256.New()
	// 字段名前缀防 (system="x", tools=null) 与 (system=null, tools="x") 碰撞
	h.Write([]byte("system:"))
	h.Write(systemRaw)
	h.Write([]byte("|tools:"))
	h.Write(toolsRaw)

	return hex.EncodeToString(h.Sum(nil))
}
