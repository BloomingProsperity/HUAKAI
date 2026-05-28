package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// ReserveRequiresTransaction 守住 Reserve 多写路径必须 fail-closed, 不能在无
// transaction store 上执行半套 reservation/window/audit 写入。
func TestServiceReserve_RequiresTransaction(t *testing.T) {
	store := &noTxReserveStore{}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            10,
		RequestFingerprint: "fp-no-tx",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeGlobal, ID: "*"}},
		PredictedCost:      decimal.RequireFromString("1"),
		LeaseExpiresAt:     time.Now().UTC().Add(time.Minute),
		At:                 time.Now().UTC(),
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v; want denied fail-closed without transaction support", err)
	}
	if result.Allowed || result.Decision.Code != decisionCodeFailClosed {
		t.Fatalf("result=%+v; want fail-closed deny", result)
	}
	if store.called {
		t.Fatalf("Reserve executed store methods without transaction support")
	}
}

// UniqueClaimConflictRereadsWinner 守住同 claim 并发插入的输者遇到唯一键
// 冲突后必须重读赢家。Mutation: 直接返回 InsertReservation 错误会 fail_closed。
func TestServiceReserve_UniqueClaimConflictRereadsWinner(t *testing.T) {
	at := time.Date(2026, 5, 28, 16, 0, 0, 0, time.UTC)
	store := &claimConflictReplayStore{
		winner: Reservation{
			TenantID:       1,
			ID:             99,
			ClaimID:        10,
			Status:         ReservationReserved,
			LeaseExpiresAt: at.Add(5 * time.Minute),
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            10,
		RequestFingerprint: "fp-claim-race",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeGlobal, ID: "*"}},
		PredictedCost:      decimal.RequireFromString("1"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if err != nil {
		t.Fatalf("Reserve err=%v; want idempotent replay of winner", err)
	}
	if !result.Allowed || !result.IdempotencyHit || result.Reservation.ID != store.winner.ID {
		t.Fatalf("result=%+v; want allowed idempotent winner reservation", result)
	}
	if store.txCalls != 2 {
		t.Fatalf("txCalls=%d; want insert tx plus replay read tx", store.txCalls)
	}
	if store.insertCalls != 1 {
		t.Fatalf("insertCalls=%d; want one losing insert", store.insertCalls)
	}
	if store.requestCountIncrements != 0 || store.reserveIncrements != 0 {
		t.Fatalf("increments request=%d reserve=%d; want no window increment by loser", store.requestCountIncrements, store.reserveIncrements)
	}
}

// RequestsMetricUsesModelBReservedAndSettled 守住 requests 准入只看
// reserved_value + settled_value, request_count 只是观测镜像。Mutation: 若回到
// request_count 判定或 reserve 阶段只写 request_count, 本测试变红。
func TestServiceReserve_RequestsMetricUsesModelBReservedAndSettled(t *testing.T) {
	at := time.Date(2026, 5, 28, 16, 30, 0, 0, time.UTC)
	store := &requestMetricReserveStore{
		at: at,
		window: WindowCounter{
			TenantID:      1,
			ID:            7,
			PolicyID:      11,
			ReservedValue: decimal.RequireFromString("1"),
			SettledValue:  decimal.RequireFromString("0"),
			RequestCount:  0,
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            10,
		RequestFingerprint: "fp-request-count-only",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if err != nil {
		t.Fatalf("Reserve err=%v; want allow at reserved+settled 2/2", err)
	}
	if !result.Allowed || result.Reservation.ID == 0 {
		t.Fatalf("result=%+v; want allowed reservation", result)
	}
	if store.requestCountIncrements != 0 {
		t.Fatalf("requestCountIncrements=%d; want requests to use reserved increment path", store.requestCountIncrements)
	}
	if store.reserveIncrements != 1 {
		t.Fatalf("reserveIncrements=%d; want 1", store.reserveIncrements)
	}
	if !store.reserveDelta.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("reserveDelta=%s; want per-request delta 1", store.reserveDelta)
	}
	if store.requestCountDelta != 1 {
		t.Fatalf("requestCountDelta=%d; want mirror delta 1", store.requestCountDelta)
	}
	if !store.reserveLimit.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("reserveLimit=%s; want 2", store.reserveLimit)
	}
}

// RetryableTxConflictRetriesWholeReserve 守住 serializable/deadlock 事务中止必须
// 重跑整笔 reserve, 不能误走同 claim 唯一键冲突的 winner reread。
func TestServiceReserve_RetryableTxConflictRetriesWholeReserve(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			at := time.Date(2026, 5, 28, 17, 0, 0, 0, time.UTC)
			store := &txRetryReserveStore{
				at:       at,
				failCode: code,
				failures: 1,
			}
			result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
				TenantID:           1,
				ClaimID:            10,
				RequestFingerprint: "fp-tx-retry-" + code,
				Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
				PredictedCost:      decimal.RequireFromString("0.01"),
				LeaseExpiresAt:     at.Add(5 * time.Minute),
				At:                 at,
			})
			if err != nil {
				t.Fatalf("Reserve err=%v; want allow after bounded tx retry", err)
			}
			if !result.Allowed || result.IdempotencyHit || result.Reservation.ID == 0 {
				t.Fatalf("result=%+v; want fresh allowed reservation after whole-tx retry", result)
			}
			if store.txCalls != 2 {
				t.Fatalf("txCalls=%d; want first aborted tx plus one whole-tx retry", store.txCalls)
			}
			if store.insertCalls != 2 || store.reserveIncrements != 2 || store.auditCalls != 2 {
				t.Fatalf("calls insert=%d reserve=%d audit=%d; want full reserve body rerun twice", store.insertCalls, store.reserveIncrements, store.auditCalls)
			}
		})
	}
}

// RetryableTxConflictExhaustionReturnsRetryable 守住重试预算耗尽时向上交还
// ErrRetryable, 不把瞬时事务冲突写成 quota_fail_closed deny。
func TestServiceReserve_RetryableTxConflictExhaustionReturnsRetryable(t *testing.T) {
	at := time.Date(2026, 5, 28, 17, 30, 0, 0, time.UTC)
	const wantRetryAttempts = 3
	store := &txRetryReserveStore{
		at:       at,
		failCode: "40001",
		failures: wantRetryAttempts,
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            10,
		RequestFingerprint: "fp-tx-retry-exhausted",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if !IsRetryable(err) {
		t.Fatalf("Reserve err=%v; want ErrRetryable after tx retry exhaustion", err)
	}
	if IsDenied(err) || result.Decision.Code == decisionCodeFailClosed {
		t.Fatalf("err=%v result=%+v; want retryable, not fail-closed deny", err, result)
	}
	if store.txCalls != wantRetryAttempts {
		t.Fatalf("txCalls=%d; want retry budget %d", store.txCalls, wantRetryAttempts)
	}
}

type noTxReserveStore struct {
	called bool
}

func (s *noTxReserveStore) fail() error {
	s.called = true
	return errors.New("noTxReserveStore method executed")
}

func (s *noTxReserveStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	return nil, s.fail()
}

func (s *noTxReserveStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return WindowCounter{}, s.fail()
}

func (s *noTxReserveStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return WindowCounter{}, s.fail()
}

func (s *noTxReserveStore) IncrementWindowRequestCount(context.Context, WindowRequestCount) (WindowCounter, error) {
	return WindowCounter{}, s.fail()
}

func (s *noTxReserveStore) IncrementWindowReserved(context.Context, WindowReserve) (WindowCounter, error) {
	return WindowCounter{}, s.fail()
}

func (s *noTxReserveStore) ApplyWindowSettlement(context.Context, WindowSettlement) (WindowCounter, error) {
	return WindowCounter{}, s.fail()
}

func (s *noTxReserveStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	return Reservation{}, s.fail()
}

func (s *noTxReserveStore) InsertReservation(context.Context, ReservationInsert) (Reservation, error) {
	return Reservation{}, s.fail()
}

func (s *noTxReserveStore) ReactivateReservation(context.Context, ReservationReactivate) (Reservation, error) {
	return Reservation{}, s.fail()
}

func (s *noTxReserveStore) SettleReservation(context.Context, Settlement) error {
	return s.fail()
}

func (s *noTxReserveStore) ReleaseReservation(context.Context, ReservationRelease) error {
	return s.fail()
}

func (s *noTxReserveStore) MarkReservationReconciliationNeeded(context.Context, int64, int64, int64) error {
	return s.fail()
}

func (s *noTxReserveStore) AcquireConcurrencySlot(context.Context, ConcurrencyAcquire) (ConcurrencySlot, error) {
	return ConcurrencySlot{}, s.fail()
}

func (s *noTxReserveStore) ReleaseConcurrencySlots(context.Context, int64, int64, string) error {
	return s.fail()
}

func (s *noTxReserveStore) ExpireConcurrencySlots(context.Context, int64, time.Time) error {
	return s.fail()
}

func (s *noTxReserveStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
	return 0, s.fail()
}

func (s *noTxReserveStore) EnqueueReconciliationJob(context.Context, ReconciliationEnqueue) (ReconciliationJob, error) {
	return ReconciliationJob{}, s.fail()
}

func (s *noTxReserveStore) ListDueReconciliationJobs(context.Context, int64, time.Time, int) ([]ReconciliationJob, error) {
	return nil, s.fail()
}

func (s *noTxReserveStore) MarkReconciliationJobRunning(context.Context, int64, int64) error {
	return s.fail()
}

func (s *noTxReserveStore) CompleteReconciliationJob(context.Context, int64, int64) error {
	return s.fail()
}

func (s *noTxReserveStore) FailReconciliationJob(context.Context, ReconciliationFailure) error {
	return s.fail()
}

type claimConflictReplayStore struct {
	txCalls                int
	getCalls               int
	insertCalls            int
	requestCountIncrements int
	reserveIncrements      int
	winner                 Reservation
}

func (s *claimConflictReplayStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	s.txCalls++
	return fn(s)
}

func (s *claimConflictReplayStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	return nil, nil
}

func (s *claimConflictReplayStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return WindowCounter{}, errors.New("unexpected UpsertWindow")
}

func (s *claimConflictReplayStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return WindowCounter{}, errors.New("unexpected GetWindowForUpdate")
}

func (s *claimConflictReplayStore) IncrementWindowRequestCount(context.Context, WindowRequestCount) (WindowCounter, error) {
	s.requestCountIncrements++
	return WindowCounter{}, errors.New("unexpected IncrementWindowRequestCount")
}

func (s *claimConflictReplayStore) IncrementWindowReserved(context.Context, WindowReserve) (WindowCounter, error) {
	s.reserveIncrements++
	return WindowCounter{}, errors.New("unexpected IncrementWindowReserved")
}

func (s *claimConflictReplayStore) ApplyWindowSettlement(context.Context, WindowSettlement) (WindowCounter, error) {
	return WindowCounter{}, errors.New("unexpected ApplyWindowSettlement")
}

func (s *claimConflictReplayStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	s.getCalls++
	if s.getCalls == 1 {
		return Reservation{}, pgx.ErrNoRows
	}
	return s.winner, nil
}

func (s *claimConflictReplayStore) InsertReservation(context.Context, ReservationInsert) (Reservation, error) {
	s.insertCalls++
	return Reservation{}, &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "uq_quota_reservations_tenant_claim",
		Message:        "duplicate key value violates unique constraint",
	}
}

func (s *claimConflictReplayStore) ReactivateReservation(context.Context, ReservationReactivate) (Reservation, error) {
	return Reservation{}, errors.New("unexpected ReactivateReservation")
}

func (s *claimConflictReplayStore) SettleReservation(context.Context, Settlement) error {
	return errors.New("unexpected SettleReservation")
}

func (s *claimConflictReplayStore) ReleaseReservation(context.Context, ReservationRelease) error {
	return errors.New("unexpected ReleaseReservation")
}

func (s *claimConflictReplayStore) MarkReservationReconciliationNeeded(context.Context, int64, int64, int64) error {
	return errors.New("unexpected MarkReservationReconciliationNeeded")
}

func (s *claimConflictReplayStore) AcquireConcurrencySlot(context.Context, ConcurrencyAcquire) (ConcurrencySlot, error) {
	return ConcurrencySlot{}, errors.New("unexpected AcquireConcurrencySlot")
}

func (s *claimConflictReplayStore) ReleaseConcurrencySlots(context.Context, int64, int64, string) error {
	return errors.New("unexpected ReleaseConcurrencySlots")
}

func (s *claimConflictReplayStore) ExpireConcurrencySlots(context.Context, int64, time.Time) error {
	return errors.New("unexpected ExpireConcurrencySlots")
}

func (s *claimConflictReplayStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
	return 0, errors.New("unexpected InsertAuditEvent")
}

func (s *claimConflictReplayStore) EnqueueReconciliationJob(context.Context, ReconciliationEnqueue) (ReconciliationJob, error) {
	return ReconciliationJob{}, errors.New("unexpected EnqueueReconciliationJob")
}

func (s *claimConflictReplayStore) ListDueReconciliationJobs(context.Context, int64, time.Time, int) ([]ReconciliationJob, error) {
	return nil, errors.New("unexpected ListDueReconciliationJobs")
}

func (s *claimConflictReplayStore) MarkReconciliationJobRunning(context.Context, int64, int64) error {
	return errors.New("unexpected MarkReconciliationJobRunning")
}

func (s *claimConflictReplayStore) CompleteReconciliationJob(context.Context, int64, int64) error {
	return errors.New("unexpected CompleteReconciliationJob")
}

func (s *claimConflictReplayStore) FailReconciliationJob(context.Context, ReconciliationFailure) error {
	return errors.New("unexpected FailReconciliationJob")
}

type requestMetricReserveStore struct {
	noTxReserveStore
	at                     time.Time
	window                 WindowCounter
	requestCountIncrements int
	requestCountDelta      int64
	reserveIncrements      int
	reserveDelta           decimal.Decimal
	reserveLimit           decimal.Decimal
}

func (s *requestMetricReserveStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	return fn(s)
}

func (s *requestMetricReserveStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	return []Policy{{
		TenantID:   1,
		ID:         11,
		Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
		Metric:     MetricRequests,
		Window:     Window{Kind: WindowFixed, Seconds: 3600},
		LimitValue: decimal.NewFromInt(2),
		Mode:       ModeEnforce,
		Priority:   10,
		ValidFrom:  s.at.Add(-time.Minute),
	}}, nil
}

func (s *requestMetricReserveStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return s.window, nil
}

func (s *requestMetricReserveStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return s.window, nil
}

func (s *requestMetricReserveStore) IncrementWindowRequestCount(context.Context, WindowRequestCount) (WindowCounter, error) {
	s.requestCountIncrements++
	return WindowCounter{}, errors.New("requests metric must increment reserved_value")
}

func (s *requestMetricReserveStore) IncrementWindowReserved(_ context.Context, input WindowReserve) (WindowCounter, error) {
	s.reserveIncrements++
	s.reserveDelta = input.ReserveDelta
	s.requestCountDelta = input.RequestCountDelta
	s.reserveLimit = input.LimitValue
	s.window.ReservedValue = s.window.ReservedValue.Add(input.ReserveDelta)
	s.window.RequestCount += input.RequestCountDelta
	return s.window, nil
}

func (s *requestMetricReserveStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	return Reservation{}, pgx.ErrNoRows
}

func (s *requestMetricReserveStore) InsertReservation(context.Context, ReservationInsert) (Reservation, error) {
	return Reservation{
		TenantID:       1,
		ID:             123,
		ClaimID:        10,
		Status:         ReservationReserved,
		LeaseExpiresAt: s.at.Add(5 * time.Minute),
	}, nil
}

func (s *requestMetricReserveStore) ReactivateReservation(context.Context, ReservationReactivate) (Reservation, error) {
	return Reservation{}, errors.New("unexpected ReactivateReservation")
}

func (s *requestMetricReserveStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
	return 1, nil
}

type txRetryReserveStore struct {
	noTxReserveStore
	at                time.Time
	failCode          string
	failures          int
	txCalls           int
	getCalls          int
	insertCalls       int
	reserveIncrements int
	auditCalls        int
}

func (s *txRetryReserveStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	s.txCalls++
	if err := fn(s); err != nil {
		return err
	}
	if s.txCalls <= s.failures {
		return &pgconn.PgError{
			Code:    s.failCode,
			Message: "simulated retryable transaction conflict",
		}
	}
	return nil
}

func (s *txRetryReserveStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	return []Policy{{
		TenantID:   1,
		ID:         11,
		Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
		Metric:     MetricRequests,
		Window:     Window{Kind: WindowFixed, Seconds: 3600},
		LimitValue: decimal.NewFromInt(2),
		Mode:       ModeEnforce,
		Priority:   10,
		ValidFrom:  s.at.Add(-time.Minute),
	}}, nil
}

func (s *txRetryReserveStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return WindowCounter{
		TenantID: 1,
		ID:       7,
		PolicyID: 11,
		Window: Window{
			Kind:    WindowFixed,
			Seconds: 3600,
			Start:   s.at.Truncate(time.Hour),
			End:     s.at.Truncate(time.Hour).Add(time.Hour),
		},
		ReservedValue: decimal.Zero,
		SettledValue:  decimal.Zero,
		RequestCount:  0,
	}, nil
}

func (s *txRetryReserveStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return WindowCounter{
		TenantID:      1,
		ID:            7,
		PolicyID:      11,
		ReservedValue: decimal.Zero,
		SettledValue:  decimal.Zero,
		RequestCount:  0,
	}, nil
}

func (s *txRetryReserveStore) IncrementWindowReserved(context.Context, WindowReserve) (WindowCounter, error) {
	s.reserveIncrements++
	return WindowCounter{
		TenantID:      1,
		ID:            7,
		PolicyID:      11,
		ReservedValue: decimal.NewFromInt(1),
		SettledValue:  decimal.Zero,
		RequestCount:  1,
	}, nil
}

func (s *txRetryReserveStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	s.getCalls++
	return Reservation{}, pgx.ErrNoRows
}

func (s *txRetryReserveStore) InsertReservation(context.Context, ReservationInsert) (Reservation, error) {
	s.insertCalls++
	return Reservation{
		TenantID:       1,
		ID:             int64(100 + s.insertCalls),
		ClaimID:        10,
		Status:         ReservationReserved,
		LeaseExpiresAt: s.at.Add(5 * time.Minute),
	}, nil
}

func (s *txRetryReserveStore) ReactivateReservation(context.Context, ReservationReactivate) (Reservation, error) {
	return Reservation{}, errors.New("unexpected ReactivateReservation")
}

func (s *txRetryReserveStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
	s.auditCalls++
	return int64(s.auditCalls), nil
}
