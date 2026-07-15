package quota

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

// AT-CD2-001:Release 的前五笔完整事务发生瞬时冲突时，第六笔必须成功，且业务写入只提交一次。
func TestServiceRelease_ATCD2001_SixthTransactionSucceeds(t *testing.T) {
	conflicts := []error{
		releaseConflict("40001"),
		releaseConflict("40P01"),
		releaseConflict("40001"),
		releaseConflict("40P01"),
		releaseConflict("40001"),
	}
	store := newReleaseResilienceStore(conflicts)
	metricBefore := quotaReleaseMetricValue("40001", "retry_success")

	result, err := NewService(store).Release(context.Background(), ReleaseRequest{
		TenantID: 1,
		ClaimID:  store.reservation.ClaimID,
		Reason:   "abort",
	})

	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if store.finalizationTransactions != 6 || store.withTxCalls != 6 {
		t.Fatalf("finalization transactions=%d WithTx=%d; want 6/6", store.finalizationTransactions, store.withTxCalls)
	}
	if result.Reservation.Status != ReservationReleased || store.reservation.Status != ReservationReleased {
		t.Fatalf("result/store status=%s/%s; want released/released", result.Reservation.Status, store.reservation.Status)
	}
	if store.windowReserved.Sign() != 0 {
		t.Fatalf("window reserved=%s; want 0", store.windowReserved)
	}
	if !store.slotReleased {
		t.Fatal("slotReleased=false; want true")
	}
	if store.releaseWrites != 1 {
		t.Fatalf("release business writes=%d; want exactly 1", store.releaseWrites)
	}
	if len(store.audits) != 1 || store.audits[0].DecisionCode != "quota_release_aborted" {
		t.Fatalf("audits=%+v; want exactly one release audit", store.audits)
	}
	if got := quotaReleaseMetricValue("40001", "retry_success") - metricBefore; got != 1 {
		t.Fatalf("retry_success metric delta=%d; want 1", got)
	}
}

// TestServiceRelease_ClaimStateThreeWayGuard 固定 Release 行锁后的三分流：
// aborted 正常释放；复活后的孤儿标记仍释放；复活后仍为 reserved 的活预留必须拒绝。
// 变异：删除任一状态条件，至少一个子用例会在状态、窗口或 sentinel 断言上变红。
func TestServiceRelease_ClaimStateThreeWayGuard(t *testing.T) {
	tests := []struct {
		name              string
		claimStatus       string
		reservationStatus ReservationStatus
		wantReleased      bool
		wantInvalidated   bool
	}{
		{
			name:              "aborted_releases_reserved",
			claimStatus:       claimStatusAborted,
			reservationStatus: ReservationReserved,
			wantReleased:      true,
		},
		{
			name:              "revived_releases_orphan_marker",
			claimStatus:       "reserving",
			reservationStatus: ReservationReconciliationNeeded,
			wantReleased:      true,
		},
		{
			name:              "revived_protects_live_reserved",
			claimStatus:       "reserving",
			reservationStatus: ReservationReserved,
			wantInvalidated:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newReleaseResilienceStore(nil)
			store.claimStatus = tc.claimStatus
			store.reservation.Status = tc.reservationStatus

			result, err := NewService(store).Release(context.Background(), ReleaseRequest{
				TenantID: store.reservation.TenantID,
				ClaimID:  store.reservation.ClaimID,
				Reason:   "abort",
			})

			if store.claimReads != 1 || !store.claimReadInsideTx {
				t.Fatalf("claim reads/inside tx=%d/%v; want 1/true", store.claimReads, store.claimReadInsideTx)
			}
			if tc.wantReleased {
				if err != nil {
					t.Fatalf("Release: %v", err)
				}
				if result.Reservation.Status != ReservationReleased || store.reservation.Status != ReservationReleased {
					t.Fatalf("result/store status=%s/%s; want released/released", result.Reservation.Status, store.reservation.Status)
				}
				if store.windowReserved.Sign() != 0 || !store.slotReleased || store.releaseWrites != 1 {
					t.Fatalf("window/slot/writes=%s/%v/%d; want 0/true/1", store.windowReserved, store.slotReleased, store.releaseWrites)
				}
				return
			}

			if !tc.wantInvalidated || !errors.Is(err, ErrReleaseInvalidatedByRevival) {
				t.Fatalf("err=%v; want ErrReleaseInvalidatedByRevival", err)
			}
			if result.ReconciliationQueued || store.prepareCalls != 0 || len(store.jobs) != 0 {
				t.Fatalf("queued/prepare/jobs=%v/%d/%d; want false/0/0", result.ReconciliationQueued, store.prepareCalls, len(store.jobs))
			}
			if store.reservation.Status != ReservationReserved || !store.windowReserved.Equal(decimal.NewFromInt(1)) {
				t.Fatalf("status/window=%s/%s; want reserved/1", store.reservation.Status, store.windowReserved)
			}
			if store.slotReleased || store.releaseWrites != 0 || len(store.audits) != 0 {
				t.Fatalf("slot/writes/audits=%v/%d/%d; want false/0/0", store.slotReleased, store.releaseWrites, len(store.audits))
			}
		})
	}
}

// AT-CD2-003:真实 Abort 只传 tenant+claim。即使第六次冲突同时取消请求，恢复准备与入队仍须用独立有界上下文完成。
func TestServiceRelease_ATCD2003_CanceledRequestStillHandsOffByClaim(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	primaryCause := releaseConflict("40001")
	conflicts := []error{primaryCause, primaryCause, primaryCause, primaryCause, primaryCause, primaryCause}
	store := newReleaseResilienceStore(conflicts)
	store.cancelOnFinalConflict = cancelRequest
	exhaustedBefore := quotaReleaseMetricValue("40001", "exhausted")
	queuedBefore := quotaReleaseMetricValue("40001", "handoff_queued")

	result, err := NewService(store).Release(requestCtx, ReleaseRequest{
		TenantID:      1,
		ClaimID:       store.reservation.ClaimID,
		ReservationID: 0,
		Reason:        "abort",
	})

	if !errors.Is(err, primaryCause) || !IsRetryable(err) {
		t.Fatalf("err=%T %v; want original retryable cause", err, err)
	}
	if !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatalf("request ctx err=%v; want canceled", requestCtx.Err())
	}
	if store.finalizationTransactions != 6 {
		t.Fatalf("finalization transactions=%d; want 6", store.finalizationTransactions)
	}
	if store.withTxCalls != 8 {
		t.Fatalf("WithTx calls=%d; want 6 finalization + prepare + enqueue", store.withTxCalls)
	}
	if !store.prepareSawLiveContext || !store.enqueueSawLiveContext {
		t.Fatalf("cleanup contexts live prepare/enqueue=%v/%v; want true/true", store.prepareSawLiveContext, store.enqueueSawLiveContext)
	}
	if !store.prepareSawBoundedContext || !store.enqueueSawBoundedContext {
		t.Fatalf("cleanup contexts bounded prepare/enqueue=%v/%v; want true/true", store.prepareSawBoundedContext, store.enqueueSawBoundedContext)
	}
	if store.prepareTenantID != 1 || store.prepareClaimID != store.reservation.ClaimID || store.prepareGuardID != 0 {
		t.Fatalf("prepare keys tenant/claim/guard=%d/%d/%d; want 1/%d/0", store.prepareTenantID, store.prepareClaimID, store.prepareGuardID, store.reservation.ClaimID)
	}
	if store.reservation.Status != ReservationReconciliationNeeded {
		t.Fatalf("reservation status=%s; want reconciliation_needed", store.reservation.Status)
	}
	if store.reservation.LeaseExpiresAt.After(store.dbNow) {
		t.Fatalf("lease=%s; want <= DB now %s", store.reservation.LeaseExpiresAt, store.dbNow)
	}
	if !result.ReconciliationQueued || len(store.jobs) != 1 {
		t.Fatalf("ReconciliationQueued=%v jobs=%d; want true/1", result.ReconciliationQueued, len(store.jobs))
	}
	job := store.jobs[0]
	if job.ReservationID == nil || *job.ReservationID != store.reservation.ID {
		t.Fatalf("job reservation=%v; want real id %d", job.ReservationID, store.reservation.ID)
	}
	if job.Status != "queued" || job.Kind != "release_after_abort" {
		t.Fatalf("job status/kind=%s/%s; want queued/release_after_abort", job.Status, job.Kind)
	}
	if job.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("job next_run_at=%s; want <= now", job.NextRunAt)
	}
	if got := quotaReleaseMetricValue("40001", "exhausted") - exhaustedBefore; got != 1 {
		t.Fatalf("exhausted metric delta=%d; want 1", got)
	}
	if got := quotaReleaseMetricValue("40001", "handoff_queued") - queuedBefore; got != 1 {
		t.Fatalf("handoff_queued metric delta=%d; want 1", got)
	}
}

// AT-CD2-004 的无 socket 护栏：手工 SQL 镜像必须在同一守卫更新中提前 lease，删除该赋值时本测试直接变红。
func TestPrepareQuotaReleaseRecovery_ATCD2004_QueryExpiresLease(t *testing.T) {
	db := &capturingQuotaQueryDB{reservationID: 77}
	queries := dbquota.New(db)

	reservationID, err := queries.PrepareQuotaReleaseRecovery(context.Background(), dbquota.PrepareQuotaReleaseRecoveryParams{
		TenantID:      1,
		ClaimID:       701,
		ReservationID: 0,
	})

	if err != nil || reservationID != 77 {
		t.Fatalf("PrepareQuotaReleaseRecovery id/err=%d/%v; want 77/nil", reservationID, err)
	}
	for _, clause := range []string{
		"lease_expires_at = LEAST(lease_expires_at, NOW())",
		"WHERE tenant_id = $1::bigint",
		"claim_id = $2::bigint",
		"($3::bigint = 0 OR id = $3::bigint)",
		"status IN ('reserved', 'reconciliation_needed')",
		"FROM billing_ledger_claims blc",
		"blc.status = 'aborted'",
		"RETURNING id",
	} {
		if !strings.Contains(db.query, clause) {
			t.Fatalf("query missing %q:\n%s", clause, db.query)
		}
	}
	if len(db.args) != 3 || db.args[0] != int64(1) || db.args[1] != int64(701) || db.args[2] != int64(0) {
		t.Fatalf("query args=%v; want tenant/claim/optional-id [1 701 0]", db.args)
	}
}

func TestDefaultFinalizationRetryPolicy_RemainsThreeAttempts(t *testing.T) {
	for _, operation := range []string{"settle", "cache_hit"} {
		t.Run(operation, func(t *testing.T) {
			store := newReleaseResilienceStore(nil)
			service := NewService(store)
			cause := releaseConflict("40001")

			err := service.runQuotaFinalizationWithRetry(context.Background(), operation, defaultFinalizationRetryPolicy, func(PGStore) error {
				return cause
			})

			if !IsRetryable(err) || !errors.Is(err, cause) {
				t.Fatalf("err=%v; want retryable original cause", err)
			}
			if reserveTxRetryAttempts != 3 {
				t.Fatalf("reserveTxRetryAttempts=%d; want unchanged default 3", reserveTxRetryAttempts)
			}
			if store.withTxCalls != reserveTxRetryAttempts {
				t.Fatalf("WithTx calls=%d; want unchanged default %d", store.withTxCalls, reserveTxRetryAttempts)
			}
		})
	}
}

// TestServiceRelease_ATCD2006_BusinessErrorRunsOneTransaction 固定 Release
// 只对 40001/40P01 重试；把普通错误也归为瞬时冲突时事务次数会从一变为六。
func TestServiceRelease_ATCD2006_BusinessErrorRunsOneTransaction(t *testing.T) {
	store := newReleaseResilienceStore(nil)
	businessErr := errors.New("deterministic release business failure")
	calls := 0

	err := NewService(store).runQuotaFinalizationWithRetry(context.Background(), "release", releaseFinalizationRetryPolicy, func(PGStore) error {
		calls++
		return businessErr
	})

	if err != businessErr {
		t.Fatalf("err=%v，want 原业务错误指针 %v", err, businessErr)
	}
	if calls != 1 || store.withTxCalls != 1 {
		t.Fatalf("闭包/事务次数=%d/%d，want 1/1", calls, store.withTxCalls)
	}
}

func TestReleaseRecoveryHandoff_ObservesStaleOnlyAndFailed(t *testing.T) {
	primaryCause := releaseConflict("40P01")

	t.Run("stale_only", func(t *testing.T) {
		store := newReleaseResilienceStore(nil)
		store.enqueueFailures = []error{errors.New("deterministic enqueue failure")}
		before := quotaReleaseMetricValue("40P01", "handoff_stale_only")

		queued, err := NewService(store).handoffReleaseRecovery(context.Background(), 1, store.reservation.ClaimID, 0, primaryCause)

		if err == nil || queued {
			t.Fatalf("queued/err=%v/%v; want false/non-nil", queued, err)
		}
		if store.reservation.Status != ReservationReconciliationNeeded || store.reservation.LeaseExpiresAt.After(store.dbNow) {
			t.Fatalf("status/lease=%s/%s; want durable stale eligibility", store.reservation.Status, store.reservation.LeaseExpiresAt)
		}
		if got := quotaReleaseMetricValue("40P01", "handoff_stale_only") - before; got != 1 {
			t.Fatalf("handoff_stale_only metric delta=%d; want 1", got)
		}
	})

	t.Run("failed", func(t *testing.T) {
		store := newReleaseResilienceStore(nil)
		store.prepareFailures = []error{errors.New("deterministic prepare failure")}
		before := quotaReleaseMetricValue("40P01", "handoff_failed")

		queued, err := NewService(store).handoffReleaseRecovery(context.Background(), 1, store.reservation.ClaimID, 0, primaryCause)

		if err == nil || queued {
			t.Fatalf("queued/err=%v/%v; want false/non-nil", queued, err)
		}
		if store.reservation.Status != ReservationReserved || !store.reservation.LeaseExpiresAt.After(store.dbNow) {
			t.Fatalf("status/lease=%s/%s; want untouched reserved future lease", store.reservation.Status, store.reservation.LeaseExpiresAt)
		}
		if got := quotaReleaseMetricValue("40P01", "handoff_failed") - before; got != 1 {
			t.Fatalf("handoff_failed metric delta=%d; want 1", got)
		}
	})
}

func TestReleaseRecoveryCleanup_RetriesEachStepAtMostThreeTimes(t *testing.T) {
	store := newReleaseResilienceStore(nil)
	store.prepareFailures = []error{releaseConflict("40001"), releaseConflict("40P01")}
	store.enqueueFailures = []error{releaseConflict("40P01"), releaseConflict("40001")}

	queued, err := NewService(store).handoffReleaseRecovery(context.Background(), 1, store.reservation.ClaimID, 0, releaseConflict("40001"))

	if err != nil || !queued {
		t.Fatalf("queued/err=%v/%v; want true/nil after bounded retries", queued, err)
	}
	if store.prepareCalls != 3 || store.enqueueCalls != 3 || store.withTxCalls != 6 {
		t.Fatalf("prepare/enqueue/WithTx=%d/%d/%d; want 3/3/6", store.prepareCalls, store.enqueueCalls, store.withTxCalls)
	}
}

type releaseResilienceStore struct {
	noTxReserveStore
	reservation              Reservation
	dbNow                    time.Time
	conflicts                []error
	cancelOnFinalConflict    context.CancelFunc
	withTxCalls              int
	finalizationTransactions int
	windowReserved           decimal.Decimal
	slotReleased             bool
	releaseWrites            int
	audits                   []AuditEvent
	prepareSawLiveContext    bool
	enqueueSawLiveContext    bool
	prepareSawBoundedContext bool
	enqueueSawBoundedContext bool
	prepareTenantID          int64
	prepareClaimID           int64
	prepareGuardID           int64
	prepareCalls             int
	prepareFailures          []error
	enqueueCalls             int
	enqueueFailures          []error
	jobs                     []ReconciliationJob
	claimStatus              string
	inTx                     bool
	claimReads               int
	claimReadInsideTx        bool
}

type capturingQuotaQueryDB struct {
	query         string
	args          []any
	reservationID int64
}

func (d *capturingQuotaQueryDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), errors.New("unexpected Exec")
}

func (d *capturingQuotaQueryDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (d *capturingQuotaQueryDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	d.query = query
	d.args = append([]any(nil), args...)
	return capturingQuotaRow{reservationID: d.reservationID}
}

type capturingQuotaRow struct {
	reservationID int64
}

func (r capturingQuotaRow) Scan(dest ...interface{}) error {
	if len(dest) != 1 {
		return fmt.Errorf("scan destinations=%d; want 1", len(dest))
	}
	reservationID, ok := dest[0].(*int64)
	if !ok {
		return fmt.Errorf("scan destination=%T; want *int64", dest[0])
	}
	*reservationID = r.reservationID
	return nil
}

func newReleaseResilienceStore(conflicts []error) *releaseResilienceStore {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	policySnapshot := marshalPolicySnapshot([]Policy{{
		TenantID:   1,
		ID:         51,
		Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
		Metric:     MetricRequests,
		Window:     Window{Kind: WindowFixed, Start: now.Add(-time.Minute), End: now.Add(time.Hour)},
		LimitValue: decimal.NewFromInt(100),
		Mode:       ModeEnforce,
	}}, map[policyMetricKey]decimal.Decimal{{policyID: 51, metric: MetricRequests}: decimal.NewFromInt(1)})
	return &releaseResilienceStore{
		reservation: Reservation{
			TenantID:       1,
			ID:             77,
			ClaimID:        701,
			PolicySnapshot: policySnapshot,
			ReservedUnits:  decimal.NewFromInt(1),
			Status:         ReservationReserved,
			LeaseExpiresAt: now.Add(30 * time.Minute),
		},
		dbNow:          now,
		conflicts:      append([]error(nil), conflicts...),
		windowReserved: decimal.NewFromInt(1),
		claimStatus:    claimStatusAborted,
	}
}

func (s *releaseResilienceStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	s.withTxCalls++
	s.inTx = true
	defer func() { s.inTx = false }()
	return fn(s)
}

func (s *releaseResilienceStore) GetReservationByClaimForUpdate(ctx context.Context, tenantID int64, claimID int64) (Reservation, error) {
	s.finalizationTransactions++
	if tenantID != s.reservation.TenantID || claimID != s.reservation.ClaimID {
		return Reservation{}, errors.New("unexpected reservation lookup keys")
	}
	if len(s.conflicts) > 0 {
		err := s.conflicts[0]
		s.conflicts = s.conflicts[1:]
		if len(s.conflicts) == 0 && s.cancelOnFinalConflict != nil {
			s.cancelOnFinalConflict()
		}
		return Reservation{}, err
	}
	return s.reservation, nil
}

func (s *releaseResilienceStore) GetClaimTerminalState(_ context.Context, tenantID int64, claimID int64) (ClaimTerminalState, error) {
	s.claimReads++
	s.claimReadInsideTx = s.claimReadInsideTx || s.inTx
	if !s.inTx {
		return ClaimTerminalState{}, errors.New("billing claim read outside transaction")
	}
	if tenantID != s.reservation.TenantID || claimID != s.reservation.ClaimID {
		return ClaimTerminalState{}, errors.New("unexpected billing claim lookup keys")
	}
	return ClaimTerminalState{Status: s.claimStatus}, nil
}

func (s *releaseResilienceStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return WindowCounter{TenantID: 1, ID: 61, PolicyID: 51}, nil
}

func (s *releaseResilienceStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return WindowCounter{TenantID: 1, ID: 61, PolicyID: 51, ReservedValue: s.windowReserved}, nil
}

func (s *releaseResilienceStore) ApplyWindowSettlement(_ context.Context, input WindowSettlement) (WindowCounter, error) {
	s.windowReserved = s.windowReserved.Sub(input.ReservedReleaseValue)
	return WindowCounter{TenantID: 1, ID: input.WindowID, PolicyID: 51, ReservedValue: s.windowReserved}, nil
}

func (s *releaseResilienceStore) ReleaseConcurrencySlots(context.Context, int64, int64, string) error {
	s.slotReleased = true
	return nil
}

func (s *releaseResilienceStore) ReleaseReservation(_ context.Context, input ReservationRelease) error {
	if input.ReservationID != s.reservation.ID {
		return fmt.Errorf("reservation id=%d; want %d", input.ReservationID, s.reservation.ID)
	}
	s.releaseWrites++
	s.reservation.Status = ReservationReleased
	return nil
}

func (s *releaseResilienceStore) InsertAuditEvent(_ context.Context, event AuditEvent) (int64, error) {
	s.audits = append(s.audits, event)
	return int64(len(s.audits)), nil
}

// PrepareReleaseRecovery 模拟数据库按 tenant+claim 原子准备恢复，并返回数据库中的真实 ID。
func (s *releaseResilienceStore) PrepareReleaseRecovery(ctx context.Context, tenantID int64, claimID int64, reservationID int64) (int64, error) {
	s.prepareCalls++
	s.prepareSawLiveContext = ctx.Err() == nil
	dl, ok := ctx.Deadline()
	remaining := time.Until(dl)
	s.prepareSawBoundedContext = ok && remaining > 0 && remaining <= quotaCleanupTimeout
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(s.prepareFailures) > 0 {
		err := s.prepareFailures[0]
		s.prepareFailures = s.prepareFailures[1:]
		return 0, err
	}
	s.prepareTenantID = tenantID
	s.prepareClaimID = claimID
	s.prepareGuardID = reservationID
	if tenantID != s.reservation.TenantID || claimID != s.reservation.ClaimID || (reservationID != 0 && reservationID != s.reservation.ID) {
		return 0, errors.New("recovery keys do not match")
	}
	if s.reservation.Status != ReservationReserved && s.reservation.Status != ReservationReconciliationNeeded {
		return 0, errors.New("reservation is terminal")
	}
	if s.claimStatus != claimStatusAborted {
		return 0, pgx.ErrNoRows
	}
	s.reservation.Status = ReservationReconciliationNeeded
	if s.reservation.LeaseExpiresAt.After(s.dbNow) {
		s.reservation.LeaseExpiresAt = s.dbNow
	}
	return s.reservation.ID, nil
}

func (s *releaseResilienceStore) EnqueueReconciliationJob(ctx context.Context, input ReconciliationEnqueue) (ReconciliationJob, error) {
	s.enqueueCalls++
	s.enqueueSawLiveContext = ctx.Err() == nil
	dl, ok := ctx.Deadline()
	remaining := time.Until(dl)
	s.enqueueSawBoundedContext = ok && remaining > 0 && remaining <= quotaCleanupTimeout
	if err := ctx.Err(); err != nil {
		return ReconciliationJob{}, err
	}
	if len(s.enqueueFailures) > 0 {
		err := s.enqueueFailures[0]
		s.enqueueFailures = s.enqueueFailures[1:]
		return ReconciliationJob{}, err
	}
	job := ReconciliationJob{
		TenantID:      input.TenantID,
		ID:            int64(len(s.jobs) + 1),
		ClaimID:       input.ClaimID,
		ReservationID: input.ReservationID,
		Kind:          input.Kind,
		Status:        "queued",
		NextRunAt:     input.NextRunAt,
	}
	s.jobs = append(s.jobs, job)
	return job, nil
}

func releaseConflict(code string) error {
	return &pgconn.PgError{Code: code, Message: "deterministic quota release conflict"}
}

func quotaReleaseMetricValue(sqlstate string, outcome string) int64 {
	metric := expvar.Get("quota")
	m, ok := metric.(*expvar.Map)
	if !ok {
		return 0
	}
	key := fmt.Sprintf("operation=release|sqlstate=%s|outcome=%s", sqlstate, outcome)
	value, ok := m.Get(key).(*expvar.Int)
	if !ok {
		return 0
	}
	return value.Value()
}
