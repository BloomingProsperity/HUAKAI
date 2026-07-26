// Package tenantadmin 实现部署者管理下级租户生命周期的唯一业务合同。
package tenantadmin

import (
	"errors"
	"time"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusDeleted  = "deleted"
)

var (
	ErrNotConfigured     = errors.New("tenantadmin: service not configured")
	ErrInvalidInput      = errors.New("tenantadmin: invalid input")
	ErrNotFound          = errors.New("tenantadmin: tenant not found")
	ErrConflict          = errors.New("tenantadmin: resource conflict")
	ErrVersionConflict   = errors.New("tenantadmin: version conflict")
	ErrPlatformTenant    = errors.New("tenantadmin: platform tenant is protected")
	ErrDeleteBlocked     = errors.New("tenantadmin: tenant deletion is blocked")
	ErrImpactChanged     = errors.New("tenantadmin: deletion impact changed")
	ErrInvalidTransition = errors.New("tenantadmin: invalid lifecycle transition")
)

type Tenant struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	Version         int64      `json:"version"`
	StatusReason    string     `json:"status_reason,omitempty"`
	StatusChangedAt time.Time  `json:"status_changed_at"`
	StatusChangedBy string     `json:"status_changed_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	IsPlatform      bool       `json:"is_platform"`
}

type AuditInput struct {
	ActorID   string
	ActorRole string
	RequestID string
	Reason    string
}

type CreateInput struct {
	Name             string
	AdminEmail       string
	AdminDisplayName string
	AdminPassword    string
	Audit            AuditInput
}

type CreateResult struct {
	Tenant       Tenant `json:"tenant"`
	FirstAdminID int64  `json:"first_admin_user_id"`
}

type StatusInput struct {
	TenantID        int64
	Status          string
	ExpectedVersion int64
	Audit           AuditInput
}

type StatusResult struct {
	Tenant          Tenant `json:"tenant"`
	Changed         bool   `json:"changed"`
	SessionsRevoked int64  `json:"sessions_revoked"`
}

type ResourceCounts struct {
	TenantAdmins       int64 `json:"tenant_admins"`
	FinalUsers         int64 `json:"final_users"`
	APIKeys            int64 `json:"api_keys"`
	ProviderAccounts   int64 `json:"provider_accounts"`
	AccountCredentials int64 `json:"account_credentials"`
	PoolGroups         int64 `json:"pool_groups"`
	Proxies            int64 `json:"proxies"`
	SessionFamilies    int64 `json:"session_families"`
}

type BlockerCounts struct {
	UserBalanceRows         int64 `json:"user_balance_rows"`
	ReservingClaims         int64 `json:"reserving_claims"`
	HeldBalances            int64 `json:"held_balances"`
	PoolSlots               int64 `json:"pool_slots"`
	QuotaReservations       int64 `json:"quota_reservations"`
	QuotaSlots              int64 `json:"quota_slots"`
	SettlementIntents       int64 `json:"settlement_intents"`
	MediaTasks              int64 `json:"media_tasks"`
	MediaOrphans            int64 `json:"media_orphans"`
	QuotaReconciliationJobs int64 `json:"quota_reconciliation_jobs"`
	UsageDLQ                int64 `json:"usage_dlq"`
	SignupRewardRecoveries  int64 `json:"signup_reward_recoveries"`
	PaymentOrders           int64 `json:"payment_orders"`
	RechargeOrders          int64 `json:"recharge_orders"`
	RefundRequests          int64 `json:"refund_requests"`
	CostDisputes            int64 `json:"cost_disputes"`
}

type DeleteImpact struct {
	TenantID            int64          `json:"tenant_id"`
	TenantVersion       int64          `json:"tenant_version"`
	TenantStatus        string         `json:"tenant_status"`
	TenantWalletBalance string         `json:"tenant_wallet_balance"`
	Resources           ResourceCounts `json:"resources"`
	Blockers            BlockerCounts  `json:"blockers"`
	Blocked             bool           `json:"blocked"`
	ImpactHash          string         `json:"impact_hash"`
}

type DeleteInput struct {
	TenantID        int64
	ExpectedVersion int64
	ImpactHash      string
	Audit           AuditInput
}

type DeleteResult struct {
	Tenant          Tenant `json:"tenant"`
	SessionsRevoked int64  `json:"sessions_revoked"`
}
