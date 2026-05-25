package dlq

import (
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
	// Risk killed: audit_ledger_entry is a primary write intent, not a replica.
	// If it starts as pending, MarkDelivered/MarkFailed will not clear it and
	// operators see a misleading stuck replica state.
	// Mutation self-check: returning ReplicaStatusPending for this kind makes
	// this assertion fail.
	if got := ReplicaStatusForKind(EventKindAuditLedgerEntry); got != ReplicaStatusNone {
		t.Fatalf("audit ledger entry replica status=%q want %q", got, ReplicaStatusNone)
	}
}
