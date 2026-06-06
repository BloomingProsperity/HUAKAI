// HUAKAI · iKun

package subscription

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Store 订阅持久化抽象。
// 核心不变量: AssignSubscription 在一个事务内完成 (建 user_subscription + 装 quota 策略 +
// 升级 users.user_group + 记 policy_links + 审计), 要么全成要么全回滚, 不留半权益。
// 配额上限不在订阅行内计数, 而是写入 internal/quota 的 quota_policies 表 (quota 引擎只读解析);
// 日历窗口由 quota 引擎自动重置, 故无周期重置接口, 仅有到期扫描/到期处理。
type Store interface {
	// --- plan 目录 ---
	CreatePlan(ctx context.Context, rec createPlanRecord) (Plan, error)
	GetPlan(ctx context.Context, tenantID, planID int64) (Plan, error)
	ListPlans(ctx context.Context, tenantID int64, onlyForSale bool) ([]Plan, error)
	UpdatePlan(ctx context.Context, rec updatePlanRecord) (Plan, error)
	DisablePlan(ctx context.Context, tenantID, planID int64) error

	// --- 订阅实例 ---
	// AssignSubscription: 幂等授予 (同租户/用户/granted_group 已有 active → 返回现有, Idempotent=true)。
	// 否则建 active 订阅 + 装窗口化 quota 策略 + 升 users.user_group (记 prev) + 审计, 单事务。
	AssignSubscription(ctx context.Context, rec assignRecord) (AssignResult, error)
	GetSubscription(ctx context.Context, tenantID, subscriptionID int64) (UserSubscription, error)
	ListUserSubscriptions(ctx context.Context, tenantID, userID int64) ([]UserSubscription, error)
	SetAutoRenew(ctx context.Context, tenantID, userID int64, autoRenew bool) (UserSubscription, error)
	// CancelSubscription: 标 cancelled + 关闭 quota 策略 + 降级 users.user_group (受 downgrade 守卫), 单事务。
	CancelSubscription(ctx context.Context, rec lifecycleRecord) (UserSubscription, error)
	// ExtendSubscription: active/non-expired 订阅延长 expires_at + 同步 quota policy valid_until + 审计, 单事务。
	ExtendSubscription(ctx context.Context, rec extendRecord) (UserSubscription, error)
	// ResetQuota: 关闭旧 subscription-owned quota policies 并按订阅快照重装, 清空当前 quota_windows 消耗。
	ResetQuota(ctx context.Context, rec lifecycleRecord) (UserSubscription, error)
	// RevokeSubscription: 标 revoked + 关闭 quota 策略 + 降级 users.user_group (受 downgrade 守卫), 单事务。
	RevokeSubscription(ctx context.Context, rec revokeRecord) (UserSubscription, error)

	// --- worker ---
	// ListDueExpiry: 扫到点的 active 订阅 (tenant 内, 限量, 供 worker 批处理)。
	ListDueExpiry(ctx context.Context, now time.Time, limit int) ([]UserSubscription, error)
	// ExpireSubscription: 标 expired + 关 quota 策略 + 降级 (downgrade 守卫: 无更新 active 升级订阅才降), 单事务, 幂等。
	ExpireSubscription(ctx context.Context, rec lifecycleRecord) (UserSubscription, error)

	// --- 到期提醒 (P3b-1) ---
	// ListDueReminder: active 且 expires_at 在 (now, now+within] 且 (expires_at, id) > 游标 的订阅,
	// 按 (expires_at, id) 升序限量返回, 附收件邮箱与套餐名 (游标用于一次 tick 翻完整窗口)。
	ListDueReminder(ctx context.Context, now time.Time, within time.Duration, after ReminderCursor, limit int) ([]ReminderCandidate, error)
	// SentReminderKeys: 某订阅已记录的提醒档位集合 (任意 status), 供 worker 跳过已处理档。
	SentReminderKeys(ctx context.Context, tenantID, subscriptionID int64) (map[string]struct{}, error)
	// RecordReminder: 记一条提醒投递结果 (ON CONFLICT DO NOTHING 幂等), 返回是否新插入。
	RecordReminder(ctx context.Context, rec reminderRecord) (bool, error)

	// --- 审计 ---
	ListAuditEvents(ctx context.Context, tenantID, subscriptionID int64) ([]AuditEvent, error)
}

type createPlanRecord struct {
	TenantID      int64
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
	Now           time.Time
}

type updatePlanRecord struct {
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
	Now           time.Time
}

// assignRecord 分配订阅的事务输入。
type assignRecord struct {
	TenantID     int64
	UserID       int64
	PlanID       int64
	ActorAdminID int64
	RequestID    string
	Now          time.Time
}

// lifecycleRecord 取消/到期的事务输入 (system 或 admin 触发)。
type lifecycleRecord struct {
	TenantID       int64
	SubscriptionID int64
	ActorKind      string
	ActorID        int64
	RequestID      string
	Now            time.Time
}

type extendRecord struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	RequestID      string
	Days           int
	Until          *time.Time
	Now            time.Time
}

type revokeRecord struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	Reason         string
	RequestID      string
	Now            time.Time
}
