package quota

import (
	"context"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

type quotaQueries interface {
	AcquireQuotaConcurrencySlot(ctx context.Context, arg dbquota.AcquireQuotaConcurrencySlotParams) (dbquota.AcquireQuotaConcurrencySlotRow, error)
	ApplyQuotaWindowSettlement(ctx context.Context, arg dbquota.ApplyQuotaWindowSettlementParams) (dbquota.ApplyQuotaWindowSettlementRow, error)
	CompleteQuotaReconciliationJob(ctx context.Context, arg dbquota.CompleteQuotaReconciliationJobParams) (int64, error)
	EnqueueQuotaReconciliationJob(ctx context.Context, arg dbquota.EnqueueQuotaReconciliationJobParams) (dbquota.EnqueueQuotaReconciliationJobRow, error)
	ExpireQuotaConcurrencySlots(ctx context.Context, arg dbquota.ExpireQuotaConcurrencySlotsParams) (int64, error)
	FailQuotaReconciliationJob(ctx context.Context, arg dbquota.FailQuotaReconciliationJobParams) (int64, error)
	GetQuotaReservationByClaimForUpdate(ctx context.Context, arg dbquota.GetQuotaReservationByClaimForUpdateParams) (dbquota.GetQuotaReservationByClaimForUpdateRow, error)
	GetQuotaWindowForUpdate(ctx context.Context, arg dbquota.GetQuotaWindowForUpdateParams) (dbquota.GetQuotaWindowForUpdateRow, error)
	IncrementQuotaWindowRequestCount(ctx context.Context, arg dbquota.IncrementQuotaWindowRequestCountParams) (dbquota.IncrementQuotaWindowRequestCountRow, error)
	IncrementQuotaWindowReserved(ctx context.Context, arg dbquota.IncrementQuotaWindowReservedParams) (dbquota.IncrementQuotaWindowReservedRow, error)
	InsertQuotaAuditEvent(ctx context.Context, arg dbquota.InsertQuotaAuditEventParams) (dbquota.InsertQuotaAuditEventRow, error)
	InsertQuotaReservation(ctx context.Context, arg dbquota.InsertQuotaReservationParams) (dbquota.InsertQuotaReservationRow, error)
	ListActiveQuotaPoliciesForScopes(ctx context.Context, arg dbquota.ListActiveQuotaPoliciesForScopesParams) ([]dbquota.ListActiveQuotaPoliciesForScopesRow, error)
	ListCurrentQuotaWindowsForScope(ctx context.Context, arg dbquota.ListCurrentQuotaWindowsForScopeParams) ([]dbquota.ListCurrentQuotaWindowsForScopeRow, error)
	ListDueQuotaReconciliationJobs(ctx context.Context, arg dbquota.ListDueQuotaReconciliationJobsParams) ([]dbquota.ListDueQuotaReconciliationJobsRow, error)
	ListTenantsWithDueQuotaReconciliationJobs(ctx context.Context, arg dbquota.ListTenantsWithDueQuotaReconciliationJobsParams) ([]int64, error)
	MarkQuotaReconciliationJobRunning(ctx context.Context, arg dbquota.MarkQuotaReconciliationJobRunningParams) (int64, error)
	MarkQuotaReservationReconciliationNeeded(ctx context.Context, arg dbquota.MarkQuotaReservationReconciliationNeededParams) (int64, error)
	ReactivateQuotaReservation(ctx context.Context, arg dbquota.ReactivateQuotaReservationParams) (dbquota.ReactivateQuotaReservationRow, error)
	ReleaseQuotaConcurrencySlotsByReservation(ctx context.Context, arg dbquota.ReleaseQuotaConcurrencySlotsByReservationParams) (int64, error)
	ReleaseQuotaReservation(ctx context.Context, arg dbquota.ReleaseQuotaReservationParams) (int64, error)
	SettleQuotaReservation(ctx context.Context, arg dbquota.SettleQuotaReservationParams) (int64, error)
	UpsertQuotaWindow(ctx context.Context, arg dbquota.UpsertQuotaWindowParams) (dbquota.UpsertQuotaWindowRow, error)
}
