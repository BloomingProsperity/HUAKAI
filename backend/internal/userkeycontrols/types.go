package userkeycontrols

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type SetKeyQuotaRequest struct {
	TenantID      int64
	UserID        int64
	APIKeyID      int64
	LimitUSD      decimal.Decimal
	Metric        quota.Metric
	WindowKind    quota.WindowKind
	WindowSeconds int32
	Mode          quota.Mode
	RequestID     string
}

type SetKeyQuotaResult struct {
	APIKeyID      int64            `json:"api_key_id"`
	PolicyID      int64            `json:"policy_id"`
	LimitUSD      decimal.Decimal  `json:"limit_usd"`
	ScopeKind     quota.ScopeKind  `json:"scope_kind"`
	ScopeID       string           `json:"scope_id"`
	Metric        quota.Metric     `json:"metric"`
	WindowKind    quota.WindowKind `json:"window_kind"`
	WindowSeconds int32            `json:"window_seconds"`
	Mode          quota.Mode       `json:"mode"`
	Priority      int32            `json:"priority"`
	ValidFrom     time.Time        `json:"valid_from"`
}

// KeyQuotaView 是 GetKeyQuota 返回的只读投影。
// UsedUSD 是当前窗口内已结算 + 已预留消耗之和(无窗口时为 0)。
// RemainingUSD 是 LimitUSD - UsedUSD;当 LimitUSD 为零(无限额)时为 nil。
type KeyQuotaView struct {
	APIKeyID      int64            `json:"api_key_id"`
	PolicyID      int64            `json:"policy_id"`
	LimitUSD      decimal.Decimal  `json:"limit_usd"`
	ScopeKind     quota.ScopeKind  `json:"scope_kind"`
	ScopeID       string           `json:"scope_id"`
	Metric        quota.Metric     `json:"metric"`
	WindowKind    quota.WindowKind `json:"window_kind"`
	WindowSeconds int32            `json:"window_seconds"`
	Mode          quota.Mode       `json:"mode"`
	// Priority 是解析时的决胜项:当多个 quota policy 对同一请求重叠时,
	// priority 最低的 policy 胜出(quota/policy.go)。以只读形式暴露,
	// 便于用户看到哪个 policy 优先。
	Priority  int32     `json:"priority"`
	ValidFrom time.Time `json:"valid_from"`
	// KEY-007:新增的用量字段
	UsedUSD      decimal.Decimal  `json:"used_usd"`
	RemainingUSD *decimal.Decimal `json:"remaining_usd,omitempty"`
	// WindowEnd 是当前 quota 窗口的重置边界 —— 即已消耗用量翻滚并被释放的时刻。
	// 取当前各 cost 窗口中最早结束的那个;尚无窗口时为 nil。
	// 与更宽泛的自助 /quota 视图所用的绝对时间戳形式一致。
	WindowEnd *time.Time `json:"window_end,omitempty"`
}

type SetKeyGroupRequest struct {
	TenantID  int64
	UserID    int64
	APIKeyID  int64
	GroupID   *int64
	RequestID string
}

type SetKeyGroupResult struct {
	APIKeyID         int64  `json:"api_key_id"`
	GroupID          *int64 `json:"group_id,omitempty"`
	GroupName        string `json:"group_name,omitempty"`
	GroupDescription string `json:"group_description,omitempty"`
	GroupEnabled     *bool  `json:"group_enabled,omitempty"`
}

type KeyGroupView = SetKeyGroupResult

type SetKeyIPAllowlistRequest struct {
	TenantID    int64
	UserID      int64
	APIKeyID    int64
	IPAllowlist []string
	RequestID   string
}

type SetKeyIPAllowlistResult struct {
	APIKeyID    int64    `json:"api_key_id"`
	IPAllowlist []string `json:"ip_allowlist"`
}

type KeyIPAllowlistView = SetKeyIPAllowlistResult

// KEY-016:IP 黑名单类型(与白名单平行)
type SetKeyIPBlacklistRequest struct {
	TenantID    int64
	UserID      int64
	APIKeyID    int64
	IPBlacklist []string
	RequestID   string
}

type SetKeyIPBlacklistResult struct {
	APIKeyID    int64    `json:"api_key_id"`
	IPBlacklist []string `json:"ip_blacklist"`
}

type KeyIPBlacklistView = SetKeyIPBlacklistResult

type SetKeyModelAllowlistRequest struct {
	TenantID      int64
	UserID        int64
	APIKeyID      int64
	AllowedModels []string
	RequestID     string
}

type SetKeyModelAllowlistResult struct {
	APIKeyID      int64    `json:"api_key_id"`
	AllowedModels []string `json:"allowed_models"`
}

type KeyModelAllowlistView = SetKeyModelAllowlistResult
