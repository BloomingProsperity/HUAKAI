package quota

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// PGStore 是 quota 的 PostgreSQL 存储端口。
//
// Slice A 只定义 HUAKAI 自有接口; sqlc 生成包和 pg_store 实现待 Slice B
// 接入, 避免本切片引用尚未生成的 internal/db/quota 类型。
type PGStore interface {
	ListActivePolicies(ctx context.Context, filter PolicyFilter) ([]Policy, error)
	UpsertWindow(ctx context.Context, input WindowUpsert) (WindowCounter, error)
	GetWindowForUpdate(ctx context.Context, tenantID int64, windowID int64) (WindowCounter, error)
	IncrementWindowRequestCount(ctx context.Context, input WindowRequestCount) (WindowCounter, error)
	IncrementWindowReserved(ctx context.Context, input WindowReserve) (WindowCounter, error)
	ApplyWindowSettlement(ctx context.Context, input WindowSettlement) (WindowCounter, error)

	GetReservationByClaimForUpdate(ctx context.Context, tenantID int64, claimID int64) (Reservation, error)
	InsertReservation(ctx context.Context, input ReservationInsert) (Reservation, error)
	ReactivateReservation(ctx context.Context, input ReservationReactivate) (Reservation, error)
	SettleReservation(ctx context.Context, settlement Settlement) error
	ReleaseReservation(ctx context.Context, input ReservationRelease) error
	MarkReservationReconciliationNeeded(ctx context.Context, tenantID int64, reservationID int64, claimID int64) error

	AcquireConcurrencySlot(ctx context.Context, input ConcurrencyAcquire) (ConcurrencySlot, error)
	ReleaseConcurrencySlots(ctx context.Context, tenantID int64, reservationID int64, reason string) error
	ExpireConcurrencySlots(ctx context.Context, tenantID int64, at time.Time) error

	InsertAuditEvent(ctx context.Context, event AuditEvent) (int64, error)
	EnqueueReconciliationJob(ctx context.Context, input ReconciliationEnqueue) (ReconciliationJob, error)
	ListDueReconciliationJobs(ctx context.Context, tenantID int64, at time.Time, limit int) ([]ReconciliationJob, error)
	// ListTenantsWithDueReconciliationJobs 返回有到期 job 的 distinct 租户,供跨租户全局 sweep worker 使用。
	ListTenantsWithDueReconciliationJobs(ctx context.Context, at time.Time, tenantLimit int) ([]int64, error)
	MarkReconciliationJobRunning(ctx context.Context, tenantID int64, jobID int64) error
	CompleteReconciliationJob(ctx context.Context, tenantID int64, jobID int64) error
	FailReconciliationJob(ctx context.Context, input ReconciliationFailure) error
	// ListStaleReservedReservations 返回 lease 已过期仍未终态、claim 已终态、且无补偿 job 史的
	// 孤儿预留(跨租户),供清扫器兜住「billing 终态后补偿 job 从未入队」的崩溃窗口。
	ListStaleReservedReservations(ctx context.Context, at time.Time, limit int) ([]StaleReservation, error)
	// GetClaimTerminalState 点查 billing claim 现状,供补偿动作执行前复核与取实结额。
	GetClaimTerminalState(ctx context.Context, tenantID, claimID int64) (ClaimTerminalState, error)
}

type ProgressReadStore interface {
	ListCurrentWindowsForScope(ctx context.Context, tenantID int64, scopeKind ScopeKind, scopeID string, at time.Time) ([]CurrentWindowRead, error)
}

type PolicyFilter struct {
	TenantID  int64
	Scopes    []Scope
	Metrics   []Metric
	At        time.Time
	ForUpdate bool
}

type WindowUpsert struct {
	TenantID int64
	PolicyID int64
	Window   Window
}

type WindowReserve struct {
	TenantID          int64
	WindowID          int64
	ReserveDelta      decimal.Decimal
	RequestCountDelta int64
	LimitValue        decimal.Decimal
}

type WindowRequestCount struct {
	TenantID          int64
	WindowID          int64
	RequestCountDelta int64
	LimitValue        decimal.Decimal
}

type WindowSettlement struct {
	TenantID             int64
	WindowID             int64
	ReservedReleaseValue decimal.Decimal
	SettledAddValue      decimal.Decimal
	OverageAddValue      decimal.Decimal
}

type ReservationInsert struct {
	TenantID           int64
	ClaimID            int64
	RequestFingerprint string
	Scopes             []Scope
	PolicySnapshot     []byte
	PredictedCost      decimal.Decimal
	ReservedUnits      decimal.Decimal
	LeaseExpiresAt     time.Time
}

type ReservationReactivate struct {
	TenantID           int64
	ReservationID      int64
	ClaimID            int64
	RequestFingerprint string
	Scopes             []Scope
	PolicySnapshot     []byte
	PredictedCost      decimal.Decimal
	ReservedUnits      decimal.Decimal
	LeaseExpiresAt     time.Time
}

type ReservationRelease struct {
	TenantID      int64
	ReservationID int64
	ClaimID       int64
	Reason        string
}

type ConcurrencyAcquire struct {
	TenantID       int64
	ReservationID  int64
	ClaimID        int64
	Scope          Scope
	SlotLimit      int64
	At             time.Time
	LeaseExpiresAt time.Time
}

type ReconciliationEnqueue struct {
	TenantID      int64
	ClaimID       int64
	ReservationID *int64
	Kind          string
	LastError     *string
	NextRunAt     time.Time
}

type ReconciliationFailure struct {
	TenantID  int64
	JobID     int64
	LastError string
	NextRunAt time.Time
}
