// Package auth 实现入站 API-key 解析, 外加 F-AUTH-005 上游
// Provider Account 凭证管理 (OAuth refresh、token cache、storm
// 防护、mimicry policy)。
//
// 当前上游凭据合同见 docs/HUAKAI工程设计手册.md §4。
// 当前切片包含基于数据表的入站 API key 鉴权、Antigravity token-provider
// 构件、以及 account-scope storm budget;
// 其它 provider adapter 与更广的 storm scope 留待 Phase E+ 完成。
package auth

import "context"

// TokenProvider 为某个上游 Provider Account 返回有效的 access_token。
// 实现按规格运行 Phase A-H 流水线。
type TokenProvider interface {
	// GetAccessToken 返回已缓存的 token、执行刷新, 或在 Account 处于
	// temp-unsched / disabled 状态时返回 error。
	GetAccessToken(ctx context.Context, tenantID, accountID int64) (string, error)
}

// MimicryEngine 应用账号模式要求的请求变换。当前运行时不读取
// mimicry_policy 表；数据库保留列的运行状态见 docs/HUAKAI工程设计手册.md §17。
type MimicryEngine interface {
	// ApplyToBody 返回变换后的请求 body + 审计属性。
	// 6 步变换: 改写 system + 剥离 cache_control + breakpoints +
	// 混淆 tool name + 注入 metadata user_id + tools[-1] breakpoint。
	ApplyToBody(ctx context.Context, accountID int64, originalBody []byte) (
		transformed []byte, audit MimicryAudit, err error)
}

// MimicryAudit 记录为审计事件行实际应用了哪些内容。
type MimicryAudit struct {
	ComponentsApplied    []string
	MimicryPolicyVersion string
}

// Outcome 按规格 §Phase E + H + storm budget 枚举审计结果。
type Outcome string

const (
	OutcomeCacheHit                  Outcome = "cache_hit"
	OutcomeRefreshLockHeld           Outcome = "refresh_lock_held"
	OutcomeRefreshSucceeded          Outcome = "refresh_succeeded"
	OutcomeRefreshTokenRotated       Outcome = "refresh_token_rotated"
	OutcomeDBVersionConflict         Outcome = "db_version_conflict"
	OutcomeInvalidGrantRaceRecovered Outcome = "invalid_grant_race_recovered"
	OutcomeStormBudgetExhausted      Outcome = "storm_budget_exhausted"
	OutcomeCASLost                   Outcome = "cas_lost"
	OutcomeTokenMalformed            Outcome = "token_malformed"
	OutcomeOAuth401ForceRefresh      Outcome = "oauth_401_force_refresh"
	OutcomePermanentDisable          Outcome = "permanent_disable"
	OutcomeOperatorAttention         Outcome = "operator_attention"
	OutcomeMimicryApplied            Outcome = "mimicry_applied"
)

// 厂商 adapter、跨副本 provider-endpoint/global storm scope 与请求变换策略
// 均由各自运行时接线负责。
