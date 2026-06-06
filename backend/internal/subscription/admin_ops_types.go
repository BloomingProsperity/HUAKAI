// HUAKAI · iKun

package subscription

import (
	"time"

	"github.com/shopspring/decimal"
)

// UpdatePlanInput replaces the mutable admin-owned fields of a plan catalog row.
// It does not rewrite already-granted subscription snapshots.
type UpdatePlanInput struct {
	TenantID      int64
	PlanID        int64
	Name          string
	Description   string
	PriceCents    int64
	CurrencyCode  string
	ValidityDays  int
	GrantedGroup  string
	DailyCapUSD   *decimal.Decimal
	WeeklyCapUSD  *decimal.Decimal
	MonthlyCapUSD *decimal.Decimal
	ForSale       bool
	SortOrder     int
	ActorAdminID  int64
	RequestID     string
}

// ExtendSubscriptionInput extends one active, non-expired admin assignment.
// Exactly one of Days or Until must be set.
type ExtendSubscriptionInput struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	RequestID      string
	Days           int
	Until          *time.Time
}

// ResetQuotaInput rebuilds the active subscription-owned quota policies from
// the subscription snapshot, clearing current quota_windows consumption.
type ResetQuotaInput struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	RequestID      string
}

// BulkAssignInput grants the same plan to many users, collecting per-user errors.
type BulkAssignInput struct {
	TenantID     int64
	UserIDs      []int64
	PlanID       int64
	ActorAdminID int64
	RequestID    string
}

type BulkAssignResult struct {
	Results []BulkAssignUserResult
}

type BulkAssignUserResult struct {
	UserID       int64
	OK           bool
	Error        string
	Subscription UserSubscription
	Idempotent   bool
}

// RevokeSubscriptionInput hard-ends an admin assignment. It is distinct from
// cancel-renew and from the existing cancelled lifecycle state.
type RevokeSubscriptionInput struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	Reason         string
	RequestID      string
}
