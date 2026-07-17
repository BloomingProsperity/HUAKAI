package billing

import (
	"context"
	"errors"
	"expvar"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestVerifyAlreadyReleasedSlot_AllowsTerminalStatusByOperation(t *testing.T) {
	token := uuid.New()
	tests := []struct {
		name      string
		operation slotReleaseOperation
		status    string
	}{
		{name: "settle_released_lease_expired", operation: slotReleaseSettle, status: "released_lease_expired"},
		{name: "abort_released_lease_expired", operation: slotReleaseAbort, status: "released_lease_expired"},
		{name: "settle_expired", operation: slotReleaseSettle, status: "expired"},
		{name: "abort_orphan_swept", operation: slotReleaseAbort, status: "orphan_swept"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &fakePoolSlotStatusQuerier{status: tt.status}
			before := slotReleaseRecoveryMetricValue(tt.operation)

			err := verifyAlreadyReleasedSlot(context.Background(), query, token, tt.operation)
			if err != nil {
				t.Fatalf("verifyAlreadyReleasedSlot status=%q: %v", tt.status, err)
			}
			if query.calls != 1 || query.token != token {
				t.Fatalf("status query calls/token=%d/%s，want 1/%s", query.calls, query.token, token)
			}
			if delta := slotReleaseRecoveryMetricValue(tt.operation) - before; delta != 1 {
				t.Fatalf("slot_already_released metric delta=%d，want 1", delta)
			}
		})
	}
}

func TestVerifyAlreadyReleasedSlot_MissingRowFailsClosed(t *testing.T) {
	for _, operation := range []slotReleaseOperation{slotReleaseSettle, slotReleaseAbort} {
		t.Run(string(operation), func(t *testing.T) {
			query := &fakePoolSlotStatusQuerier{err: pgx.ErrNoRows}
			err := verifyAlreadyReleasedSlot(context.Background(), query, uuid.New(), operation)
			if !errors.Is(err, ErrSlotReleaseMissed) {
				t.Fatalf("missing slot err=%v，want %v", err, ErrSlotReleaseMissed)
			}
		})
	}
}

func TestVerifyAlreadyReleasedSlot_NonTerminalStatusFailsClosed(t *testing.T) {
	for _, status := range []string{"acquired", "", "releasing", "released_"} {
		t.Run(status, func(t *testing.T) {
			query := &fakePoolSlotStatusQuerier{status: status}
			err := verifyAlreadyReleasedSlot(context.Background(), query, uuid.New(), slotReleaseAbort)
			if !errors.Is(err, ErrSlotReleaseMissed) {
				t.Fatalf("status=%q err=%v，want %v", status, err, ErrSlotReleaseMissed)
			}
		})
	}
}

func TestVerifyAlreadyReleasedSlot_QueryFailureIsNotTolerated(t *testing.T) {
	want := errors.New("read failed")
	query := &fakePoolSlotStatusQuerier{err: want}
	err := verifyAlreadyReleasedSlot(context.Background(), query, uuid.New(), slotReleaseAbort)
	if !errors.Is(err, want) || errors.Is(err, ErrSlotReleaseMissed) {
		t.Fatalf("query failure err=%v，want wrapped read failure", err)
	}
}

func slotReleaseRecoveryMetricValue(operation slotReleaseOperation) int64 {
	metric, ok := expvar.Get(slotReleaseRecoveryMetricsName).(*expvar.Map)
	if !ok || metric == nil {
		return 0
	}
	value, ok := metric.Get("operation=" + string(operation) + "|outcome=" + slotReleaseOutcomeAlreadyReleased).(*expvar.Int)
	if !ok || value == nil {
		return 0
	}
	return value.Value()
}

type fakePoolSlotStatusQuerier struct {
	status string
	err    error
	calls  int
	token  uuid.UUID
}

func (q *fakePoolSlotStatusQuerier) GetPoolSlotStatusByToken(_ context.Context, token uuid.UUID) (string, error) {
	q.calls++
	q.token = token
	return q.status, q.err
}
