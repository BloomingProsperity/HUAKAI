package hermes

import (
	"context"
	"testing"
	"time"
)

func TestMessageRetentionWorkerRunOncePurgesOnlyRowsOlderThanRetention(t *testing.T) {
	// 回归测试：运维显式设置正整数 retention 后才允许硬删过期 Hermes 消息。
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	store := &memoryMessagePurgeStore{
		rows: []memoryMessageRow{
			{id: 1, createdAt: now.AddDate(0, 0, -92)},
			{id: 2, createdAt: now.AddDate(0, 0, -90)},
		},
	}
	worker := NewMessageRetentionWorker(MessageRetentionWorkerConfig{
		Store:         store,
		RetentionDays: 91,
		Interval:      time.Hour,
	})

	deleted, err := worker.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d want exactly one expired row", deleted)
	}
	if len(store.rows) != 1 || store.rows[0].id != 2 {
		t.Fatalf("remaining rows=%+v want only in-retention row id=2", store.rows)
	}
	if len(store.cutoffs) != 1 || !store.cutoffs[0].Equal(now.AddDate(0, 0, -91)) {
		t.Fatalf("cutoffs=%+v want now minus 91 days", store.cutoffs)
	}
	// 变异检查：去掉 purge 调用会残留 id=1；翻转 cutoff 则会误删 id=2。
}

func TestMessageRetentionWorkerRunOnceDoesNotPurgeWhenRetentionDisabled(t *testing.T) {
	// 回归测试：默认 retention_days=0 表示永久保留，不能启动 90 天兜底硬删。
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	store := &memoryMessagePurgeStore{
		rows: []memoryMessageRow{
			{id: 1, createdAt: now.AddDate(0, 0, -365)},
			{id: 2, createdAt: now.AddDate(0, 0, -1)},
		},
	}
	worker := NewMessageRetentionWorker(MessageRetentionWorkerConfig{
		Store:         store,
		RetentionDays: 0,
		Interval:      time.Hour,
	})

	deleted, err := worker.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted=%d want no purge when retention is disabled", deleted)
	}
	if len(store.rows) != 2 {
		t.Fatalf("remaining rows=%+v want all rows retained", store.rows)
	}
	if len(store.cutoffs) != 0 {
		t.Fatalf("cutoffs=%+v want purge store not called", store.cutoffs)
	}
	// 变异检查：若恢复 90 天兜底清理会删掉 id=1 并记录一次 cutoff。
}

func TestMessageRetentionDaysFromEnvDefaultsToDisabled(t *testing.T) {
	t.Setenv(MessageRetentionDaysEnv, "")
	days, err := MessageRetentionDaysFromEnv()
	if err != nil {
		t.Fatalf("MessageRetentionDaysFromEnv default: %v", err)
	}
	if days != 0 {
		t.Fatalf("default retention days=%d want 0 disabled", days)
	}

	t.Setenv(MessageRetentionDaysEnv, "0")
	days, err = MessageRetentionDaysFromEnv()
	if err != nil {
		t.Fatalf("MessageRetentionDaysFromEnv explicit zero: %v", err)
	}
	if days != 0 {
		t.Fatalf("explicit zero retention days=%d want 0 disabled", days)
	}

	t.Setenv(MessageRetentionDaysEnv, "91")
	days, err = MessageRetentionDaysFromEnv()
	if err != nil {
		t.Fatalf("MessageRetentionDaysFromEnv explicit days: %v", err)
	}
	if days != 91 {
		t.Fatalf("explicit retention days=%d want 91", days)
	}
}

type memoryMessageRow struct {
	id        int64
	createdAt time.Time
}

type memoryMessagePurgeStore struct {
	rows    []memoryMessageRow
	cutoffs []time.Time
}

func (s *memoryMessagePurgeStore) PurgeMessagesBefore(_ context.Context, cutoff time.Time, _ int32) (int64, error) {
	s.cutoffs = append(s.cutoffs, cutoff)
	kept := make([]memoryMessageRow, 0, len(s.rows))
	var deleted int64
	for _, row := range s.rows {
		if row.createdAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, row)
	}
	s.rows = kept
	return deleted, nil
}
