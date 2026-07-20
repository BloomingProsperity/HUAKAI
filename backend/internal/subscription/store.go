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
	ListUserSubscriptionsByGroup(ctx context.Context, tenantID int64, grantedGroup string, limit int) ([]UserSubscription, error)
	SetAutoRenew(ctx context.Context, tenantID, userID int64, autoRenew bool) (UserSubscription, error)
	// CancelSubscription: 标 cancelled + 关闭 quota 策略 + 降级 users.user_group (受 downgrade 守卫), 单事务。
	CancelSubscription(ctx context.Context, rec lifecycleRecord) (UserSubscription, error)
	// ExtendSubscription: active/non-expired 订阅延长 expires_at + 同步 quota policy valid_until + 审计, 单事务。
	ExtendSubscription(ctx context.Context, rec extendRecord) (UserSubscription, error)
	// ResetQuota: 关闭旧 subscription-owned quota policies 并按订阅快照重装, 清空当前 quota_windows 消耗。
	ResetQuota(ctx context.Context, rec lifecycleRecord) (UserSubscription, error)
	// ChangePlan: active/non-expired 订阅原地切换套餐快照 + 关闭旧策略 + 安装新策略 + 审计, 单事务。
	ChangePlan(ctx context.Context, rec changePlanRecord) (UserSubscription, error)
	// RevokeSubscription: 标 revoked + 关闭 quota 策略 + 降级 users.user_group (受 downgrade 守卫), 单事务。
	RevokeSubscription(ctx context.Context, rec revokeRecord) (UserSubscription, error)

	// --- worker ---
	// ListDueExpiry: 扫到点的 active 订阅 (tenant 内, 限量, 供 worker 批处理)。
	ListDueExpiry(ctx context.Context, now time.Time, limit int) ([]UserSubscription, error)
	// ExpireSubscription: 标 expired + 关 quota 策略 + 降级 (downgrade 守卫: 无更新 active 升级订阅才降), 单事务, 幂等。
	ExpireSubscription(ctx context.Context, rec lifecycleRecord) (UserSubscription, error)

	// --- 自动续费 worker (P-AUTORENEW) ---
	// ListAutoRenewDue: 扫 expires_at<=dueCutoff 且 auto_renew=true 的 active 订阅,
	// 按 (expires_at, id) 稳定翻页，避免前一批跳过项长期挡住后续订阅。
	// dueCutoff = now + 提前续费窗口(renew-ahead grace),让续费抢在到期 worker 收割前完成,
	// 且续费失败(余额不足)的订阅照常在 expires_at 到点被 ExpiryWorker 收割, 不留白嫖窗口。
	ListAutoRenewDue(ctx context.Context, dueCutoff time.Time, after AutoRenewCursor, limit int) ([]UserSubscription, error)
	// TryAutoRenewSubscription: 单事务尝试"扣钱包余额 → 续期"。
	//   - 锁订阅行重查仍 active+auto_renew+due (并发/重复防护), 否则零副作用跳过。
	//   - 幂等锚: 同 (订阅, 续费周期) 已扣过 → 跳过, 不重复扣 (worker 重跑安全)。
	//   - 余额 < 续费价 → 跳过, 绝不扣款。余额够 → 扣 user_balances + 记账 + 续期, 三者同事务原子。
	// money fail-safe: 任一步失败回滚整事务, 宁可不续也不会扣了不续 / 续了没扣。
	TryAutoRenewSubscription(ctx context.Context, rec autoRenewRecord) (AutoRenewResult, error)

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
	ActorRef      string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID     string
	Now           time.Time
}

// assignRecord 分配订阅的事务输入。
type assignRecord struct {
	TenantID     int64
	UserID       int64
	PlanID       int64
	ActorAdminID int64
	ActorRef     string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID    string
	Now          time.Time
}

// lifecycleRecord 取消/到期的事务输入 (system 或 admin 触发)。
type lifecycleRecord struct {
	TenantID       int64
	SubscriptionID int64
	ActorKind      string
	ActorID        int64
	ActorRef       string // 同上
	RequestID      string
	Now            time.Time
}

type extendRecord struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID      string
	Days           int
	Until          *time.Time
	Now            time.Time
}

type changePlanRecord struct {
	TenantID       int64
	SubscriptionID int64
	UserID         int64
	NewPlanID      int64
	AllowDowngrade bool
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	RequestID      string
	Now            time.Time
}

type revokeRecord struct {
	TenantID       int64
	SubscriptionID int64
	ActorAdminID   int64
	ActorRef       string // 双身份归属串(AuditActor() 形态),空则列落 NULL
	Reason         string
	RequestID      string
	Now            time.Time
}

// autoRenewRecord 单条自动续费的事务输入 (system 触发, worker 逐条调)。
type autoRenewRecord struct {
	TenantID       int64
	SubscriptionID int64
	Now            time.Time
	// DueCutoff = 批扫时的 now + 提前续费窗口; 锁行后复查 expires_at<=DueCutoff 仍成立才续,
	// 否则(到期日被别的路径推到窗口外)零副作用跳过。与 ListAutoRenewDue 的 cutoff 同源。
	DueCutoff time.Time
}

// AutoRenewResult 单条自动续费的结果。
//   - Renewed=true: 已扣款 (或免费续费) 并续期成功。
//   - Renewed=false: 跳过, SkipReason 说明原因 (余额不足 / 已续过 / 重查不再 due), 零副作用。
type AutoRenewResult struct {
	Subscription UserSubscription
	Renewed      bool
	SkipReason   string
	ChargedCents int64 // 本次扣减的钱包金额 (cents); 免费续费或跳过为 0
}

// 自动续费跳过原因 (AutoRenewResult.SkipReason)。
const (
	AutoRenewSkipNotDue           = "not_due"           // 重查不再 active / 不再 due (并发已被别的路径处理)
	AutoRenewSkipAutoRenewOff     = "auto_renew_off"    // 重查 auto_renew 已被关
	AutoRenewSkipAlreadyRenewed   = "already_renewed"   // 该续费周期已有扣款行 (幂等命中)
	AutoRenewSkipInsufficientFund = "insufficient_fund" // 钱包余额 < 续费价, 绝不扣款
	AutoRenewSkipPlanUnavailable  = "plan_unavailable"  // 套餐已停用 / 不存在, 不续
)
