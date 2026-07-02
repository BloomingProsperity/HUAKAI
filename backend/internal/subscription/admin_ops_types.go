// HUAKAI · iKun

package subscription

import (
	"time"

	"github.com/shopspring/decimal"
)

// UpdatePlanInput 替换某个套餐目录行中由 admin 拥有的可变字段。
// 它不会改写已经发放的订阅快照。
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
	ActorRef      string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID     string
}

// ExtendSubscriptionInput 延长一个处于活跃、未过期状态的 admin 指派订阅。
// Days 与 Until 必须且只能设置其中之一。
type ExtendSubscriptionInput struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID      string
	Days           int
	Until          *time.Time
}

// ResetQuotaInput 根据订阅快照重建该订阅拥有的活跃 quota 策略,
// 并清空当前 quota_windows 的消耗。
type ResetQuotaInput struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID      string
}

// ChangePlanInput 把一个活跃订阅切换到另一个套餐快照。
// SubscriptionID 与 UserID 必须且只能设置其中之一: admin 调用针对具体的指派
// id, 而 self-service 调用按 user id 针对调用方当前的活跃订阅。
type ChangePlanInput struct {
	TenantID       int64
	SubscriptionID int64
	UserID         int64
	NewPlanID      int64
	AllowDowngrade bool
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID      string
}

// BulkAssignInput 把同一个套餐发放给多个用户, 并收集每个用户各自的 error。
type BulkAssignInput struct {
	TenantID     int64
	UserIDs      []int64
	PlanID       int64
	ActorAdminID int64
	ActorRef     string // 双身份归属串(AuditActor() 形态),空则列落 NULL
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

// RevokeSubscriptionInput 硬性终止一个 admin 指派的订阅。它有别于
// cancel-renew, 也有别于既有的 cancelled 生命周期状态。
type RevokeSubscriptionInput struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	Reason         string
	RequestID      string
}
