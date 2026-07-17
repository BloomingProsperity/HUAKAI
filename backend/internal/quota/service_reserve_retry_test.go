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

type reserveConflictPoint string

const (
	reserveConflictAtReservationRead reserveConflictPoint = "reservation_read"
	reserveConflictAtNewPolicyRead   reserveConflictPoint = "new_policy_read"
	reserveConflictAtRevivePolicy    reserveConflictPoint = "revive_policy_read"
)

// AT-CD1-001:reservation 读取遇到瞬时冲突时必须重跑整笔事务，最终只产生一次可见写入。
func TestServiceReserve_ATCD1001_RetryableReservationReadConflict(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			req := reserveConflictTestRequest()
			store := newReserveConflictStore(req, reserveConflictAtReservationRead, pgTxConflict(code), 1, "")

			result, err := NewService(store).Reserve(context.Background(), req)

			assertReserveConflictAllowed(t, result, err, false)
			assertReserveConflictCalls(t, store, 2, 2, 1, 1, 0)
		})
	}
}

// AT-CD1-002:新 claim 的策略读取冲突不得伪装成永久拒绝，回滚后必须从 reservation 读取重新开始。
func TestServiceReserve_ATCD1002_RetryableNewPolicyConflict(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			req := reserveConflictTestRequest()
			store := newReserveConflictStore(req, reserveConflictAtNewPolicyRead, pgTxConflict(code), 1, "")

			result, err := NewService(store).Reserve(context.Background(), req)

			assertReserveConflictAllowed(t, result, err, false)
			assertReserveConflictCalls(t, store, 2, 2, 2, 1, 0)
		})
	}
}

// AT-CD1-003:released/expired reservation 复活时的策略冲突也必须重跑整笔事务。
func TestServiceReserve_ATCD1003_RetryableRevivePolicyConflict(t *testing.T) {
	for _, status := range []ReservationStatus{ReservationReleased, ReservationExpired} {
		for _, code := range []string{"40001", "40P01"} {
			t.Run(string(status)+"_"+code, func(t *testing.T) {
				req := reserveConflictTestRequest()
				store := newReserveConflictStore(req, reserveConflictAtRevivePolicy, pgTxConflict(code), 1, status)

				result, err := NewService(store).Reserve(context.Background(), req)

				assertReserveConflictAllowed(t, result, err, true)
				if result.Reservation.Status != ReservationReserved {
					t.Fatalf("Reservation.Status=%q; want %q", result.Reservation.Status, ReservationReserved)
				}
				assertReserveConflictCalls(t, store, 2, 2, 2, 0, 1)
			})
		}
	}
}

// AT-CD1-004:三个吞错点的瞬时冲突耗尽预算后必须返回 retryable，且不得留下业务写入或审计。
func TestServiceReserve_ATCD1004_RetryableConflictExhaustion(t *testing.T) {
	for _, point := range []reserveConflictPoint{
		reserveConflictAtReservationRead,
		reserveConflictAtNewPolicyRead,
		reserveConflictAtRevivePolicy,
	} {
		for _, code := range []string{"40001", "40P01"} {
			t.Run(string(point)+"_"+code, func(t *testing.T) {
				req := reserveConflictTestRequest()
				status := ReservationStatus("")
				if point == reserveConflictAtRevivePolicy {
					status = ReservationReleased
				}
				store := newReserveConflictStore(req, point, pgTxConflict(code), reserveTxRetryAttempts, status)

				result, err := NewService(store).Reserve(context.Background(), req)

				if !IsRetryable(err) || IsDenied(err) {
					t.Fatalf("err=%v; want retryable=true denied=false", err)
				}
				var retryable *RetryableError
				if !errors.As(err, &retryable) || retryable.Operation != "reserve transaction" {
					t.Fatalf("err=%T %v; want RetryableError operation=reserve transaction", err, err)
				}
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != code {
					t.Fatalf("err=%v; want underlying SQLSTATE %s", err, code)
				}
				if result.Allowed || result.Decision.Code != "" || result.Reservation.ID != 0 || result.IdempotencyHit {
					t.Fatalf("result=%+v; want empty result after retry exhaustion", result)
				}
				if store.txCalls != reserveTxRetryAttempts {
					t.Fatalf("txCalls=%d; want %d", store.txCalls, reserveTxRetryAttempts)
				}
				assertReserveConflictNoWrites(t, store)
			})
		}
	}
}

// AT-CD1-005:普通错误和非重试 PG 状态仍须一次事务内 fail-closed，不能扩大重试集合。
func TestServiceReserve_ATCD1005_NonRetryableErrorsStayFailClosed(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "ordinary", err: errors.New("quota policy read failed")},
		{name: "23514", err: &pgconn.PgError{Code: "23514", Message: "simulated check violation"}},
	}
	for _, point := range []reserveConflictPoint{
		reserveConflictAtReservationRead,
		reserveConflictAtNewPolicyRead,
		reserveConflictAtRevivePolicy,
	} {
		for _, tc := range tests {
			t.Run(string(point)+"_"+tc.name, func(t *testing.T) {
				req := reserveConflictTestRequest()
				status := ReservationStatus("")
				if point == reserveConflictAtRevivePolicy {
					status = ReservationExpired
				}
				store := newReserveConflictStore(req, point, tc.err, 1, status)

				result, err := NewService(store).Reserve(context.Background(), req)

				if !IsDenied(err) || IsRetryable(err) {
					t.Fatalf("err=%v; want denied=true retryable=false", err)
				}
				var deny *DenyError
				if !errors.As(err, &deny) || deny.Decision.Code != decisionCodeFailClosed || !errors.Is(err, tc.err) {
					t.Fatalf("err=%T %v; want DenyError code=%s cause=%v", err, err, decisionCodeFailClosed, tc.err)
				}
				if result.Allowed || result.Decision.Kind != DecisionDeny || result.Decision.Code != decisionCodeFailClosed {
					t.Fatalf("result=%+v; want exact fail-closed deny", result)
				}
				if store.txCalls != 1 {
					t.Fatalf("txCalls=%d; want 1 for non-retryable error", store.txCalls)
				}
				assertReserveConflictNoWrites(t, store)
			})
		}
	}
}

type reserveConflictStore struct {
	noTxReserveStore
	at                time.Time
	point             reserveConflictPoint
	failure           error
	failuresRemaining int
	existing          Reservation
	txCalls           int
	getCalls          int
	policyCalls       int
	upsertCalls       int
	windowReadCalls   int
	insertCalls       int
	reactivateCalls   int
	reserveIncrements int
	audits            []AuditEvent
}

func newReserveConflictStore(req ReserveRequest, point reserveConflictPoint, failure error, failures int, status ReservationStatus) *reserveConflictStore {
	store := &reserveConflictStore{
		at:                req.At,
		point:             point,
		failure:           failure,
		failuresRemaining: failures,
	}
	if status != "" {
		store.existing = Reservation{
			TenantID:           req.TenantID,
			ID:                 71,
			ClaimID:            req.ClaimID,
			RequestFingerprint: req.RequestFingerprint,
			Scopes:             req.Scopes,
			PredictedCost:      req.PredictedCost,
			ReservedUnits:      decimal.NewFromInt(1),
			Status:             status,
			LeaseExpiresAt:     req.LeaseExpiresAt,
		}
	}
	return store
}

func (s *reserveConflictStore) WithTx(ctx context.Context, fn func(PGStore) error) error {
	s.txCalls++
	return fn(s)
}

func (s *reserveConflictStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	s.getCalls++
	if s.point == reserveConflictAtReservationRead && s.consumeFailure() {
		return Reservation{}, s.failure
	}
	if s.existing.ID != 0 {
		return s.existing, nil
	}
	return Reservation{}, pgx.ErrNoRows
}

func (s *reserveConflictStore) ListActivePolicies(context.Context, PolicyFilter) ([]Policy, error) {
	s.policyCalls++
	if s.point != reserveConflictAtReservationRead && s.consumeFailure() {
		return nil, s.failure
	}
	return []Policy{{
		TenantID:   1,
		ID:         51,
		Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
		Metric:     MetricRequests,
		Window:     Window{Kind: WindowFixed, Seconds: 3600},
		LimitValue: decimal.NewFromInt(10),
		Mode:       ModeEnforce,
		Priority:   10,
		ValidFrom:  s.at.Add(-time.Hour),
	}}, nil
}

func (s *reserveConflictStore) consumeFailure() bool {
	if s.failuresRemaining <= 0 {
		return false
	}
	s.failuresRemaining--
	return true
}

func (s *reserveConflictStore) UpsertWindow(_ context.Context, input WindowUpsert) (WindowCounter, error) {
	s.upsertCalls++
	return WindowCounter{TenantID: input.TenantID, ID: 61, PolicyID: input.PolicyID, Window: input.Window}, nil
}

func (s *reserveConflictStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	s.windowReadCalls++
	return WindowCounter{TenantID: 1, ID: 61, PolicyID: 51}, nil
}

func (s *reserveConflictStore) InsertReservation(_ context.Context, input ReservationInsert) (Reservation, error) {
	s.insertCalls++
	return Reservation{
		TenantID:           input.TenantID,
		ID:                 72,
		ClaimID:            input.ClaimID,
		RequestFingerprint: input.RequestFingerprint,
		Scopes:             input.Scopes,
		PolicySnapshot:     input.PolicySnapshot,
		PredictedCost:      input.PredictedCost,
		ReservedUnits:      input.ReservedUnits,
		Status:             ReservationReserved,
		LeaseExpiresAt:     input.LeaseExpiresAt,
	}, nil
}

func (s *reserveConflictStore) ReactivateReservation(_ context.Context, input ReservationReactivate) (Reservation, error) {
	s.reactivateCalls++
	return Reservation{
		TenantID:           input.TenantID,
		ID:                 input.ReservationID,
		ClaimID:            input.ClaimID,
		RequestFingerprint: input.RequestFingerprint,
		Scopes:             input.Scopes,
		PolicySnapshot:     input.PolicySnapshot,
		PredictedCost:      input.PredictedCost,
		ReservedUnits:      input.ReservedUnits,
		Status:             ReservationReserved,
		LeaseExpiresAt:     input.LeaseExpiresAt,
	}, nil
}

func (s *reserveConflictStore) IncrementWindowReserved(_ context.Context, input WindowReserve) (WindowCounter, error) {
	s.reserveIncrements++
	return WindowCounter{
		TenantID:      input.TenantID,
		ID:            input.WindowID,
		PolicyID:      51,
		ReservedValue: input.ReserveDelta,
		RequestCount:  input.RequestCountDelta,
	}, nil
}

func (s *reserveConflictStore) InsertAuditEvent(_ context.Context, event AuditEvent) (int64, error) {
	s.audits = append(s.audits, event)
	return int64(len(s.audits)), nil
}

func (s *reserveConflictStore) auditCount(eventType, decisionCode string) int {
	count := 0
	for _, event := range s.audits {
		if (eventType == "" || event.EventType == eventType) && event.DecisionCode == decisionCode {
			count++
		}
	}
	return count
}

func reserveConflictTestRequest() ReserveRequest {
	at := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	return ReserveRequest{
		TenantID:           1,
		ClaimID:            41,
		RequestFingerprint: "at-cd1-reserve",
		Scopes: []Scope{
			{TenantID: 1, Kind: ScopeGlobal, ID: "*"},
			{TenantID: 1, Kind: ScopeUser, ID: "42"},
		},
		PredictedCost:  decimal.RequireFromString("0.01"),
		LeaseExpiresAt: at.Add(5 * time.Minute),
		At:             at,
	}
}

func pgTxConflict(code string) error {
	return &pgconn.PgError{Code: code, Message: "simulated transaction conflict"}
}

func assertReserveConflictAllowed(t *testing.T, result ReserveResult, err error, wantIdempotencyHit bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("Reserve err=%v; want nil", err)
	}
	if !result.Allowed || result.Decision.Kind != DecisionAllow || result.Decision.Code != decisionCodeAllowed {
		t.Fatalf("result=%+v; want Allowed=true Kind=%q Code=%q", result, DecisionAllow, decisionCodeAllowed)
	}
	if result.IdempotencyHit != wantIdempotencyHit {
		t.Fatalf("IdempotencyHit=%v; want %v", result.IdempotencyHit, wantIdempotencyHit)
	}
}

func assertReserveConflictCalls(t *testing.T, store *reserveConflictStore, tx, get, policy, inserts, reactivations int) {
	t.Helper()
	if store.txCalls != tx || store.getCalls != get || store.policyCalls != policy {
		t.Fatalf("calls tx=%d get=%d policy=%d; want %d/%d/%d", store.txCalls, store.getCalls, store.policyCalls, tx, get, policy)
	}
	if store.insertCalls != inserts || store.reactivateCalls != reactivations {
		t.Fatalf("reservation writes insert=%d reactivate=%d; want %d/%d", store.insertCalls, store.reactivateCalls, inserts, reactivations)
	}
	if store.upsertCalls != 1 || store.windowReadCalls != 1 || store.reserveIncrements != 1 {
		t.Fatalf("window calls upsert=%d read=%d increment=%d; want 1/1/1", store.upsertCalls, store.windowReadCalls, store.reserveIncrements)
	}
	if len(store.audits) != 1 || store.auditCount("reserve_allowed", decisionCodeAllowed) != 1 || store.auditCount("", decisionCodeFailClosed) != 0 {
		t.Fatalf("audits=%+v; want one reserve_allowed and zero quota_fail_closed", store.audits)
	}
	if store.called {
		t.Fatal("unexpected PGStore method was called")
	}
}

func assertReserveConflictNoWrites(t *testing.T, store *reserveConflictStore) {
	t.Helper()
	if store.insertCalls != 0 || store.reactivateCalls != 0 || store.upsertCalls != 0 || store.windowReadCalls != 0 || store.reserveIncrements != 0 || len(store.audits) != 0 {
		t.Fatalf("writes insert=%d reactivate=%d upsert=%d read=%d increment=%d audits=%d; want all zero", store.insertCalls, store.reactivateCalls, store.upsertCalls, store.windowReadCalls, store.reserveIncrements, len(store.audits))
	}
	if store.called {
		t.Fatal("unexpected PGStore method was called")
	}
}
