package usageretention

import (
	"context"
	"testing"
	"time"
)

// 变异:把 RetentionDays=0 当作「立即删除」-> store 被调用 -> 变红。
func TestUsageRetentionDefault0KeepsAll(t *testing.T) {
	store := &fakeUsagePurgeStore{}
	worker := NewUsageRetentionWorker(Config{
		Store:         store,
		RetentionDays: 0,
		BatchLimit:    10,
		Now:           func() time.Time { return time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC) },
	})

	deleted, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0 when retention is disabled", store.calls)
	}
}

func TestUsageRetentionDeletesOnlyBeforeCutoff(t *testing.T) {
	now := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	store := &fakeUsagePurgeStore{deleted: []int64{3, 0}}
	worker := NewUsageRetentionWorker(Config{
		Store:         store,
		RetentionDays: 7,
		BatchLimit:    3,
		Now:           func() time.Time { return now },
	})

	deleted, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want 2", store.calls)
	}
	wantCutoff := now.Add(-7 * 24 * time.Hour)
	if !store.cutoffs[0].Equal(wantCutoff) {
		t.Fatalf("cutoff = %s, want %s", store.cutoffs[0], wantCutoff)
	}
}

type fakeUsagePurgeStore struct {
	deleted []int64
	calls   int
	cutoffs []time.Time
	limits  []int32
}

func (s *fakeUsagePurgeStore) PurgeUsageRecordsBefore(_ context.Context, cutoff time.Time, limit int32) (int64, error) {
	s.calls++
	s.cutoffs = append(s.cutoffs, cutoff)
	s.limits = append(s.limits, limit)
	if len(s.deleted) == 0 {
		return 0, nil
	}
	deleted := s.deleted[0]
	s.deleted = s.deleted[1:]
	return deleted, nil
}
