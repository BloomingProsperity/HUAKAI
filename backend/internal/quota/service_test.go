package quota

import (
	"context"
	"encoding/json"
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
	fp := "fp-claim-race"
	scopes := []Scope{{TenantID: 1, Kind: ScopeGlobal, ID: "*"}}
	cost := decimal.RequireFromString("1")
	store := &claimConflictReplayStore{
		winner: Reservation{
			TenantID:           1,
			ID:                 99,
			ClaimID:            10,
			RequestFingerprint: fp,
			Scopes:             scopes,
			PredictedCost:      cost,
			Status:             ReservationReserved,
			LeaseExpiresAt:     at.Add(5 * time.Minute),
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            10,
		RequestFingerprint: fp,
		Scopes:             scopes,
		PredictedCost:      cost,
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

func TestServiceReserve_ExistingClaimRejectsFingerprintMismatch(t *testing.T) {
	at := time.Date(2026, 5, 28, 16, 30, 0, 0, time.UTC)
	store := &claimConflictReplayStore{
		returnWinnerImmediately: true,
		winner: Reservation{
			TenantID:           1,
			ID:                 100,
			ClaimID:            11,
			RequestFingerprint: "fp-original",
			Scopes:             []Scope{{TenantID: 1, Kind: ScopeGlobal, ID: "*"}},
			PredictedCost:      decimal.RequireFromString("1"),
			Status:             ReservationReserved,
			LeaseExpiresAt:     at.Add(5 * time.Minute),
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            11,
		RequestFingerprint: "fp-mutated",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeGlobal, ID: "*"}},
		PredictedCost:      decimal.RequireFromString("1"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if !errors.Is(err, ErrReservationReplayConflict) {
		t.Fatalf("Reserve err=%v want ErrReservationReplayConflict", err)
	}
	if result.Allowed {
		t.Fatalf("result=%+v want denied replay conflict", result)
	}
	if store.insertCalls != 0 || store.requestCountIncrements != 0 || store.reserveIncrements != 0 {
		t.Fatalf("side effects insert=%d request=%d reserve=%d; replay conflict must not create or increment",
			store.insertCalls, store.requestCountIncrements, store.reserveIncrements)
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

func TestServiceReserve_NoTokenPolicyDoesNotApplyTokenEstimate(t *testing.T) {
	// Mutation check: mistakenly applying ReservedTokens to the requests metric
	// would deny or reserve 50000 request units. With no token policy configured,
	// TPD estimates must have zero effect on request quota behavior.
	at := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	store := &requestMetricReserveStore{
		at: at,
		window: WindowCounter{
			TenantID:      1,
			ID:            8,
			PolicyID:      12,
			ReservedValue: decimal.NewFromInt(1),
			SettledValue:  decimal.Zero,
			RequestCount:  0,
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            12,
		RequestFingerprint: "fp-no-token-policy",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
		ReservedTokens:     50000,
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if err != nil {
		t.Fatalf("Reserve err=%v; want request quota allow without token policy", err)
	}
	if !result.Allowed {
		t.Fatalf("result=%+v; want allowed without token policy", result)
	}
	if !store.reserveDelta.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("request reserveDelta=%s want 1, not token estimate", store.reserveDelta)
	}
	if !store.insertReservedUnits.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("reservation ReservedUnits=%s want 1 without token policy", store.insertReservedUnits)
	}
}

func TestServiceReserve_TokensEstimatedEnforceDeniesWhenEstimateExceedsLimit(t *testing.T) {
	// Mutation check: restoring the old MetricTokensEstimated skip lets this
	// request through and inserts a reservation; the deny and insert assertions
	// must turn red.
	at := time.Date(2026, 6, 4, 10, 30, 0, 0, time.UTC)
	store := &tokenMetricReserveStore{
		at:    at,
		limit: decimal.NewFromInt(100),
		window: WindowCounter{
			TenantID:      1,
			ID:            9,
			PolicyID:      13,
			ReservedValue: decimal.NewFromInt(80),
			SettledValue:  decimal.Zero,
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            13,
		RequestFingerprint: "fp-token-deny",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
		ReservedTokens:     25,
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if !IsDenied(err) {
		t.Fatalf("Reserve err=%v; want token quota deny", err)
	}
	if result.Allowed || result.Decision.Metric != MetricTokensEstimated {
		t.Fatalf("result=%+v; want tokens_estimated deny", result)
	}
	if store.insertCalls != 0 {
		t.Fatalf("insertCalls=%d want 0 on token quota deny", store.insertCalls)
	}
	if store.reserveIncrements != 0 {
		t.Fatalf("reserveIncrements=%d want 0 because deny happens before apply", store.reserveIncrements)
	}
}

func TestServiceReserve_TokensEstimatedAllowReservesEstimateAndSnapshotsAmount(t *testing.T) {
	// Mutation check: reserving 1 token instead of the estimate leaves
	// reserveDelta=1 and snapshot reserved_amount=1; both assertions must fail.
	at := time.Date(2026, 6, 4, 10, 45, 0, 0, time.UTC)
	store := &tokenMetricReserveStore{
		at:    at,
		limit: decimal.NewFromInt(100),
		window: WindowCounter{
			TenantID:      1,
			ID:            11,
			PolicyID:      15,
			ReservedValue: decimal.NewFromInt(10),
			SettledValue:  decimal.Zero,
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            15,
		RequestFingerprint: "fp-token-allow",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
		ReservedTokens:     25,
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if err != nil {
		t.Fatalf("Reserve err=%v; want allow under token limit", err)
	}
	if !result.Allowed {
		t.Fatalf("result=%+v; want allowed under token limit", result)
	}
	if !store.reserveDelta.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("token reserveDelta=%s want estimate 25", store.reserveDelta)
	}
	if store.requestCountDelta != 0 {
		t.Fatalf("token requestCountDelta=%d want 0", store.requestCountDelta)
	}
	if !store.insertReservedUnits.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("reservation ReservedUnits=%s want request-unit 1", store.insertReservedUnits)
	}
	var records []policySnapshotRecord
	if err := json.Unmarshal(store.policySnapshot, &records); err != nil {
		t.Fatalf("unmarshal policy snapshot: %v", err)
	}
	if len(records) != 1 || records[0].Metric != string(MetricTokensEstimated) || records[0].ReservedAmount != "25" {
		t.Fatalf("snapshot=%+v want one tokens_estimated record with reserved_amount=25", records)
	}
}

func TestServiceReserve_TokensEstimatedZeroEstimateSkipsWithoutDeny(t *testing.T) {
	// Mutation check: treating missing estimates as 1 token would deny against
	// this already-full token window. Zero estimate must preserve the current
	// observe/skip behavior and allow the hot path.
	at := time.Date(2026, 6, 4, 11, 0, 0, 0, time.UTC)
	store := &tokenMetricReserveStore{
		at:    at,
		limit: decimal.NewFromInt(1),
		window: WindowCounter{
			TenantID:      1,
			ID:            10,
			PolicyID:      14,
			ReservedValue: decimal.NewFromInt(1),
			SettledValue:  decimal.Zero,
		},
	}
	result, err := NewService(store).Reserve(context.Background(), ReserveRequest{
		TenantID:           1,
		ClaimID:            14,
		RequestFingerprint: "fp-token-zero-estimate",
		Scopes:             []Scope{{TenantID: 1, Kind: ScopeUser, ID: "42"}},
		PredictedCost:      decimal.RequireFromString("0.01"),
		LeaseExpiresAt:     at.Add(5 * time.Minute),
		At:                 at,
	})
	if err != nil {
		t.Fatalf("Reserve err=%v; want allow when token estimate is absent", err)
	}
	if !result.Allowed {
		t.Fatalf("result=%+v; want allowed with zero token estimate", result)
	}
	if store.reserveIncrements != 0 || store.upsertCalls != 0 {
		t.Fatalf("token window calls reserve=%d upsert=%d; want 0 when estimate is absent", store.reserveIncrements, store.upsertCalls)
	}
	if store.insertCalls != 1 {
		t.Fatalf("insertCalls=%d want 1 reservation after token policy skipped", store.insertCalls)
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

func TestServiceSettle_ZeroReservationIDResolvesByClaim(t *testing.T) {
	store := &claimFinalizationStore{reservation: Reservation{
		TenantID: 1,
		ID:       77,
		ClaimID:  10,
		Status:   ReservationSettled,
	}}

	result, err := NewService(store).Settle(context.Background(), SettleRequest{
		TenantID:   1,
		ClaimID:    10,
		ActualCost: decimal.RequireFromString("0.25"),
	})

	if err != nil {
		t.Fatalf("Settle with ReservationID=0: %v", err)
	}
	if !result.IdempotencyHit || result.Reservation.ID != 77 {
		t.Fatalf("result=%+v want idempotent settled reservation 77 resolved by claim", result)
	}
	if store.getCalls != 1 {
		t.Fatalf("GetReservationByClaimForUpdate calls=%d want 1", store.getCalls)
	}
}

func TestServiceRelease_ZeroReservationIDResolvesByClaim(t *testing.T) {
	store := &claimFinalizationStore{reservation: Reservation{
		TenantID: 1,
		ID:       78,
		ClaimID:  11,
		Status:   ReservationReleased,
	}}

	result, err := NewService(store).Release(context.Background(), ReleaseRequest{
		TenantID: 1,
		ClaimID:  11,
		Reason:   "abort",
	})

	if err != nil {
		t.Fatalf("Release with ReservationID=0: %v", err)
	}
	if !result.IdempotencyHit || result.Reservation.ID != 78 {
		t.Fatalf("result=%+v want idempotent released reservation 78 resolved by claim", result)
	}
	if store.getCalls != 1 {
		t.Fatalf("GetReservationByClaimForUpdate calls=%d want 1", store.getCalls)
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
	txCalls                 int
	getCalls                int
	insertCalls             int
	requestCountIncrements  int
	reserveIncrements       int
	winner                  Reservation
	returnWinnerImmediately bool
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
	if s.returnWinnerImmediately {
		return s.winner, nil
	}
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

type claimFinalizationStore struct {
	noTxReserveStore
	reservation Reservation
	getCalls    int
}

func (s *claimFinalizationStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	return fn(s)
}

func (s *claimFinalizationStore) GetReservationByClaimForUpdate(_ context.Context, tenantID int64, claimID int64) (Reservation, error) {
	s.getCalls++
	if tenantID != s.reservation.TenantID || claimID != s.reservation.ClaimID {
		return Reservation{}, pgx.ErrNoRows
	}
	return s.reservation, nil
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
	insertReservedUnits    decimal.Decimal
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

func (s *requestMetricReserveStore) InsertReservation(_ context.Context, input ReservationInsert) (Reservation, error) {
	s.insertReservedUnits = input.ReservedUnits
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

type tokenMetricReserveStore struct {
	noTxReserveStore
	at                  time.Time
	limit               decimal.Decimal
	window              WindowCounter
	upsertCalls         int
	insertCalls         int
	reserveIncrements   int
	reserveDelta        decimal.Decimal
	requestCountDelta   int64
	insertReservedUnits decimal.Decimal
	policySnapshot      []byte
}

func (s *tokenMetricReserveStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	return fn(s)
}

func (s *tokenMetricReserveStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	limit := s.limit
	if limit.IsZero() {
		limit = decimal.NewFromInt(100)
	}
	return []Policy{{
		TenantID:   1,
		ID:         s.window.PolicyID,
		Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
		Metric:     MetricTokensEstimated,
		Window:     Window{Kind: WindowCalendarDay},
		LimitValue: limit,
		Mode:       ModeEnforce,
		Priority:   10,
		ValidFrom:  s.at.Add(-time.Minute),
	}}, nil
}

func (s *tokenMetricReserveStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	s.upsertCalls++
	return s.window, nil
}

func (s *tokenMetricReserveStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return s.window, nil
}

func (s *tokenMetricReserveStore) IncrementWindowReserved(_ context.Context, input WindowReserve) (WindowCounter, error) {
	s.reserveIncrements++
	s.reserveDelta = input.ReserveDelta
	s.requestCountDelta = input.RequestCountDelta
	s.window.ReservedValue = s.window.ReservedValue.Add(input.ReserveDelta)
	s.window.RequestCount += input.RequestCountDelta
	return s.window, nil
}

func (s *tokenMetricReserveStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	return Reservation{}, pgx.ErrNoRows
}

func (s *tokenMetricReserveStore) InsertReservation(_ context.Context, input ReservationInsert) (Reservation, error) {
	s.insertCalls++
	s.insertReservedUnits = input.ReservedUnits
	s.policySnapshot = input.PolicySnapshot
	return Reservation{
		TenantID:       1,
		ID:             124,
		ClaimID:        14,
		Status:         ReservationReserved,
		LeaseExpiresAt: s.at.Add(5 * time.Minute),
	}, nil
}

func (s *tokenMetricReserveStore) ReactivateReservation(context.Context, ReservationReactivate) (Reservation, error) {
	return Reservation{}, errors.New("unexpected ReactivateReservation")
}

func (s *tokenMetricReserveStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
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
