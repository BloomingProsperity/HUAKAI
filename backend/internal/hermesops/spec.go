// Package hermesops is the gateway-mediated tool-execution spine for the
// admin-gated Hermes ops assistant (WAVE H3 read-only + WAVE H4 mutating).
//
// It exposes a registry of tools that wrap EXISTING gateway functions so an
// operator (and later the assistant LLM) can run root-cause diagnostics AND
// apply fixes through a single audited endpoint:
//   - READ-ONLY diagnostic tools (H3): MUST NOT mutate state; dispatched via Run.
//   - MUTATING ops tools (H4): replay / pause / resume / renew. Each wraps an
//     existing mutation behind a 5-layer safety contract (RBAC floor, dry-run +
//     confirm, atomic audit, advisory lock, idempotency) and is dispatched only
//     through the confirm-gated mutate path — NEVER through Run.
//
// Design:
//   - ToolSpec declares a tool's identity, category, min required role, and
//     either a Run (read-only) or a Resolve + Mutate pair (mutating).
//   - Registry holds the specs and performs RBAC + dispatch. It fails closed:
//     an unknown tool is denied, a tool whose dependency is unwired returns an
//     error (never a panic), a caller lacking the role is denied, and a mutating
//     tool can never run through the read-only Run path (and vice-versa).
//   - MutateOrchestrator (mutate_tx.go) owns the single transaction that ties
//     the mutation to its hermes_tool_calls + admin_audit_events rows (L3) under
//     a per-target advisory lock (L4).
//   - Privacy: a tool result carries ONLY enums / counts / ids / fingerprints /
//     state-names — never prompts, completions, raw bodies, secrets, PII, or
//     rotated credential material. The persisting layer additionally routes the
//     args and summary through the hermes sanitizer as defense in depth.
package hermesops

import (
	"context"
	"errors"
)

// Tool names. These are the authoritative identifiers; they MUST match the
// hermes_tool_calls.tool_name CHECK list and the hermes.tool.<name> audit
// actions. H4 mutating tools add new names via a DROP+ADD migration.
const (
	ToolCredentialDiagnose    = "credential_diagnose"
	ToolAccountHealthDiagnose = "account_health_diagnose"
	ToolRequestDiagnose       = "request_diagnose"
	ToolDLQInspect            = "dlq_inspect"
	ToolAuditLookup           = "audit_lookup"
	ToolLogAnalyze            = "log_analyze"
	// ToolChannelHealthList(0152 迁移准入)是逐通道健康列表的只读诊断工具,租户级、可按 state 过滤,
	// 补 account_health_diagnose(单账号)缺的"跨账号看哪些通道不健康"。只读 → 仅写 hermes_tool_calls。
	ToolChannelHealthList = "channel_health_list"

	// ToolModelResolveDiagnose(0153 迁移准入)诊断一个公开模型别名在调用方租户内的路由解析:
	// 落到哪些上游池(pool group)、各 binding 的优先级/权重/选号模式/限流/fallback,以及该模型的
	// 能力/上下文窗口/协议族。只读 → 仅写 hermes_tool_calls。投影 safe-by-construction:绝不露 binding
	// 上的 SystemPrompt/SensitiveWords/ParamOverride 等自由文本运营配置(只降级为存在标记/计数)。
	ToolModelResolveDiagnose = "model_resolve_diagnose"

	// ToolPoolList(0154 迁移准入)列出本租户的 pool group(路由池)及其配置:名称、路由策略版本、
	// top-k/能力默认、各类等待/超时/限流参数、启用状态。补全 model_resolve_diagnose 开的"模型→池→账号"
	// 路由拓扑里"池本身有哪些、怎么配的"一环。只读 → 仅写 hermes_tool_calls。PoolGroup 全是结构化配置、
	// 无自由文本/PII(Name 是运营自取的池标签)。
	ToolPoolList = "pool_list"

	// ToolProviderAccountList(0155 迁移准入)列出本租户的上游 provider account 清册(整租户俯瞰,可按
	// state 过滤),补 account_health_diagnose(单账号)缺的"我有哪些账号、各自启用/健康/凭证/限流状态、
	// 路由权重/容量"。只读 → 仅写 hermes_tool_calls。投影 safe-by-construction:绝不露 Extra(原始 JSON
	// blob)、RateLimitReason(可能夹带上游错误文本)、Tags 值(运营自由标签→只露 count)、ProxyGroupID 文本;
	// 且**绝无凭证/token 明文**(原始凭证存 credentialstore,本行根本不含)。
	ToolProviderAccountList = "provider_account_list"

	// ToolQuotaPolicyList(0156 迁移准入)列出本租户的配额策略(quota policy)及其配置:scope/metric/
	// 窗口/限额/burst/模式/优先级/启用/生效区间。让 Hermes 能回答"我的配额怎么配的、对谁、限多少"。只读 →
	// 仅写 hermes_tool_calls。QuotaPolicy 全是结构化配置;投影排 CreatedByActor/LastModifiedByActor
	// (actor 标识)与 TenantID。
	ToolQuotaPolicyList = "quota_policy_list"

	// ToolAlertRuleList(0157 迁移准入)列出本租户的告警规则(alert rule)及其配置:名称、metric、
	// 比较器/阈值、严重度、窗口/持续/冷却秒、是否邮件通知、filters(运营自定义的过滤标签)、是否启用、
	// 上次触发时间。让 Hermes 能回答"我配了哪些告警、对什么 metric、阈值多少、上次什么时候触发"。只读 →
	// 仅写 hermes_tool_calls。AlertRule 全是结构化配置;Filters 是运营自填的规则过滤标签(规则定义的一部分,
	// 非用户内容);投影排 TenantID。
	ToolAlertRuleList = "alert_rule_list"

	// ToolAlertEventList(0158 迁移准入)列出本租户的告警事件(alert event,可按 state 过滤):规则 id、
	// 状态(firing/resolved/manual_resolved)、观测值/阈值/指标值、dimensions(触发时的规则过滤标签)、
	// 触发/解决时间、是否已发邮件。补 alert_rule_list(规则配置)缺的"实际触发了什么"。让 Hermes 能回答
	// "现在有什么告警在响、最近触发过什么"。只读 → 仅写 hermes_tool_calls。Dimensions 同 AlertRule.Filters
	// 来源(规则过滤标签,运营自填);投影排 TenantID。
	ToolAlertEventList = "alert_event_list"

	// WAVE H4 MUTATING tool names. Each wraps an EXISTING admin mutation behind
	// the 5-layer safety contract (RBAC, dry-run+confirm, atomic audit, advisory
	// lock, idempotency). They are registered with Mutating=true so the read-only
	// dispatch path can never run them.
	ToolDLQReplay     = "dlq_replay"
	ToolAccountPause  = "account_pause"
	ToolAccountResume = "account_resume"
	ToolRenewTrigger  = "renew_trigger"
)

// Roles mirror internal/admin role identifiers. Kept as local constants so this
// package does not import the admin package for two strings (the RBAC check
// itself is performed by the caller via admin.AdminIdentity.CanIssueForTenant,
// which this package never bypasses).
const (
	RolePlatformAdmin  = "platform_admin"
	RoleTenantOperator = "tenant_operator"
)

// Category groups tools for listing/UX. H3 tools are diagnostic reads; H4 adds
// mutating ops tools (the "fix" capability).
type Category string

const (
	CategoryDiagnostic Category = "diagnostic"
	CategoryMutating   Category = "mutating"
)

// ResultStatus is the persisted hermes_tool_calls.result_status enum.
type ResultStatus string

const (
	ResultOK     ResultStatus = "ok"
	ResultError  ResultStatus = "error"
	ResultDenied ResultStatus = "denied"
)

// Sentinel errors. Tools and the registry return these so the HTTP layer can
// map them to status codes without string matching.
var (
	// ErrToolUnknown is returned when a tool name is not registered.
	ErrToolUnknown = errors.New("hermesops: unknown tool")
	// ErrToolForbidden is returned when the caller's role is below the tool's
	// minimum required role. Tenant-scope denial is enforced separately by the
	// caller via CanIssueForTenant and surfaces as a denied row too.
	ErrToolForbidden = errors.New("hermesops: tool forbidden for role")
	// ErrDependencyUnwired is returned by a tool whose underlying read
	// dependency is nil. Tools MUST fail closed with this rather than panic.
	ErrDependencyUnwired = errors.New("hermesops: tool dependency unwired")
	// ErrInvalidArgs is returned for malformed / missing required tool args.
	ErrInvalidArgs = errors.New("hermesops: invalid tool args")
	// ErrNotMutating is returned when a mutate/preview path is asked to run a
	// read-only tool (or vice-versa), so a mutation can never sneak through the
	// read-only dispatch and a read-only tool can never reach the confirm path.
	ErrNotMutating = errors.New("hermesops: tool is not mutating")
	// ErrTargetResolution is returned when a mutating tool cannot resolve its
	// target (missing/foreign tenant, account not found). It is distinct from
	// ErrInvalidArgs so the HTTP layer can map it to 404/403 rather than 400.
	ErrTargetResolution = errors.New("hermesops: target resolution failed")
)

// ToolRequest is the resolved, already-authorized invocation context handed to
// a tool's Run. TenantID is the middleware-derived, scope-checked tenant; the
// HTTP layer guarantees CanIssueForTenant passed before Run is called.
type ToolRequest struct {
	// TenantID is the resolved tenant the tool must scope its reads to. Always
	// > 0 (the HTTP layer rejects a non-positive tenant before dispatch).
	TenantID int64
	// ActorUserID is the tenant user whose ops context the operator acts within.
	ActorUserID int64
	// Role is the operator's admin role (platform_admin / tenant_operator).
	Role string
	// Args is the raw, decoded tool argument map from the request body. A tool
	// reads only the keys it understands and ignores the rest. Never persisted
	// raw — the store sanitizes it.
	Args map[string]any
}

// ToolResult is a tool's structured, sanitized output. Summary holds ONLY
// system-diagnostic enums / counts / ids; it is the body returned to the caller
// and (after a second sanitize pass) persisted to hermes_tool_calls.result_summary.
type ToolResult struct {
	// Summary is the diagnostic payload (enums/counts/ids only).
	Summary map[string]any
	// ErrorClass is an optional non-PII classification when the diagnostic
	// surfaced a problem (e.g. "invalid_grant", "rate_limit_exceeded"). It is a
	// short enum, never a free-form message containing user data.
	ErrorClass string
}

// ArgInt extracts a positive int64 arg, returning ErrInvalidArgs when missing
// or non-positive. JSON decodes numbers as float64, so both float64 and int64
// are accepted.
func ArgInt(args map[string]any, key string) (int64, error) {
	v, ok := args[key]
	if !ok {
		return 0, ErrInvalidArgs
	}
	switch n := v.(type) {
	case float64:
		if n <= 0 || n != float64(int64(n)) {
			return 0, ErrInvalidArgs
		}
		return int64(n), nil
	case int64:
		if n <= 0 {
			return 0, ErrInvalidArgs
		}
		return n, nil
	case int:
		if n <= 0 {
			return 0, ErrInvalidArgs
		}
		return int64(n), nil
	default:
		return 0, ErrInvalidArgs
	}
}

// ArgString extracts a trimmed non-empty string arg (optional). Returns
// ("", false) when absent; ("", false) for a non-string value (caller decides
// whether absence is an error). It never returns user prompt content — callers
// only pull identifier-shaped args (request_id, claim_id, status filters).
func ArgString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// ToolSpec declares one tool (read-only diagnostic OR mutating ops tool).
type ToolSpec struct {
	Name         string
	Category     Category
	Description  string
	ReadOnly     bool
	RequiredRole string
	// Mutating is true for the H4 "fix" tools. A mutating tool MUST set Resolve +
	// Mutate (not Run) and is dispatched only through the confirm-gated mutate
	// path; the read-only Run dispatch refuses it. RequiresConfirmation is true
	// for every mutating tool (dry-run + confirm is mandatory this wave).
	Mutating             bool
	RequiresConfirmation bool
	// InputSchema is a small map describing accepted args (name -> human hint),
	// surfaced by GET /v1/hermes/tools. It is documentation only, not validation.
	InputSchema map[string]string
	// Run wraps the underlying read function(s) for a READ-ONLY tool. It MUST NOT
	// mutate state and MUST return an error (never panic) on a missing dependency.
	// nil for a mutating tool.
	Run func(ctx context.Context, req ToolRequest) (ToolResult, error)
	// Resolve performs the READ-ONLY target resolution + dry-run preview for a
	// MUTATING tool. It validates the target belongs to req.TenantID, reads its
	// current state, and returns a MutationPlan describing the target + the
	// intended change. It is called for BOTH the dry-run preview AND immediately
	// before the real mutation, so the preview can never diverge from the action.
	// It MUST NOT mutate state. nil for a read-only tool.
	Resolve func(ctx context.Context, req ToolRequest) (MutationPlan, error)
	// Mutate executes the actual state change for a MUTATING tool, given the plan
	// produced by Resolve. It is invoked exactly once, only on a confirmed
	// request, while the per-target advisory lock is held. It returns the final
	// post-mutation summary (enums/counts/ids/state-names only — NEVER secrets or
	// rotated credential material). nil for a read-only tool.
	Mutate func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error)
}

// MutationPlan is the resolved, read-only description of a pending mutation: the
// target's identity + current state + the intended change. It is the single
// source of truth shared by the dry-run preview and the real execution (so the
// preview cannot lie about what the action will do). TargetType/TargetID feed
// the admin_audit_events row; Preview is the sanitized "what would change"
// payload returned to the caller on a dry-run.
type MutationPlan struct {
	// TargetType is the admin_audit_events target_type (e.g. "provider_account",
	// "account_credential", "dlq_event"). It MUST be in the migration whitelist.
	TargetType string
	// TargetID is the numeric id of the target row (the advisory-lock key + the
	// audit target_id).
	TargetID int64
	// LockKey is the advisory-lock discriminator serializing concurrent mutations
	// on the SAME target. Keyed on tenant + tool + target so two operators cannot
	// race a pause/replay on one account. Empty => the orchestrator derives a
	// default from (TenantID, ToolName, TargetID).
	LockKey string
	// Preview is the sanitized current-state + intended-change payload returned to
	// the caller on confirm=false. enums/counts/ids/state-names only.
	Preview map[string]any
}
