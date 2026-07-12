package dlq

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRetryPolicyExponentialBackoffAndCap(t *testing.T) {
	policy := RetryPolicy{
		BaseBackoff: time.Second,
		CapBackoff:  5 * time.Minute,
		MaxAttempts: 10,
		DLQAfter:    15 * time.Minute,
	}
	first := time.Date(2026, 5, 15, 1, 0, 0, 0, time.UTC)
	now := first.Add(10 * time.Second)

	cases := []struct {
		previous int
		want     time.Duration
	}{
		{previous: 0, want: time.Second},
		{previous: 1, want: 2 * time.Second},
		{previous: 8, want: 256 * time.Second},
		{previous: 9},
	}
	for _, tc := range cases {
		got := policy.NextFailure(now, first, tc.previous)
		if tc.previous == 9 {
			if got.Status != StatusOperatorReview || got.Attempts != 10 {
				t.Fatalf("previous=%d status=%s attempts=%d; want operator_review/10", tc.previous, got.Status, got.Attempts)
			}
			continue
		}
		if got.Status != StatusPending || got.Delay != tc.want || !got.NextRetryAt.Equal(now.Add(tc.want)) {
			t.Fatalf("previous=%d got status=%s delay=%s next=%s", tc.previous, got.Status, got.Delay, got.NextRetryAt)
		}
	}
}

func TestRetryPolicyDLQAgeThreshold(t *testing.T) {
	policy := RetryPolicy{BaseBackoff: time.Second, CapBackoff: 5 * time.Minute, MaxAttempts: 10, DLQAfter: 15 * time.Minute}
	first := time.Date(2026, 5, 15, 1, 0, 0, 0, time.UTC)
	got := policy.NextFailure(first.Add(15*time.Minute), first, 2)
	if got.Status != StatusOperatorReview {
		t.Fatalf("status=%s want operator_review", got.Status)
	}
}

func TestServicePostDeliverySettlementFailureKeepsRetryingPastAlertThreshold(t *testing.T) {
	// 变异检查:改回通用重试决策后,超过次数或年龄阈值会停在 operator_review/quarantined。
	first := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	now := first.Add(time.Hour)
	service := NewService(nil,
		WithClock(func() time.Time { return now }),
		WithPolicy(RetryPolicy{BaseBackoff: time.Second, CapBackoff: 5 * time.Minute, MaxAttempts: 2, DLQAfter: time.Minute}),
	)
	for name, failure := range map[string]error{
		"瞬时失败": errors.New("temporary database error"),
		"结构失败": fmt.Errorf("decode: %w", ErrUnretryable),
	} {
		t.Run(name, func(t *testing.T) {
			decision := service.failureDecision(context.Background(), &Record{
				EventKind:      EventKindPostDeliverySettlement,
				FailureAt:      first,
				ReplayAttempts: 10,
			}, failure)
			if decision.Status != StatusPending {
				t.Fatalf("status=%s want pending", decision.Status)
			}
			if decision.Delay != 5*time.Minute || !decision.NextRetryAt.Equal(now.Add(5*time.Minute)) {
				t.Fatalf("delay=%v next=%v want capped retry", decision.Delay, decision.NextRetryAt)
			}
		})
	}
}

func TestLaneForKind(t *testing.T) {
	if LaneForKind(EventKindBillingEventReplica) != LaneHigh || LaneForKind(EventKindAuditEventReplica) != LaneHigh {
		t.Fatalf("billing/audit replica events must use HIGH lane")
	}
	if LaneForKind(EventKindAuditLedgerEntry) != LaneHigh {
		t.Fatalf("audit ledger entry intent must use HIGH lane")
	}
	if LaneForKind(EventKindAccountHealth) != LaneMed {
		t.Fatalf("account health must use MED lane")
	}
	if LaneForKind(EventKindMetrics) != LaneLow {
		t.Fatalf("metrics must use LOW lane")
	}
}

func TestReplicaStatusForKindAuditLedgerEntryNone(t *testing.T) {
	// 消除的风险：audit_ledger_entry 是一个主写入意图，而非副本。
	// 若它初始为 pending，MarkDelivered/MarkFailed 不会清除它，运营者会看到
	// 一个误导性的卡住副本状态。
	// 变异自检：对该 kind 返回 ReplicaStatusPending 会让本断言失败。
	if got := ReplicaStatusForKind(EventKindAuditLedgerEntry); got != ReplicaStatusNone {
		t.Fatalf("audit ledger entry replica status=%q want %q", got, ReplicaStatusNone)
	}
}

// TestNextFailureForErr_QuarantinesUnretryable 守:结构性毒消息(errors.Is 命中
// ErrUnretryable)在第 1 次失败即转 quarantined,不消耗任何重试预算。
// Mutation: 删掉 NextFailureForErr 里的 ErrUnretryable 短路分支 → 落 StatusPending
// (attempt 1 在 backoff 预算内)→ 本断言红。
func TestNextFailureForErr_QuarantinesUnretryable(t *testing.T) {
	policy := DefaultRetryPolicy()
	first := time.Date(2026, 5, 15, 1, 0, 0, 0, time.UTC)
	poison := fmt.Errorf("decode payload: %w", ErrUnretryable)
	got := policy.NextFailureForErr(first.Add(time.Second), first, 0, poison)
	if got.Status != StatusQuarantined {
		t.Fatalf("unretryable err must quarantine on attempt 1, got status=%s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("quarantine attempts=%d want=1 (must not burn retry budget)", got.Attempts)
	}
}

// TestNextFailureForErr_QuarantinesNoHandler 守:部署未注册对应 kind 的 handler 时,
// 同一条 DLQ 记录继续重试也不会自愈,必须直接进入 quarantined 等 operator 补接线。
// Mutation: 去掉 ErrNoHandler 短路 → attempt1 回到 pending → 本断言红。
func TestNextFailureForErr_QuarantinesNoHandler(t *testing.T) {
	policy := DefaultRetryPolicy()
	first := time.Date(2026, 5, 15, 1, 0, 0, 0, time.UTC)
	noHandler := fmt.Errorf("dispatch: %w", ErrNoHandler)

	got := policy.NextFailureForErr(first.Add(time.Second), first, 0, noHandler)
	if got.Status != StatusQuarantined {
		t.Fatalf("no handler err must quarantine on attempt 1, got status=%s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("quarantine attempts=%d want=1 (must not burn retry budget)", got.Attempts)
	}
}

// TestNextFailureForErr_TransientDelegates 守:非 ErrUnretryable 的瞬时错与 nil err
// 完全沿用 NextFailure 既有语义 —— 对既有调用者零行为变更,且瞬时抖动绝不被误判为
// 不可重试。Mutation: 若短路对任意非 nil err 命中 → 瞬时 case 变 quarantined → 红。
func TestNextFailureForErr_TransientDelegates(t *testing.T) {
	policy := DefaultRetryPolicy()
	first := time.Date(2026, 5, 15, 1, 0, 0, 0, time.UTC)
	now := first.Add(10 * time.Second)
	transient := errors.New("pgx: connection refused")

	got := policy.NextFailureForErr(now, first, 0, transient)
	if got.Status != StatusPending {
		t.Fatalf("transient err on attempt 1 must stay pending (retryable), got %s", got.Status)
	}

	// nil-err 路径必须逐字段等同 NextFailure。
	a := policy.NextFailureForErr(now, first, 3, nil)
	b := policy.NextFailure(now, first, 3)
	if a.Status != b.Status || a.Attempts != b.Attempts || a.Delay != b.Delay || !a.NextRetryAt.Equal(b.NextRetryAt) {
		t.Fatalf("nil-err path must equal NextFailure: %+v vs %+v", a, b)
	}

	// 达 MaxAttempts 的升级语义(operator_review)必须保留。
	esc := policy.NextFailureForErr(now, first, 9, transient)
	if esc.Status != StatusOperatorReview {
		t.Fatalf("transient err at max attempts must escalate operator_review, got %s", esc.Status)
	}
}
