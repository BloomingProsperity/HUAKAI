package quota

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

var ErrStoreNotConfigured = errors.New("quota: postgres store not configured")

// PostgresStore 是 PG/sqlc 支持的 quota store 实现。
type PostgresStore struct {
	q       quotaQueries
	beginTx func(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// NewPostgresStore 从支持 BeginTx 的 PG 连接/连接池构造 quota store。
// Service.Reserve 需要 store 自己开启事务; 已有 pgx.Tx 不作为组合事务入口。
func NewPostgresStore(db dbquota.DBTX) *PostgresStore {
	if db == nil {
		return &PostgresStore{}
	}
	store := &PostgresStore{q: dbquota.New(db)}
	if beginner, ok := db.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	}); ok {
		store.beginTx = beginner.BeginTx
	}
	return store
}

// NewPGStore 保留短命名构造器, 方便 wiring 侧按 PGStore 接口注入。
func NewPGStore(db dbquota.DBTX) *PostgresStore {
	return NewPostgresStore(db)
}

func (s *PostgresStore) queries() (quotaQueries, error) {
	if s == nil || s.q == nil {
		return nil, ErrStoreNotConfigured
	}
	return s.q, nil
}

// WithTx 在单个 PG 事务内执行 quota 操作, 供 Reserve 保证 reservation/window/audit 原子性。
// 它只支持构造参数本身能 BeginTx 的 store, 不尝试嵌套或接管外部 pgx.Tx。
func (s *PostgresStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	if s == nil || s.beginTx == nil {
		return ErrReserveRequiresTransaction
	}
	tx, err := s.beginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	txStore := &PostgresStore{q: dbquota.New(tx)}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return nil
}

func (s *PostgresStore) ListActivePolicies(ctx context.Context, filter PolicyFilter) ([]Policy, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	scopes, err := marshalScopes(filter.Scopes)
	if err != nil {
		return nil, err
	}
	metrics := make([]string, 0, len(filter.Metrics))
	for _, metric := range filter.Metrics {
		metrics = append(metrics, string(metric))
	}
	rows, err := q.ListActiveQuotaPoliciesForScopes(ctx, dbquota.ListActiveQuotaPoliciesForScopesParams{
		TenantID: filter.TenantID,
		Scopes:   scopes,
		Metrics:  metrics,
		AtTime:   pgTimestamptz(filter.At),
	})
	if err != nil {
		return nil, err
	}
	policies := make([]Policy, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, policyFromDB(row))
	}
	return policies, nil
}

func (s *PostgresStore) ListCurrentWindowsForScope(ctx context.Context, tenantID int64, scopeKind ScopeKind, scopeID string, at time.Time) ([]CurrentWindowRead, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	rows, err := q.ListCurrentQuotaWindowsForScope(ctx, dbquota.ListCurrentQuotaWindowsForScopeParams{
		TenantID:  tenantID,
		ScopeKind: string(scopeKind),
		ScopeID:   normalizeScopeID(scopeKind, scopeID),
		AtTime:    pgTimestamptz(at.UTC()),
	})
	if err != nil {
		return nil, err
	}
	windows := make([]CurrentWindowRead, 0, len(rows))
	for _, row := range rows {
		windows = append(windows, currentWindowReadFromDB(row, at.UTC()))
	}
	return windows, nil
}

func (s *PostgresStore) UpsertWindow(ctx context.Context, input WindowUpsert) (WindowCounter, error) {
	q, err := s.queries()
	if err != nil {
		return WindowCounter{}, err
	}
	row, err := q.UpsertQuotaWindow(ctx, dbquota.UpsertQuotaWindowParams{
		TenantID:    input.TenantID,
		PolicyID:    input.PolicyID,
		WindowStart: pgTimestamptz(input.Window.Start),
		WindowEnd:   pgTimestamptz(input.Window.End),
	})
	if err != nil {
		return WindowCounter{}, err
	}
	return windowCounterFromUpsert(row), nil
}

func (s *PostgresStore) GetWindowForUpdate(ctx context.Context, tenantID int64, windowID int64) (WindowCounter, error) {
	q, err := s.queries()
	if err != nil {
		return WindowCounter{}, err
	}
	row, err := q.GetQuotaWindowForUpdate(ctx, dbquota.GetQuotaWindowForUpdateParams{
		TenantID: tenantID,
		WindowID: windowID,
	})
	if err != nil {
		return WindowCounter{}, err
	}
	return windowCounterFromGet(row), nil
}

func (s *PostgresStore) IncrementWindowReserved(ctx context.Context, input WindowReserve) (WindowCounter, error) {
	q, err := s.queries()
	if err != nil {
		return WindowCounter{}, err
	}
	reserveDelta, err := pgNumeric(input.ReserveDelta)
	if err != nil {
		return WindowCounter{}, err
	}
	limitValue, err := pgNumeric(input.LimitValue)
	if err != nil {
		return WindowCounter{}, err
	}
	row, err := q.IncrementQuotaWindowReserved(ctx, dbquota.IncrementQuotaWindowReservedParams{
		ReserveDelta:      reserveDelta,
		RequestCountDelta: input.RequestCountDelta,
		TenantID:          input.TenantID,
		WindowID:          input.WindowID,
		LimitValue:        limitValue,
	})
	if err != nil {
		return WindowCounter{}, err
	}
	return windowCounterFromReserve(row), nil
}

func (s *PostgresStore) IncrementWindowRequestCount(ctx context.Context, input WindowRequestCount) (WindowCounter, error) {
	q, err := s.queries()
	if err != nil {
		return WindowCounter{}, err
	}
	limitValue, err := pgNumeric(input.LimitValue)
	if err != nil {
		return WindowCounter{}, err
	}
	row, err := q.IncrementQuotaWindowRequestCount(ctx, dbquota.IncrementQuotaWindowRequestCountParams{
		RequestCountDelta: input.RequestCountDelta,
		TenantID:          input.TenantID,
		WindowID:          input.WindowID,
		LimitValue:        limitValue,
	})
	if err != nil {
		return WindowCounter{}, err
	}
	return windowCounterFromRequestCount(row), nil
}

func (s *PostgresStore) ApplyWindowSettlement(ctx context.Context, input WindowSettlement) (WindowCounter, error) {
	q, err := s.queries()
	if err != nil {
		return WindowCounter{}, err
	}
	releaseValue, err := pgNumeric(input.ReservedReleaseValue)
	if err != nil {
		return WindowCounter{}, err
	}
	settledValue, err := pgNumeric(input.SettledAddValue)
	if err != nil {
		return WindowCounter{}, err
	}
	overageValue, err := pgNumeric(input.OverageAddValue)
	if err != nil {
		return WindowCounter{}, err
	}
	row, err := q.ApplyQuotaWindowSettlement(ctx, dbquota.ApplyQuotaWindowSettlementParams{
		ReservedReleaseValue: releaseValue,
		SettledAddValue:      settledValue,
		OverageAddValue:      overageValue,
		TenantID:             input.TenantID,
		WindowID:             input.WindowID,
	})
	if err != nil {
		return WindowCounter{}, err
	}
	return windowCounterFromSettlement(row), nil
}

func (s *PostgresStore) GetReservationByClaimForUpdate(ctx context.Context, tenantID int64, claimID int64) (Reservation, error) {
	q, err := s.queries()
	if err != nil {
		return Reservation{}, err
	}
	row, err := q.GetQuotaReservationByClaimForUpdate(ctx, dbquota.GetQuotaReservationByClaimForUpdateParams{
		TenantID: tenantID,
		ClaimID:  claimID,
	})
	if err != nil {
		return Reservation{}, err
	}
	return reservationFromGet(row)
}

func (s *PostgresStore) InsertReservation(ctx context.Context, input ReservationInsert) (Reservation, error) {
	q, err := s.queries()
	if err != nil {
		return Reservation{}, err
	}
	scopeSnapshot, err := marshalScopes(input.Scopes)
	if err != nil {
		return Reservation{}, err
	}
	policySnapshot := input.PolicySnapshot
	if len(policySnapshot) == 0 {
		policySnapshot = []byte("[]")
	}
	predictedCost, err := pgNumeric(input.PredictedCost)
	if err != nil {
		return Reservation{}, err
	}
	reservedUnits, err := pgNumeric(input.ReservedUnits)
	if err != nil {
		return Reservation{}, err
	}
	row, err := q.InsertQuotaReservation(ctx, dbquota.InsertQuotaReservationParams{
		TenantID:           input.TenantID,
		ClaimID:            input.ClaimID,
		RequestFingerprint: input.RequestFingerprint,
		ScopeSnapshot:      scopeSnapshot,
		PolicySnapshot:     policySnapshot,
		PredictedCost:      predictedCost,
		ReservedUnits:      reservedUnits,
		LeaseExpiresAt:     pgTimestamptz(input.LeaseExpiresAt),
	})
	if err != nil {
		return Reservation{}, err
	}
	return reservationFromInsert(row)
}

func (s *PostgresStore) ReactivateReservation(ctx context.Context, input ReservationReactivate) (Reservation, error) {
	q, err := s.queries()
	if err != nil {
		return Reservation{}, err
	}
	scopeSnapshot, err := marshalScopes(input.Scopes)
	if err != nil {
		return Reservation{}, err
	}
	policySnapshot := input.PolicySnapshot
	if len(policySnapshot) == 0 {
		policySnapshot = []byte("[]")
	}
	predictedCost, err := pgNumeric(input.PredictedCost)
	if err != nil {
		return Reservation{}, err
	}
	reservedUnits, err := pgNumeric(input.ReservedUnits)
	if err != nil {
		return Reservation{}, err
	}
	row, err := q.ReactivateQuotaReservation(ctx, dbquota.ReactivateQuotaReservationParams{
		RequestFingerprint: input.RequestFingerprint,
		ScopeSnapshot:      scopeSnapshot,
		PolicySnapshot:     policySnapshot,
		PredictedCost:      predictedCost,
		ReservedUnits:      reservedUnits,
		LeaseExpiresAt:     pgTimestamptz(input.LeaseExpiresAt),
		TenantID:           input.TenantID,
		ReservationID:      input.ReservationID,
		ClaimID:            input.ClaimID,
	})
	if err != nil {
		return Reservation{}, err
	}
	return reservationFromReactivate(row)
}

func (s *PostgresStore) SettleReservation(ctx context.Context, settlement Settlement) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	settledCost, err := pgNumeric(settlement.ActualCost)
	if err != nil {
		return err
	}
	settledUnits, err := pgNumeric(settlement.SettledUnits)
	if err != nil {
		return err
	}
	overageUnits, err := pgNumeric(settlement.OverageUnits)
	if err != nil {
		return err
	}
	rows, err := q.SettleQuotaReservation(ctx, dbquota.SettleQuotaReservationParams{
		SettledCost:   settledCost,
		SettledUnits:  settledUnits,
		OverageUnits:  overageUnits,
		TenantID:      settlement.TenantID,
		ReservationID: settlement.ReservationID,
		ClaimID:       settlement.ClaimID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) ReleaseReservation(ctx context.Context, input ReservationRelease) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.ReleaseQuotaReservation(ctx, dbquota.ReleaseQuotaReservationParams{
		ReleaseReason: input.Reason,
		TenantID:      input.TenantID,
		ReservationID: input.ReservationID,
		ClaimID:       input.ClaimID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) MarkReservationReconciliationNeeded(ctx context.Context, tenantID int64, reservationID int64, claimID int64) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.MarkQuotaReservationReconciliationNeeded(ctx, dbquota.MarkQuotaReservationReconciliationNeededParams{
		TenantID:      tenantID,
		ReservationID: reservationID,
		ClaimID:       claimID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) AcquireConcurrencySlot(ctx context.Context, input ConcurrencyAcquire) (ConcurrencySlot, error) {
	q, err := s.queries()
	if err != nil {
		return ConcurrencySlot{}, err
	}
	row, err := q.AcquireQuotaConcurrencySlot(ctx, dbquota.AcquireQuotaConcurrencySlotParams{
		TenantID:       input.TenantID,
		ReservationID:  input.ReservationID,
		ClaimID:        input.ClaimID,
		ScopeKind:      string(input.Scope.Kind),
		ScopeID:        normalizeScopeID(input.Scope.Kind, input.Scope.ID),
		AtTime:         pgTimestamptz(input.At),
		LeaseExpiresAt: pgTimestamptz(input.LeaseExpiresAt),
		SlotLimit:      input.SlotLimit,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ConcurrencySlot{}, nil
	}
	if err != nil {
		return ConcurrencySlot{}, err
	}
	return ConcurrencySlot{
		TenantID:      row.TenantID,
		ID:            row.ID,
		ReservationID: row.ReservationID,
		// claim_id 是精确 bigint 入参, DB 函数原样写入, 因此这里用入参作为 canonical 值。
		ClaimID: input.ClaimID,
		Scope: Scope{
			TenantID: row.TenantID,
			Kind:     ScopeKind(row.ScopeKind),
			ID:       row.ScopeID,
		},
		LeaseExpiresAt: pgTime(row.LeaseExpiresAt),
		Status:         row.Status,
	}, nil
}

func (s *PostgresStore) ReleaseConcurrencySlots(ctx context.Context, tenantID int64, reservationID int64, reason string) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "released"
	}
	_, err = q.ReleaseQuotaConcurrencySlotsByReservation(ctx, dbquota.ReleaseQuotaConcurrencySlotsByReservationParams{
		ReleaseReason: reason,
		TenantID:      tenantID,
		ReservationID: reservationID,
	})
	return err
}

func (s *PostgresStore) ExpireConcurrencySlots(ctx context.Context, tenantID int64, at time.Time) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	_, err = q.ExpireQuotaConcurrencySlots(ctx, dbquota.ExpireQuotaConcurrencySlotsParams{
		TenantID: tenantID,
		AtTime:   pgTimestamptz(at),
	})
	return err
}

func (s *PostgresStore) InsertAuditEvent(ctx context.Context, event AuditEvent) (int64, error) {
	q, err := s.queries()
	if err != nil {
		return 0, err
	}
	reserved, err := pgNumeric(event.AmountReserved)
	if err != nil {
		return 0, err
	}
	settled, err := pgNumeric(event.AmountSettled)
	if err != nil {
		return 0, err
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	var retryAfter *int32
	if event.RetryAfterSeconds != nil {
		v := int32(*event.RetryAfterSeconds)
		retryAfter = &v
	}
	row, err := q.InsertQuotaAuditEvent(ctx, dbquota.InsertQuotaAuditEventParams{
		TenantID:          event.TenantID,
		ReservationID:     event.ReservationID,
		ClaimID:           event.ClaimID,
		EventType:         event.EventType,
		DecisionCode:      event.DecisionCode,
		ScopeKind:         string(event.Scope.Kind),
		ScopeID:           normalizeScopeID(event.Scope.Kind, event.Scope.ID),
		Metric:            string(event.Metric),
		AmountReserved:    reserved,
		AmountSettled:     settled,
		RetryAfterSeconds: retryAfter,
		Payload:           payload,
		Actor:             event.Actor,
	})
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (s *PostgresStore) EnqueueReconciliationJob(ctx context.Context, input ReconciliationEnqueue) (ReconciliationJob, error) {
	q, err := s.queries()
	if err != nil {
		return ReconciliationJob{}, err
	}
	row, err := q.EnqueueQuotaReconciliationJob(ctx, dbquota.EnqueueQuotaReconciliationJobParams{
		TenantID:      input.TenantID,
		ClaimID:       input.ClaimID,
		ReservationID: input.ReservationID,
		JobKind:       input.Kind,
		LastError:     input.LastError,
		NextRunAt:     pgTimestamptz(input.NextRunAt),
	})
	if err != nil {
		return ReconciliationJob{}, err
	}
	return ReconciliationJob{
		TenantID:      row.TenantID,
		ID:            row.ID,
		ClaimID:       row.ClaimID,
		ReservationID: row.ReservationID,
		Kind:          row.JobKind,
		Status:        row.Status,
		AttemptCount:  int(row.AttemptCount),
		NextRunAt:     pgTime(row.NextRunAt),
	}, nil
}

func (s *PostgresStore) ListDueReconciliationJobs(ctx context.Context, tenantID int64, at time.Time, limit int) ([]ReconciliationJob, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	if limit > math.MaxInt32 {
		limit = math.MaxInt32
	}
	rows, err := q.ListDueQuotaReconciliationJobs(ctx, dbquota.ListDueQuotaReconciliationJobsParams{
		TenantID: tenantID,
		AtTime:   pgTimestamptz(at),
		JobLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	jobs := make([]ReconciliationJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, ReconciliationJob{
			TenantID:      row.TenantID,
			ID:            row.ID,
			ClaimID:       row.ClaimID,
			ReservationID: row.ReservationID,
			Kind:          row.JobKind,
			Status:        row.Status,
			AttemptCount:  int(row.AttemptCount),
			LastError:     row.LastError,
			NextRunAt:     pgTime(row.NextRunAt),
		})
	}
	return jobs, nil
}

func (s *PostgresStore) MarkReconciliationJobRunning(ctx context.Context, tenantID int64, jobID int64) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.MarkQuotaReconciliationJobRunning(ctx, dbquota.MarkQuotaReconciliationJobRunningParams{
		TenantID: tenantID,
		JobID:    jobID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) CompleteReconciliationJob(ctx context.Context, tenantID int64, jobID int64) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.CompleteQuotaReconciliationJob(ctx, dbquota.CompleteQuotaReconciliationJobParams{
		TenantID: tenantID,
		JobID:    jobID,
	})
	return requireAffected(rows, err)
}

func (s *PostgresStore) FailReconciliationJob(ctx context.Context, input ReconciliationFailure) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	rows, err := q.FailQuotaReconciliationJob(ctx, dbquota.FailQuotaReconciliationJobParams{
		LastError: input.LastError,
		NextRunAt: pgTimestamptz(input.NextRunAt),
		TenantID:  input.TenantID,
		JobID:     input.JobID,
	})
	return requireAffected(rows, err)
}

var _ PGStore = (*PostgresStore)(nil)
var _ ProgressReadStore = (*PostgresStore)(nil)
