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
	ValidFrom     time.Time        `json:"valid_from"`
}

type KeyQuotaView = SetKeyQuotaResult

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
