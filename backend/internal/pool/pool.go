// Package pool implements F-POOL-001: Provider Account Pool Selection.
//
// See docs/specs/pool-routing.md for the released spec.
// Current slice includes the L0 DefaultSelector, PostgreSQL account source,
// DB slot acquisition, and Pattern B claim writeback. Redis sticky state and
// scheduler-driven queueing remain Phase E+ work.
package pool

import (
	"context"

	"github.com/google/uuid"
)

// VendorFromProtocolFamily 把已注册 ProtocolFamily (gateway/protocol_selector.go)
// 映射到 4-vendor 真实账号测试集合 (memory: project_real_vendor_account_scope):
// anthropic / openai / gemini / codex。 用于 dispatcher metric 按 vendor 切片。
//
// 显式 exact-match (避免 prefix 误判, 例如 openai_codex 不能落到 openai 槽):
//   - openai_codex 是 ChatGPT Plus / Codex CLI 反转 session 的 ProtocolFamily,
//     虽底层走 OpenAI Adapter, 但属真实独立 vendor "codex" (Owner 真账号 4
//     vendor 之一), 必须单独切片;
//   - openai_chat / openai_responses → vendor "openai";
//   - gemini_messages / gemini_advanced_session → vendor "gemini";
//   - anthropic_messages → vendor "anthropic"。
//
// 其他 ProtocolFamily (bedrock_invoke / openrouter_chat / grok_chat /
// deepseek_chat / mistral_chat / groqcloud_chat / together_chat /
// perplexity_chat / fireworks_chat / copilot_session / cursor_session /
// antigravity_session / kiro_session / windsurf_session) 不在 4-vendor
// 真实账号集合, 静默返空字符串 → dispatcher metric 不记 vendor 维度。
func VendorFromProtocolFamily(pf string) string {
	switch pf {
	case "anthropic_messages":
		return "anthropic"
	case "openai_chat", "openai_responses":
		return "openai"
	case "openai_codex":
		return "codex"
	case "gemini_messages", "gemini_advanced_session":
		return "gemini"
	default:
		return ""
	}
}

// Selector chooses a Provider Account for a tenant request per the layered
// algorithm in docs/specs/pool-routing.md §Phase A-D.
type Selector interface {
	// Select runs the 5-layer selection (routing config → sticky-within-routing
	// → sticky-standalone → load-aware fresh → fallback queue) plus the
	// Phase C atomic admission with Pattern B writeback.
	Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error)
}

// SelectionRequest carries Phase A candidate intent inputs.
type SelectionRequest struct {
	TenantID         int64
	UserID           int64
	APIKeyID         int64
	PoolGroupID      int64
	RequestedModel   string
	EndpointFamily   string
	CapabilityFlags  []string
	SessionHash      string
	ContinuationKey  string
	ExcludedAccounts map[int64]struct{}
	AttemptSeq       int
	ClaimID          int64

	// Vendor (D2): 来自 ResolvedModel.ProtocolFamily 派生的 vendor 字面量
	// (anthropic / openai / gemini / codex), 用于 dispatcher 按 vendor 切片
	// metric (memory: project_real_vendor_account_scope)。 空字符串时
	// dispatcher 不记 vendor 维度 (退化路径, 与现状等价)。
	Vendor string
}

// SelectionResult is the Phase C output: Provider Account acquired or wait plan.
type SelectionResult struct {
	AccountID         int64
	AcquisitionToken  uuid.UUID
	WaitPlan          *WaitPlan
	RoutingReasonJSON []byte
}

// WaitPlan describes a queued admission attempt per Layer 3 fallback.
type WaitPlan struct {
	AccountID      int64
	MaxConcurrency int
	TimeoutMS      int
	MaxWaiting     int
}

// TODO(phase-e): add Redis-backed sticky state, queue/scheduler integration,
// and Serializable retry loops around slot acquisition conflicts.
