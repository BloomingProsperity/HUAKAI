package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/workerpulse"
)

type leaseStub struct {
	acquired bool
	err      error
	releases int
}

func (l *leaseStub) TryAcquire(context.Context) (bool, func(), error) {
	if l.err != nil || !l.acquired {
		return l.acquired, nil, l.err
	}
	return true, func() { l.releases++ }, nil
}

func TestGuardedTickFollowerDoesNotRunWork(t *testing.T) {
	store := workerpulse.NewMemoryStore()
	ran := 0
	executed, err := runGuardedTick(context.Background(), &leaseStub{}, store, "node-b", workerpulse.JobSubscriptionExpiry, func() error {
		ran++
		return nil
	})
	if executed || err != nil || ran != 0 {
		t.Fatalf("executed=%v err=%v ran=%d", executed, err, ran)
	}
	rows, _ := store.List(context.Background(), workerpulse.JobSubscriptionExpiry)
	if len(rows) != 1 || rows[0].Executor || rows[0].ReplicaID != "node-b" {
		t.Fatalf("follower pulse=%+v", rows)
	}
}

func TestGuardedTickLeaseErrorFailsClosed(t *testing.T) {
	store := workerpulse.NewMemoryStore()
	ran := 0
	executed, err := runGuardedTick(context.Background(), &leaseStub{err: errors.New("lock store down")}, store, "node-a", workerpulse.JobSubscriptionExpiry, func() error {
		ran++
		return nil
	})
	if executed || err == nil || ran != 0 {
		t.Fatalf("executed=%v err=%v ran=%d", executed, err, ran)
	}
	rows, _ := store.List(context.Background(), workerpulse.JobSubscriptionExpiry)
	if len(rows) != 1 || rows[0].Executor || rows[0].LastError == "" {
		t.Fatalf("lease error pulse=%+v", rows)
	}
}

func TestGuardedTickExecutorRecordsSuccess(t *testing.T) {
	store := workerpulse.NewMemoryStore()
	lease := &leaseStub{acquired: true}
	executed, err := runGuardedTick(context.Background(), lease, store, "node-a", workerpulse.JobSubscriptionExpiry, func() error {
		return nil
	})
	if !executed || err != nil || lease.releases != 1 {
		t.Fatalf("executed=%v err=%v releases=%d", executed, err, lease.releases)
	}
	rows, _ := store.List(context.Background(), workerpulse.JobSubscriptionExpiry)
	if len(rows) != 1 || !rows[0].Executor || rows[0].SuccessAt.IsZero() {
		t.Fatalf("executor pulse=%+v", rows)
	}
}

func TestExpiryWorkerLeaseSkipDoesNotCountTick(t *testing.T) {
	w := NewExpiryWorker(ExpiryWorkerConfig{
		Service:     &Service{},
		LeaderLease: &leaseStub{},
		Pulse:       workerpulse.NewMemoryStore(),
		ReplicaID:   "node-b",
	})
	w.TickOnce(context.Background())
	if w.TickCount() != 0 {
		t.Fatalf("follower tick_count=%d", w.TickCount())
	}
}

func TestAutoRenewWorkerLeaseSkipDoesNotCountTick(t *testing.T) {
	w := NewAutoRenewWorker(AutoRenewWorkerConfig{
		Service:     &Service{},
		LeaderLease: &leaseStub{},
		Pulse:       workerpulse.NewMemoryStore(),
		ReplicaID:   "node-b",
	})
	w.TickOnce(context.Background())
	if w.TickCount() != 0 || w.RenewedTotal() != 0 {
		t.Fatalf("follower auto-renew tick_count=%d renewed=%d", w.TickCount(), w.RenewedTotal())
	}
}

func TestAutoRenewWorkerLeaseErrorDoesNotCountTick(t *testing.T) {
	w := NewAutoRenewWorker(AutoRenewWorkerConfig{
		Service:     &Service{},
		LeaderLease: &leaseStub{err: errors.New("lock store down")},
		Pulse:       workerpulse.NewMemoryStore(),
		ReplicaID:   "node-a",
	})
	w.TickOnce(context.Background())
	if w.TickCount() != 0 || w.RenewedTotal() != 0 || w.FailedTicks() != 1 {
		t.Fatalf("lease error auto-renew tick=%d renewed=%d failed=%d", w.TickCount(), w.RenewedTotal(), w.FailedTicks())
	}
}

func TestReminderWorkerLeaseSkipDoesNotCountTick(t *testing.T) {
	w := NewReminderWorker(ReminderWorkerConfig{
		Service:     &ReminderService{},
		LeaderLease: &leaseStub{},
		Pulse:       workerpulse.NewMemoryStore(),
		ReplicaID:   "node-b",
	})
	w.TickOnce(context.Background())
	if w.TickCount() != 0 || w.SentTotal() != 0 {
		t.Fatalf("follower reminder tick_count=%d sent=%d", w.TickCount(), w.SentTotal())
	}
}

func TestExpiryWorkerLeaseErrorDoesNotCountTick(t *testing.T) {
	w := NewExpiryWorker(ExpiryWorkerConfig{
		Service:     &Service{},
		LeaderLease: &leaseStub{err: errors.New("lock store down")},
		Pulse:       workerpulse.NewMemoryStore(),
		ReplicaID:   "node-a",
	})
	w.TickOnce(context.Background())
	if w.TickCount() != 0 || w.ExpiredTotal() != 0 || w.FailedTicks() != 1 {
		t.Fatalf("lease error expiry tick=%d expired=%d failed=%d", w.TickCount(), w.ExpiredTotal(), w.FailedTicks())
	}
}

func TestReminderWorkerLeaseErrorDoesNotCountTick(t *testing.T) {
	w := NewReminderWorker(ReminderWorkerConfig{
		Service:     &ReminderService{},
		LeaderLease: &leaseStub{err: errors.New("lock store down")},
		Pulse:       workerpulse.NewMemoryStore(),
		ReplicaID:   "node-a",
	})
	w.TickOnce(context.Background())
	if w.TickCount() != 0 || w.SentTotal() != 0 || w.FailedTicks() != 1 {
		t.Fatalf("lease error reminder tick=%d sent=%d failed=%d", w.TickCount(), w.SentTotal(), w.FailedTicks())
	}
}
