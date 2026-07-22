package hermes

import (
	"context"
	"testing"
	"time"
)

func TestMessageRetentionWorkerRunOncePurgesOnlyRowsOlderThanRetention(t *testing.T) {
	// 第 30 天边界保留，更早的消息和已经清空的会话一起删除。
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	store := &memoryMessagePurgeStore{
		rows: []memoryMessageRow{
			{id: 1, conversationID: 11, createdAt: now.AddDate(0, 0, -31)},
			{id: 2, conversationID: 12, createdAt: now.AddDate(0, 0, -30)},
		},
		conversations: []memoryConversationRow{
			{id: 11, lastMessageAt: now.AddDate(0, 0, -31)},
			{id: 12, lastMessageAt: now.AddDate(0, 0, -30)},
		},
	}
	worker := NewMessageRetentionWorker(MessageRetentionWorkerConfig{
		Store:         store,
		RetentionDays: 30,
		Interval:      time.Hour,
	})

	deleted, err := worker.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted=%d，期望一条消息和一个空会话", deleted)
	}
	if len(store.rows) != 1 || store.rows[0].id != 2 {
		t.Fatalf("剩余消息=%+v，期望仅保留边界内的 id=2", store.rows)
	}
	if len(store.conversations) != 1 || store.conversations[0].id != 12 {
		t.Fatalf("剩余会话=%+v，期望仅保留 id=12", store.conversations)
	}
	if len(store.cutoffs) != 2 || !store.cutoffs[0].Equal(now.AddDate(0, 0, -30)) ||
		!store.cutoffs[1].Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("截止线=%+v，期望消息和会话都使用 30 天", store.cutoffs)
	}
}

func TestMessageRetentionWorkerClampsInvalidConfigToThirtyDays(t *testing.T) {
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
	if deleted != 1 {
		t.Fatalf("deleted=%d，期望无效配置回退 30 天后删除旧消息", deleted)
	}
	if len(store.rows) != 1 || store.rows[0].id != 2 {
		t.Fatalf("剩余消息=%+v，期望仅保留近期消息", store.rows)
	}
	if len(store.cutoffs) != 2 || !store.cutoffs[0].Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("截止线=%+v，期望回退为 30 天", store.cutoffs)
	}
}

func TestMessageRetentionDaysFromEnvEnforcesMaximum(t *testing.T) {
	t.Setenv(MessageRetentionDaysEnv, "")
	days, err := MessageRetentionDaysFromEnv()
	if err != nil {
		t.Fatalf("MessageRetentionDaysFromEnv default: %v", err)
	}
	if days != 30 {
		t.Fatalf("默认保留天数=%d，期望 30", days)
	}

	t.Setenv(MessageRetentionDaysEnv, "0")
	if _, err = MessageRetentionDaysFromEnv(); err == nil {
		t.Fatal("显式 0 天未被拒绝")
	}

	t.Setenv(MessageRetentionDaysEnv, "31")
	if _, err = MessageRetentionDaysFromEnv(); err == nil {
		t.Fatal("超过 30 天未被拒绝")
	}

	t.Setenv(MessageRetentionDaysEnv, "7")
	if _, err = MessageRetentionDaysFromEnv(); err == nil {
		t.Fatal("缩短全局 30 天周期未被拒绝")
	}

	t.Setenv(MessageRetentionDaysEnv, "30")
	days, err = MessageRetentionDaysFromEnv()
	if err != nil {
		t.Fatalf("读取 30 天配置: %v", err)
	}
	if days != 30 {
		t.Fatalf("显式保留天数=%d，期望 30", days)
	}
}

type memoryMessageRow struct {
	id             int64
	conversationID int64
	createdAt      time.Time
}

type memoryConversationRow struct {
	id            int64
	lastMessageAt time.Time
}

type memoryMessagePurgeStore struct {
	rows          []memoryMessageRow
	conversations []memoryConversationRow
	cutoffs       []time.Time
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

func (s *memoryMessagePurgeStore) PurgeConversationsBefore(_ context.Context, cutoff time.Time, _ int32) (int64, error) {
	s.cutoffs = append(s.cutoffs, cutoff)
	remainingMessages := make(map[int64]bool, len(s.rows))
	for _, row := range s.rows {
		remainingMessages[row.conversationID] = true
	}
	kept := make([]memoryConversationRow, 0, len(s.conversations))
	var deleted int64
	for _, row := range s.conversations {
		if row.lastMessageAt.Before(cutoff) && !remainingMessages[row.id] {
			deleted++
			continue
		}
		kept = append(kept, row)
	}
	s.conversations = kept
	return deleted, nil
}
