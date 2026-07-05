package quota

import (
	"time"

	"github.com/shopspring/decimal"
)

// ScopeKind 表示配额作用域类型。provider account 在 Slice A 只作为只读策略输入。
type ScopeKind string

const (
	ScopeGlobal          ScopeKind = "global"
	ScopeUser            ScopeKind = "user"
	ScopeAPIKey          ScopeKind = "api_key"
	ScopeChannel         ScopeKind = "channel"
	ScopePoolGroup       ScopeKind = "pool_group"
	ScopeProviderAccount ScopeKind = "provider_account"
)

// Scope 是租户内的配额作用域。ID 使用 HUAKAI 中性编码, global 固定为 "*"。
type Scope struct {
	TenantID int64
	Kind     ScopeKind
	ID       string
}

// Metric 表示配额度量。money 维度对应 PostgreSQL numeric(20,8)。
type Metric string

const (
	MetricRequests        Metric = "requests"
	MetricTokensEstimated Metric = "tokens_estimated"
	MetricCostUSD         Metric = "cost_usd"
	MetricConcurrency     Metric = "concurrency"
)

// WindowKind 表示窗口计算方式; 具体窗口边界计算留到 Slice B。
type WindowKind string

const (
	WindowNone          WindowKind = "none"
	WindowFixed         WindowKind = "fixed"
	WindowCalendarDay   WindowKind = "calendar_day"
	WindowCalendarWeek  WindowKind = "calendar_week"
	WindowCalendarMonth WindowKind = "calendar_month"
	WindowManual        WindowKind = "manual"
)

// Window 是一个已解析的配额窗口。
type Window struct {
	Kind    WindowKind
	Seconds int64
	Start   time.Time
	End     time.Time
}

// Mode 控制策略是强制拒绝、只观察, 还是手动优先。
type Mode string

const (
	ModeEnforce     Mode = "enforce"
	ModeObserve     Mode = "observe"
	ModeManualFirst Mode = "manual_first"
	ModeDisabled    Mode = "disabled"
)

// DecisionKind 是 reserve 阶段返回给调用方的决策类别。
type DecisionKind string

const (
	DecisionAllow                  DecisionKind = "allow"
	DecisionDeny                   DecisionKind = "deny"
	DecisionObserveOnly            DecisionKind = "observe_only"
	DecisionRequiresReconciliation DecisionKind = "requires_reconciliation"
)

// Decision 汇总一次配额检查结果。Code 是稳定的 HUAKAI 客户端/审计码。
type Decision struct {
	Kind       DecisionKind
	Code       string
	Reason     string
	RetryAfter time.Duration
	Scope      Scope
	Metric     Metric
	Amount     decimal.Decimal
	// WindowKind 标明本次拒绝命中的是哪个时间窗口(calendar_day/week/month/fixed/manual)。
	// 仅在拒绝决策上有意义,供拒绝响应透出"是日额还是月额超了";零值(WindowNone/空)表示无固定
	// 窗口或未知,调用方据此决定不透出窗口名,保持对未配多窗口租户的零行为变化。
	WindowKind WindowKind
}

// ReservationStatus 是 reservation ledger 的生命周期状态。
type ReservationStatus string

const (
	ReservationReserved             ReservationStatus = "reserved"
	ReservationSettled              ReservationStatus = "settled"
	ReservationReleased             ReservationStatus = "released"
	ReservationExpired              ReservationStatus = "expired"
	ReservationReconciliationNeeded ReservationStatus = "reconciliation_needed"
)

// Reservation 是 claim 级配额预留, 由 (TenantID, ClaimID) 保证幂等。
type Reservation struct {
	TenantID           int64
	ID                 int64
	ClaimID            int64
	RequestFingerprint string
	Scopes             []Scope
	PolicySnapshot     []byte
	PredictedCost      decimal.Decimal
	ReservedUnits      decimal.Decimal
	Status             ReservationStatus
	LeaseExpiresAt     time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Settlement 表示 billing 已提交后对 quota reservation 的独立结算。
type Settlement struct {
	TenantID      int64
	ReservationID int64
	ClaimID       int64
	ActualCost    decimal.Decimal
	SettledUnits  decimal.Decimal
	OverageUnits  decimal.Decimal
	SettledAt     time.Time
}

// Policy 是 Slice B store/service 会使用的内存策略视图。
type Policy struct {
	TenantID   int64
	ID         int64
	Scope      Scope
	Metric     Metric
	Window     Window
	LimitValue decimal.Decimal
	BurstValue decimal.Decimal
	Mode       Mode
	Priority   int
	ValidFrom  time.Time
	ValidUntil *time.Time
}

// WindowCounter 是 quota_windows 的计数视图。
type WindowCounter struct {
	TenantID      int64
	ID            int64
	PolicyID      int64
	Window        Window
	ReservedValue decimal.Decimal
	SettledValue  decimal.Decimal
	OverageValue  decimal.Decimal
	RequestCount  int64
	Version       int
}

// CurrentWindowRead 是某条 policy 当前窗口的只读 subscription/admin 投影视图。
type CurrentWindowRead struct {
	TenantID      int64
	PolicyID      int64
	WindowID      int64
	Scope         Scope
	Metric        Metric
	Window        Window
	LimitValue    decimal.Decimal
	ReservedValue decimal.Decimal
	SettledValue  decimal.Decimal
	OverageValue  decimal.Decimal
	RequestCount  int64
	Version       int
}

// ConcurrencySlot 是本地 scope 并发槽。provider-account in-flight 不由本包维护。
type ConcurrencySlot struct {
	TenantID       int64
	ID             int64
	ReservationID  int64
	ClaimID        int64
	Scope          Scope
	LeaseExpiresAt time.Time
	ReleasedAt     *time.Time
	Status         string
}

// AuditEvent 是 quota_audit_events 的写入视图。
type AuditEvent struct {
	TenantID          int64
	ReservationID     *int64
	ClaimID           *int64
	EventType         string
	DecisionCode      string
	Scope             Scope
	Metric            Metric
	AmountReserved    decimal.Decimal
	AmountSettled     decimal.Decimal
	RetryAfterSeconds *int
	Payload           []byte
	Actor             *string
}

// ReconciliationJob 表示需要后台补偿的 quota 操作。
type ReconciliationJob struct {
	TenantID      int64
	ID            int64
	ClaimID       int64
	ReservationID *int64
	Kind          string
	Status        string
	AttemptCount  int
	LastError     *string
	NextRunAt     time.Time
}

// StaleReservation 是 lease 已过期仍未终态的预留 + 其 billing claim 终态视图,
// 供清扫器按 claim 终态定向补偿(committed→Settle, aborted→Release)。
// ClaimActualCostSet=false 表示 claim 行 actual_cost 为 NULL(此时用 PredictedCost 兜底)。
type StaleReservation struct {
	TenantID           int64
	ReservationID      int64
	ClaimID            int64
	PredictedCost      decimal.Decimal
	ClaimStatus        string
	ClaimActualCost    decimal.Decimal
	ClaimActualCostSet bool
}
