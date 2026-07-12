// Package rate 实现 F-RATE-001:上游限流 + 冷却(cooldown)。
//
// 已发布的规格见 docs/specs/rate-limiting.md。
// 本包定义限流/冷却契约,以及各 provider 专属的分类器接口面。
package rate

import (
	"context"
	"net/http"
	"time"
)

// Service 运行有序的上游错误决策树。
type Service interface {
	// HandleUpstreamError 应用分层决策树:
	// custom-codes/temp-unsched 规则 → pool-mode 未匹配短路 → 状态码分支。
	HandleUpstreamError(ctx context.Context, accountID int64, statusCode int,
		respHeaders http.Header, respBody []byte) (Decision, error)

	// ClearCascade 原子地清除所有冷却状态:
	// rate_limit、overload、temp_unsched、model_rate_limits、openai_403_counter。
	ClearCascade(ctx context.Context, accountID int64, actorID string) error

	// UpdateSessionWindow 应用恢复信号(recovery-signal)的处理逻辑。
	UpdateSessionWindow(ctx context.Context, accountID int64, headers http.Header) error
}

// Decision 是 HandleUpstreamError 的结果。
type Decision struct {
	StateChange       StateChange
	CooldownUntil     time.Time
	Reason            Reason
	ShouldFailover    bool
	RetryAfterSeconds int
	// SuppressLocalState 只由 pool_mode 的未匹配分支设置；请求仍可故障转移，但不得写账号或模型健康状态。
	SuppressLocalState bool
}

// StateChange 对 Account 状态变更进行分类。
type StateChange int

const (
	StateNoChange StateChange = iota
	StateRateLimited
	StateOverloaded
	StateTempUnsched
	StateModelRateLimited
	StatePermanentDisable
)

// Reason 是规格 §Failure Path 中定义的结构化 rate_limit_reason 枚举。
type Reason string

const (
	ReasonRateLimit5h           Reason = "rate_limit_5h_exceeded"
	ReasonRateLimit7d           Reason = "rate_limit_7d_exceeded"
	ReasonRateLimitBoth         Reason = "rate_limit_both_windows"
	ReasonRateLimitRPM          Reason = "rate_limit_rpm"
	ReasonRateLimitTPM          Reason = "rate_limit_tpm"
	ReasonExtraUsageRequired    Reason = "extra_usage_required"
	ReasonOverloaded            Reason = "overloaded"
	ReasonUpstreamTransient     Reason = "upstream_transient_error"
	ReasonTokenRefreshRequired  Reason = "token_refresh_required"
	ReasonTokenRevoked          Reason = "token_permanently_revoked"
	ReasonKYCRequired           Reason = "kyc_required"
	ReasonOrgDisabled           Reason = "org_disabled"
	ReasonCreditExhausted       Reason = "credit_exhausted"
	ReasonWorkspaceDeactivated  Reason = "workspace_deactivated"
	ReasonModelLimitExceeded    Reason = "model_limit_exceeded"
	ReasonTempUnschedRule       Reason = "temp_unsched_rule_matched"
	ReasonOpenAI403Counted      Reason = "openai_403_counted"
	ReasonOpenAI403Disabled     Reason = "openai_403_disabled"
	ReasonAntigravityValidation Reason = "antigravity_403_validation"
	ReasonCustomErrorCode       Reason = "custom_error_code"
)
