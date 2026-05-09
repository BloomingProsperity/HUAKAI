// Package pool implements F-POOL-001: Provider Account Pool Selection.
//
// See docs/specs/pool-routing.md for the released spec.
// Current slice includes the L0 DefaultSelector, PostgreSQL account source,
// DB slot acquisition, and Pattern B claim writeback. Redis sticky state and
// scheduler-driven queueing remain Phase E+ work.
package pool

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// VendorFromProtocolFamily 派生 vendor 字面量 (D2: dispatcher metric vendor
// 维度用)。 ProtocolFamily 字面量目前已知: anthropic_messages / openai_chat /
// openai_responses / gemini_messages / bedrock_invoke (Bedrock 走 anthropic
// 翻译, 但底层 vendor 是 bedrock; Owner 无 AWS 凭据测试范围外)。
//
// 真实账号测试限定 4 vendor (memory: project_real_vendor_account_scope):
// anthropic / openai / gemini / codex。 codex 暂时通过 OpenAI 兼容协议
// 接入, 未来加独立 codex_complete ProtocolFamily 时本 helper 同步更新。
//
// 不在集合内 → 返空字符串, dispatcher metric 静默不记 vendor 维度。
func VendorFromProtocolFamily(pf string) string {
	switch {
	case strings.HasPrefix(pf, "anthropic"):
		return "anthropic"
	case strings.HasPrefix(pf, "openai"):
		return "openai"
	case strings.HasPrefix(pf, "gemini"):
		return "gemini"
	case strings.HasPrefix(pf, "codex"):
		return "codex"
	default:
		// bedrock_invoke / 其他 vendor: 静默返空, 不进 4-vendor 切片 metric
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
