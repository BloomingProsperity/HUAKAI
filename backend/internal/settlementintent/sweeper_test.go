package settlementintent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type sweepMutation struct {
	id        int64
	version   int32
	status    string
	cost      decimal.Decimal
	settledAt time.Time
}

type fakeSweeperStore struct {
	mu sync.Mutex

	intents       []StaleSettlementIntent
	listErr       error
	listPanic     bool
	listCalls     int
	staleCutoff   time.Time
	createdBefore time.Time
	limit         int32
	listed        chan struct{}
	markErrs      map[int64]error
	mutations     []sweepMutation

	forwardID      int64
	forwardVersion int32
	forwardEvents  []string
}

func (s *fakeSweeperStore) Insert(_ context.Context, _ CreateParams) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forwardID == 0 {
		s.forwardID = 71001
	}
	s.forwardVersion = 0
	s.forwardEvents = append(s.forwardEvents, "pending")
	return s.forwardID, nil
}

func (s *fakeSweeperStore) MarkDelivering(_ context.Context, id int64, version int32, _ time.Time) (int32, error) {
	return s.forwardTransition(id, version, "delivering")
}

func (s *fakeSweeperStore) MarkSettling(_ context.Context, id int64, version int32, _ decimal.Decimal) (int32, error) {
	return s.forwardTransition(id, version, "settling")
}

func (s *fakeSweeperStore) MarkSettled(_ context.Context, id int64, version int32, _ decimal.Decimal, _ time.Time) (int32, error) {
	return s.forwardTransition(id, version, "settled")
}

func (s *fakeSweeperStore) MarkAborted(_ context.Context, id int64, version int32) (int32, error) {
	return s.forwardTransition(id, version, "aborted")
}

func (s *fakeSweeperStore) MarkFailed(_ context.Context, id int64, version int32, _ decimal.Decimal) (int32, error) {
	return s.forwardTransition(id, version, "failed")
}

func (s *fakeSweeperStore) forwardTransition(id int64, version int32, status string) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.forwardID || version != s.forwardVersion {
		return 0, fmt.Errorf("正向 version=%d/%d want %d/%d", id, version, s.forwardID, s.forwardVersion)
	}
	s.forwardVersion++
	s.forwardEvents = append(s.forwardEvents, status)
	return s.forwardVersion, nil
}

func (s *fakeSweeperStore) ListStaleNonTerminalSettlementIntents(_ context.Context, staleCutoff, createdBefore time.Time, limit int32) ([]StaleSettlementIntent, error) {
	s.mu.Lock()
	s.listCalls++
	s.staleCutoff = staleCutoff
	s.createdBefore = createdBefore
	s.limit = limit
	listErr := s.listErr
	listPanic := s.listPanic
	listed := s.listed
	intents := append([]StaleSettlementIntent(nil), s.intents...)
	s.mu.Unlock()
	if listed != nil {
		select {
		case listed <- struct{}{}:
		default:
		}
	}
	if listPanic {
		panic("不应泄露的扫描异常 secret-sentinel")
	}
	return intents, listErr
}

func (s *fakeSweeperStore) MarkSettledIfStale(_ context.Context, id int64, version int32, cost decimal.Decimal, settledAt time.Time) (int32, error) {
	return s.recordMutation(id, version, "settled", cost, settledAt)
}

func (s *fakeSweeperStore) MarkAbortedIfStale(_ context.Context, id int64, version int32) (int32, error) {
	return s.recordMutation(id, version, "aborted", decimal.Zero, time.Time{})
}

func (s *fakeSweeperStore) MarkSupersededIfStale(_ context.Context, id int64, version int32) (int32, error) {
	return s.recordMutation(id, version, "superseded", decimal.Zero, time.Time{})
}

func (s *fakeSweeperStore) recordMutation(id int64, version int32, status string, cost decimal.Decimal, settledAt time.Time) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.markErrs[id]; err != nil {
		return 0, err
	}
	s.mutations = append(s.mutations, sweepMutation{
		id: id, version: version, status: status, cost: cost, settledAt: settledAt,
	})
	return version + 1, nil
}

type fakeClaimAuthority struct {
	mu     sync.Mutex
	claims map[int64]ClaimSnapshot
	errs   map[int64]error
	panics map[int64]bool
	calls  []int64
}

func (a *fakeClaimAuthority) GetClaim(_ context.Context, _ int64, claimID int64) (ClaimSnapshot, error) {
	a.mu.Lock()
	a.calls = append(a.calls, claimID)
	shouldPanic := a.panics[claimID]
	claim := a.claims[claimID]
	err := a.errs[claimID]
	a.mu.Unlock()
	if shouldPanic {
		panic("不应泄露的单条异常 secret-sentinel")
	}
	return claim, err
}

// TestSettlementIntentSweeperReconcilesByAttemptAndStatus 守住 attempt proof、
// 权威金额、在途跳过和单条故障隔离。
func TestSettlementIntentSweeperReconcilesByAttemptAndStatus(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store := &fakeSweeperStore{intents: []StaleSettlementIntent{
		{ID: 1, TenantID: 10, ClaimID: 101, AttemptSeq: 1, Version: 2, Status: "delivering"},
		{ID: 2, TenantID: 10, ClaimID: 102, AttemptSeq: 1, Version: 0, Status: "pending"},
		{ID: 3, TenantID: 10, ClaimID: 103, AttemptSeq: 1, Version: 1, Status: "delivering"},
		{ID: 4, TenantID: 10, ClaimID: 104, AttemptSeq: 1, Version: 3, Status: "settling"},
		{ID: 5, TenantID: 10, ClaimID: 105, AttemptSeq: 1, Version: 0, Status: "pending"},
		{ID: 6, TenantID: 10, ClaimID: 106, AttemptSeq: 1, Version: 0, Status: "pending"},
		{ID: 7, TenantID: 10, ClaimID: 107, AttemptSeq: 1, Version: 0, Status: "pending"},
		{ID: 8, TenantID: 10, ClaimID: 108, AttemptSeq: 1, Version: 0, Status: "pending"},
	}}
	authority := &fakeClaimAuthority{
		claims: map[int64]ClaimSnapshot{
			101: {Status: "committed", AttemptSeq: 1, ActualCost: validCost("1.25000000")},
			102: {Status: "aborted", AttemptSeq: 1},
			103: {Status: "reserving", AttemptSeq: 1},
			104: {Status: "committed", AttemptSeq: 2, ActualCost: validCost("9.99000000")},
			105: {Status: "committed", AttemptSeq: 1},
			108: {Status: "committed", AttemptSeq: 1, ActualCost: validCost("0.50000000")},
		},
		errs:   map[int64]error{106: errAuthoritativeClaimNotFound},
		panics: map[int64]bool{107: true},
	}
	var logs bytes.Buffer
	worker := NewSettlementIntentSweeper(store, authority, SweeperOptions{
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})

	result, err := worker.RunOnce(context.Background(), now)
	if err == nil {
		t.Fatal("缺金额、缺 claim 与 panic 必须汇总为轮次错误")
	}
	want := SweepResult{Scanned: 8, Settled: 2, Aborted: 1, Superseded: 1, Skipped: 1, Failed: 3}
	if result != want {
		t.Fatalf("result=%+v want %+v", result, want)
	}

	store.mu.Lock()
	mutations := append([]sweepMutation(nil), store.mutations...)
	store.mu.Unlock()
	if len(mutations) != 4 {
		t.Fatalf("追平写次数=%d want 4: %+v", len(mutations), mutations)
	}
	assertSweepMutation(t, mutations[0], 1, 2, "settled")
	if !mutations[0].cost.Equal(decimal.RequireFromString("1.25000000")) || !mutations[0].settledAt.Equal(now) {
		t.Fatalf("settled 未使用权威金额或轮次时间: %+v", mutations[0])
	}
	assertSweepMutation(t, mutations[1], 2, 0, "aborted")
	// 变异证：删除 attempt_seq 比较会把 claim 104 误写 settled，本断言立即变红。
	assertSweepMutation(t, mutations[2], 4, 3, "superseded")
	if !mutations[2].cost.IsZero() {
		t.Fatalf("superseded 不得复制新 attempt 金额: %s", mutations[2].cost)
	}
	assertSweepMutation(t, mutations[3], 8, 0, "settled")
	if strings.Contains(logs.String(), "secret-sentinel") {
		t.Fatalf("panic 值泄露到日志: %s", logs.String())
	}
	for _, marker := range []string{"结算意图单条对账失败", "claim_not_found", "panic_recovered", "actual_cost"} {
		if !strings.Contains(logs.String(), marker) {
			t.Fatalf("结构化 warning 缺少 %q: %s", marker, logs.String())
		}
	}
}

// TestSettlementIntentSweeperCutoffs 守住默认宽限和可注入阈值。
func TestSettlementIntentSweeperCutoffs(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name              string
		opts              SweeperOptions
		wantStaleCutoff   time.Time
		wantCreatedBefore time.Time
		wantLimit         int32
	}{
		{
			name:              "默认保守阈值",
			wantStaleCutoff:   now.Add(-10 * time.Minute),
			wantCreatedBefore: now.Add(-10 * time.Second),
			wantLimit:         100,
		},
		{
			name: "注入阈值",
			opts: SweeperOptions{
				StaleAfter: 2 * time.Hour, CreatedGrace: 45 * time.Second, Batch: 7,
			},
			wantStaleCutoff:   now.Add(-2 * time.Hour),
			wantCreatedBefore: now.Add(-45 * time.Second),
			wantLimit:         7,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeSweeperStore{}
			worker := NewSettlementIntentSweeper(store, &fakeClaimAuthority{}, tc.opts)
			if _, err := worker.RunOnce(context.Background(), now); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if !store.staleCutoff.Equal(tc.wantStaleCutoff) {
				t.Fatalf("staleCutoff=%s want %s", store.staleCutoff, tc.wantStaleCutoff)
			}
			// 变异证：去掉 created grace 后这里会得到 now，而不是 now-10s/45s。
			if !store.createdBefore.Equal(tc.wantCreatedBefore) {
				t.Fatalf("createdBefore=%s want %s", store.createdBefore, tc.wantCreatedBefore)
			}
			if store.limit != tc.wantLimit {
				t.Fatalf("limit=%d want %d", store.limit, tc.wantLimit)
			}
		})
	}
}

// TestSettlementIntentSweeperRecoversWholeRoundPanic 守住扫描层 panic 不逃出 worker。
func TestSettlementIntentSweeperRecoversWholeRoundPanic(t *testing.T) {
	t.Run("扫描异常", func(t *testing.T) {
		store := &fakeSweeperStore{listPanic: true}
		var logs bytes.Buffer
		worker := NewSettlementIntentSweeper(store, &fakeClaimAuthority{}, SweeperOptions{
			Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		})
		result, err := worker.RunOnce(context.Background(), time.Now())
		if !errors.Is(err, errSweepPanicked) || result.Failed != 1 {
			t.Fatalf("panic result=%+v err=%v", result, err)
		}
		if strings.Contains(logs.String(), "secret-sentinel") {
			t.Fatalf("扫描 panic 值泄露到日志: %s", logs.String())
		}
	})

	t.Run("注入时钟异常", func(t *testing.T) {
		worker := NewSettlementIntentSweeper(&fakeSweeperStore{}, &fakeClaimAuthority{}, SweeperOptions{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Now: func() time.Time {
				panic("不应泄露的时钟异常 secret-sentinel")
			},
		})
		result, err := worker.RunOnce(context.Background(), time.Time{})
		if !errors.Is(err, errSweepPanicked) || result.Failed != 1 {
			t.Fatalf("时钟 panic result=%+v err=%v", result, err)
		}
	})
}

// TestSettlementIntentSweeperStartStop 守住 ticker 启动和幂等停止。
func TestSettlementIntentSweeperStartStop(t *testing.T) {
	listed := make(chan struct{}, 1)
	store := &fakeSweeperStore{listed: listed}
	var nowCalls atomic.Int32
	worker := NewSettlementIntentSweeper(store, &fakeClaimAuthority{}, SweeperOptions{
		Interval: time.Millisecond,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time {
			if nowCalls.Add(1) == 1 {
				panic("首轮时钟异常")
			}
			return time.Now().UTC()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	select {
	case <-listed:
	case <-time.After(time.Second):
		t.Fatal("首轮 panic 后 ticker 未继续触发 RunOnce")
	}
	if nowCalls.Load() < 2 {
		t.Fatalf("时钟调用=%d want >=2", nowCalls.Load())
	}
	worker.Stop()
	worker.Stop()
}

// TestSettlementIntentForwardLifecycleUnchanged 守住阶段 1 Tracker 仍只调用原 Mark*。
func TestSettlementIntentForwardLifecycleUnchanged(t *testing.T) {
	store := &fakeSweeperStore{forwardID: 71001}
	tracker := NewTracker(store)
	ctx := context.Background()
	cost := decimal.RequireFromString("0.75000000")
	tracker.InsertPending(ctx, 10, "request-1", "logical-1", 101, 1, 20, "fingerprint", decimal.NewFromInt(1))
	tracker.MarkDelivering(ctx, time.Now())
	tracker.MarkSettling(ctx, cost)
	tracker.MarkSettled(ctx, cost)

	store.mu.Lock()
	defer store.mu.Unlock()
	want := []string{"pending", "delivering", "settling", "settled"}
	if fmt.Sprint(store.forwardEvents) != fmt.Sprint(want) {
		t.Fatalf("阶段 1 生命周期=%v want %v", store.forwardEvents, want)
	}
	if store.forwardVersion != 3 {
		t.Fatalf("阶段 1 version=%d want 3", store.forwardVersion)
	}
	if len(store.mutations) != 0 {
		t.Fatalf("阶段 1 不得调用 sweeper 写: %+v", store.mutations)
	}
}

type fakeClaimByIDQuerier struct {
	arg dbbilling.GetClaimByIDParams
	row dbbilling.GetClaimByIDRow
	err error
}

func (q *fakeClaimByIDQuerier) GetClaimByID(_ context.Context, arg dbbilling.GetClaimByIDParams) (dbbilling.GetClaimByIDRow, error) {
	q.arg = arg
	return q.row, q.err
}

// TestPostgresClaimAuthorityNarrowsTenantScopedLookup 守住 adapter 只转交租户、标识和三项权威字段。
func TestPostgresClaimAuthorityNarrowsTenantScopedLookup(t *testing.T) {
	querier := &fakeClaimByIDQuerier{row: dbbilling.GetClaimByIDRow{
		Status: "committed", AttemptSeq: 3, ActualCost: validCost("2.50000000"),
	}}
	authority := NewPostgresClaimAuthority(querier)
	got, err := authority.GetClaim(context.Background(), 17, 29)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if querier.arg.TenantID != 17 || querier.arg.ID != 29 {
		t.Fatalf("tenant-scoped 参数=%+v", querier.arg)
	}
	if got.Status != "committed" || got.AttemptSeq != 3 || !got.ActualCost.Valid || !got.ActualCost.Decimal.Equal(decimal.RequireFromString("2.50000000")) {
		t.Fatalf("claim snapshot=%+v", got)
	}

	querier.err = pgx.ErrNoRows
	if _, err := authority.GetClaim(context.Background(), 17, 30); !errors.Is(err, errAuthoritativeClaimNotFound) {
		t.Fatalf("缺失 claim err=%v want errAuthoritativeClaimNotFound", err)
	}
}

func validCost(value string) decimal.NullDecimal {
	return decimal.NullDecimal{Decimal: decimal.RequireFromString(value), Valid: true}
}

func assertSweepMutation(t *testing.T, got sweepMutation, id int64, version int32, status string) {
	t.Helper()
	if got.id != id || got.version != version || got.status != status {
		t.Fatalf("mutation=%+v want id=%d version=%d status=%s", got, id, version, status)
	}
}
