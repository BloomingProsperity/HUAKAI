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

// KeyQuotaView is the read projection returned by GetKeyQuota.
// UsedUSD is settled + reserved consumed in the current window (0 when no window exists).
// RemainingUSD is LimitUSD - UsedUSD; nil when LimitUSD is zero (unlimited).
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
	// Priority is the resolution tiebreaker: when several quota policies overlap a
	// request, the lowest-priority policy wins (quota/policy.go). Surfaced read-only so
	// the user can see which policy takes precedence.
	Priority  int32     `json:"priority"`
	ValidFrom time.Time `json:"valid_from"`
	// KEY-007: additive usage fields
	UsedUSD      decimal.Decimal  `json:"used_usd"`
	RemainingUSD *decimal.Decimal `json:"remaining_usd,omitempty"`
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

// KEY-016: IP blacklist types (parallel to allowlist)
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
