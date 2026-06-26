package invitation

import (
	"errors"
	"time"
)

const (
	CodeLength          = 8
	DefaultMaxUsage     = 1
	DefaultExpiryDays   = 30
	MaxUsageLimit       = 100
	MonthlyTenantQuota  = 100
	MaxGenerateAttempts = 64

	// SelfReferralIdempotencyPrefix 在 client_idempotency_key 中标记用户那唯一
	// 且稳定的自助推荐码。它仅由服务端设置：活动 Generate 路径会拒绝任何带此
	// 前缀的调用方提供的键（validateGenerateParams），因此用户无法伪造该标记
	// 来规避每月活动配额。自荐行既免配额，也被排除在活动配额计数器之外
	//（CountTenantInvitationsSince）。
	SelfReferralIdempotencyPrefix = "self:"
	// selfReferralExpiryYears 让个人推荐码实际上永久有效
	//（它是身份，而非有时限的活动码）。
	selfReferralExpiryYears = 100
)

var (
	ErrInvalidInput               = errors.New("invitation: invalid input")
	ErrInvitationExpiresOverLimit = errors.New("invitation: expires_in_days over limit")
	ErrQuotaExceeded              = errors.New("invitation: monthly quota exceeded")
	ErrDuplicateCode              = errors.New("invitation: duplicate code")
	ErrNotFound                   = errors.New("invitation: not found")
	ErrExpired                    = errors.New("invitation: expired")
	ErrExhausted                  = errors.New("invitation: exhausted")
	ErrStoreNotConfigured         = errors.New("invitation: store not configured")
	// ErrReservedIdempotencyKey 拒绝与服务端保留的自荐前缀冲突的、由调用方
	// 提供的 client_idempotency_key。
	ErrReservedIdempotencyKey = errors.New("invitation: reserved idempotency key prefix")
)

type Invitation struct {
	ID            int64      `json:"id"`
	TenantID      int64      `json:"tenant_id"`
	Code          string     `json:"code"`
	InviterUserID int64      `json:"inviter_user_id"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	UsageCount    int        `json:"usage_count"`
	MaxUsage      int        `json:"max_usage"`
}

type GenerateInvitationParams struct {
	TenantID             int64
	InviterUserID        int64
	MaxUsage             int
	ExpiresInDays        int
	ClientIdempotencyKey *string
	Now                  time.Time
}

type GenerateInvitationOutput struct {
	Code          string    `json:"code"`
	InviterUserID int64     `json:"inviter_user_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	MaxUsage      int       `json:"max_usage"`
}

type InvitationPreview struct {
	InviterUserID int64      `json:"inviter_user_id"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	UsageCount    int        `json:"usage_count"`
	MaxUsage      int        `json:"max_usage"`
}

type ReferralSummary struct {
	QualifiedCount     int64 `json:"qualified_count"`
	RewardedCount      int64 `json:"rewarded_count"`
	RewardsEarnedCents int64 `json:"rewards_earned_cents"`
}

type generateRecord struct {
	TenantID             int64
	InviterUserID        int64
	Code                 string
	CreatedAt            time.Time
	ExpiresAt            time.Time
	MaxUsage             int
	ClientIdempotencyKey *string
	// QuotaExempt 在 store 插入时跳过每月活动配额的复检。仅由自荐的
	// get-or-create 路径设置；活动 Generate 路径保持其为 false，使上限在那里
	// 仍然生效。
	QuotaExempt bool
}
