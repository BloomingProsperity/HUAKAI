package proto

// CacheScope 标记 cache_control 作用域。
type CacheScope string

const (
	CacheScopeRequest CacheScope = "request"
	CacheScopeMessage CacheScope = "message"
	CacheScopeBlock   CacheScope = "block"
	CacheScopeSession CacheScope = "session"
	CacheScopeVendor  CacheScope = "vendor"
)

// CacheControlNode 是 cache_control capability 的 payload。
//
// D6 已批保守边界：本 P-0 schema 包含 LocalityHint 留位（PASR cache-aware 关联），
// 但不包含 ReplicationIntent（跨账号 cache 复制属于 P-8 roadmap，不在 P-0 暴露）。
type CacheControlNode struct {
	// Scope 必填；request/message/block/session/vendor。
	Scope CacheScope `json:"scope"`

	// BreakpointRefs 必填；指向 node id 或 message/block ref；空数组表示无断点但保留 cache policy。
	BreakpointRefs []string `json:"breakpoint_refs"`

	// CacheKeyHint 可选；只能是 hash/hint，禁止写 prompt 明文。
	CacheKeyHint string `json:"cache_key_hint,omitempty"`

	// CacheCreationInputTokens 可选；映射 CanonicalUsage.CacheCreationInputTokens。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`

	// CacheReadInputTokens 可选；映射 CanonicalUsage.CacheReadInputTokens。
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`

		// SanitizeSystemMetadata 必填；默认 true；防止动态 billing/header metadata 破坏 prefix cache。
		// 这是从真实 cache_read=0 场景沉淀出的需求。
		SanitizeSystemMetadata bool `json:"sanitize_system_metadata"`

	// LocalityHint 可选；PASR cache-aware 关联留位；P-0 仅记录，不在 P-0 触发 selector 行为。
	// 取值建议：account_pin/account_recent/global。
	LocalityHint string `json:"locality_hint,omitempty"`
}
