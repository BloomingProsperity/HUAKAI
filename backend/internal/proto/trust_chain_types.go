package proto

import "encoding/json"

// HUAKAI 信任链 T1：HopAttestation + ModelChain 类型定义。
//
// 设计依据：docs/process/plans/2026-05-13-trust-chain-feature-family-claude.md §3-§5。
// 与 sub2api / new-api / portkey / litellm / helicone 现有项目根本差异：
//   - 所有现有项目"信任商家"，user 看不到 hop chain；HUAKAI 强制 hop chain 写
//     入 Accounting，T2 加 ed25519 签名，T4 落 audit_ledger Merkle 链。
//   - 防偷换模型、防虚报 token、防伪造 cache hit 由 ModelChain 三方比对 +
//     audit_ledger cross-check 共同守护。
//
// 本文件只定义类型 + 基础 helper，**不**做签名 / 落库逻辑（T2/T4 才做）。
// schema 添加全部 omitempty，向后兼容 P-1 35+2 个 fixture 不破。

// HopHop 是 HopAttestation 的 hop 名称枚举；闭集合避免随意填字符串。
type HopHop string

const (
	HopIngress  HopHop = "ingress"  // HTTP 入口
	HopRouter   HopHop = "router"   // alias → pool 解析
	HopPool     HopHop = "pool"     // pool group 选定
	HopAccount  HopHop = "account"  // provider account 选定（含 PASR）
	HopProvider HopHop = "provider" // 上游 vendor endpoint
	HopResponse HopHop = "response" // 最终回写 client
)

// HopAttestation 记录链路单跳证据；按 ts 单调递增（T2 落）。
//
// 隐私守门：
//   - AccountIDHash 是 SHA-1/SHA-256 hash，禁止直接放 account_id；
//     避免攻击者拿明文 ID 直接锁定账号。
//   - Detail 仅放 hop-specific 元数据（如 cache_hit_ratio / latency_ms），
//     绝不能放 prompt / completion / tool 内容。
type HopAttestation struct {
	// SchemaVersion / HopIndex / HopKind / Actor / StartedAt / EndedAt /
	// DecisionRef 是 F-TRUST-001 面向 receipt 的字段。过渡期内较旧的 gateway
	// 路径可能仍然填写下面紧凑的 Hop/Timestamp 字段。
	SchemaVersion string   `json:"schema_version,omitempty"`
	HopIndex      int      `json:"hop_index,omitempty"`
	HopKind       string   `json:"hop_kind,omitempty"`
	Actor         string   `json:"actor,omitempty"`
	StartedAt     string   `json:"started_at,omitempty"`
	EndedAt       string   `json:"ended_at,omitempty"`
	DecisionRef   string   `json:"decision_ref,omitempty"`
	FeatureRefs   []string `json:"feature_refs,omitempty"`
	AltEventID    string   `json:"alt_event_id,omitempty"`

	// Hop 必填；闭集合枚举。
	Hop HopHop `json:"hop,omitempty"`

	// Timestamp 必填；RFC3339Nano；ts ↑ monotonic。
	Timestamp string `json:"ts,omitempty"`

	// RequestID 可选；request_id 已在 RequestMeta 顶层；hop 内重复仅做 audit 自包含。
	RequestID string `json:"request_id,omitempty"`

	// AccountIDHash 可选；hop=account 时写；其它 hop 留空。
	// hash 算法见 docs/process/plans/2026-05-13-trust-chain-feature-family-claude.md §11.8。
	AccountIDHash string `json:"account_id_hash,omitempty"`

	// PoolID 可选；hop=pool 时写。
	PoolID string `json:"pool_id,omitempty"`

	// RouteID 可选；hop=router 时写。
	RouteID string `json:"route_id,omitempty"`

	// Provider 可选；hop=provider 时写人读 vendor 名（anthropic / openai / gemini 等）。
	Provider string `json:"provider,omitempty"`

	// Endpoint 可选；hop=provider 时写完整 URL（不含 secrets）。
	Endpoint string `json:"endpoint,omitempty"`

	// DurationMS 可选；hop=response 等阶段记录本跳耗时。
	DurationMS int64 `json:"duration_ms,omitempty"`

	// Detail 可选；hop-specific 扩展，**严禁** prompt/completion/tool 内容。
	Detail json.RawMessage `json:"detail,omitempty"`
}

// ModelChain 三方比对模型名，反掺水 / 反映射。
// 取值规则：
//   - Requested = client request body model 字段（用户付费的模型）
//   - RouteDecided = HUAKAI router/pool 决策选用的模型（HUAKAI 自报）
//   - UpstreamReported = 上游 response model 字段（vendor 自报）
//
// 三者必须一致；任何不一致 → audit_ledger 记 divergence + warning（T3）。
type ModelChain struct {
	// Requested 必填；client request body 中 model 字段值。
	Requested string `json:"requested"`

	// RouteDecided 必填；HUAKAI 路由层决策的实际目标 model（可能与 Requested
	// 一致，也可能是 alias 解析后的真实 model）。
	RouteDecided string `json:"route_decided"`

	// UpstreamReported 可选；上游 vendor response 中携带的 model 字段；
	// streaming 流尚未结束时为空，response 完成后填。
	UpstreamReported string `json:"upstream_reported,omitempty"`

	// Verdict 是用户可见的模型一致性裁决：match / allowed_alias /
	// mismatch / unknown。为空时保持旧路径兼容。
	Verdict string `json:"verdict,omitempty"`
}

// IsConsistent 检查 Requested / RouteDecided / UpstreamReported 三方是否一致。
// UpstreamReported 为空时视为 streaming-in-flight，只校验前两者。
func (m *ModelChain) IsConsistent() bool {
	if m == nil {
		return true // nil = 未启用 chain，不算 inconsistent
	}
	if m.Requested == "" || m.RouteDecided == "" {
		return false
	}
	if m.Requested != m.RouteDecided {
		return false
	}
	if m.UpstreamReported != "" && m.UpstreamReported != m.Requested {
		return false
	}
	return true
}

// allValidHops 是 HopHop 枚举集合，HopChain 写入时 forwarder 校验用。
var allValidHops = map[HopHop]struct{}{
	HopIngress:  {},
	HopRouter:   {},
	HopPool:     {},
	HopAccount:  {},
	HopProvider: {},
	HopResponse: {},
}

// IsValidHop 检查 hop 名是否在闭集合内。
func IsValidHop(h HopHop) bool {
	if h == "" {
		return false
	}
	_, ok := allValidHops[h]
	return ok
}
