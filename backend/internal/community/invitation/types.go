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

	// SelfReferralIdempotencyPrefix marks a user's single stable self-service
	// referral code in client_idempotency_key. It is server-set only: the
	// campaign Generate path rejects any caller-supplied key with this prefix
	// (validateGenerateParams), so a user cannot forge the marker to dodge the
	// monthly campaign quota. Self-coded rows are quota-exempt AND excluded from
	// the campaign quota counter (CountTenantInvitationsSince).
	SelfReferralIdempotencyPrefix = "self:"
	// selfReferralExpiryYears keeps a personal referral code effectively
	// permanent (it is identity, not a time-boxed campaign code).
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
	// ErrReservedIdempotencyKey rejects a caller-supplied client_idempotency_key
	// that collides with the server-reserved self-referral prefix.
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
	// QuotaExempt skips the monthly campaign quota recheck inside the store
	// insert. Set only by the self-referral get-or-create path; the campaign
	// Generate path leaves it false so the cap still applies there.
	QuotaExempt bool
}
