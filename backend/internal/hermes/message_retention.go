package hermes

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

const (
	MessageRetentionDaysEnv = "HUAKAI_HERMES_RETENTION_DAYS"
	// 运维助手聊天属于普通日志，统一保留 30 天，模块不能自行关闭、延长或缩短。
	DefaultMessageRetentionDays   = 30
	defaultMessageRetentionBatch  = int32(1000)
	defaultMessageRetentionTicker = time.Hour
)

type MessageRetentionStore interface {
	PurgeMessagesBefore(context.Context, time.Time, int32) (int64, error)
	PurgeConversationsBefore(context.Context, time.Time, int32) (int64, error)
}

type MessageRetentionWorkerConfig struct {
	Store         MessageRetentionStore
	RetentionDays int
	Interval      time.Duration
	BatchLimit    int32
}

type MessageRetentionWorker struct {
	store         MessageRetentionStore
	retentionDays int
	interval      time.Duration
	batchLimit    int32

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	now     func() time.Time
}

func NewMessageRetentionWorker(cfg MessageRetentionWorkerConfig) *MessageRetentionWorker {
	if cfg.RetentionDays != DefaultMessageRetentionDays {
		cfg.RetentionDays = DefaultMessageRetentionDays
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultMessageRetentionTicker
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultMessageRetentionBatch
	}
	return &MessageRetentionWorker{
		store:         cfg.Store,
		retentionDays: cfg.RetentionDays,
		interval:      cfg.Interval,
		batchLimit:    cfg.BatchLimit,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

func MessageRetentionDaysFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv(MessageRetentionDaysEnv))
	if raw == "" {
		return DefaultMessageRetentionDays, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", MessageRetentionDaysEnv, raw, err)
	}
	if days != DefaultMessageRetentionDays {
		return 0, fmt.Errorf("%s 只能为全局固定值 %d 天，实际为 %d", MessageRetentionDaysEnv, DefaultMessageRetentionDays, days)
	}
	return days, nil
}

func (w *MessageRetentionWorker) Start(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.running = true
	go w.loop(ctx)
}

func (w *MessageRetentionWorker) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	_, _ = w.RunOnce(ctx, w.now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			_, _ = w.RunOnce(ctx, w.now())
		}
	}
}

func (w *MessageRetentionWorker) RunOnce(ctx context.Context, now time.Time) (int64, error) {
	if w == nil || w.store == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = w.now()
	}
	batchLimit := w.batchLimit
	if batchLimit <= 0 {
		batchLimit = defaultMessageRetentionBatch
	}
	cutoff := now.UTC().AddDate(0, 0, -w.retentionDays)
	messageTotal, err := purgeRetentionBatches(ctx, batchLimit, func(ctx context.Context, limit int32) (int64, error) {
		return w.store.PurgeMessagesBefore(ctx, cutoff, limit)
	})
	if err != nil {
		return messageTotal, fmt.Errorf("清理过期 Hermes 消息: %w", err)
	}
	conversationTotal, err := purgeRetentionBatches(ctx, batchLimit, func(ctx context.Context, limit int32) (int64, error) {
		return w.store.PurgeConversationsBefore(ctx, cutoff, limit)
	})
	if err != nil {
		return messageTotal + conversationTotal, fmt.Errorf("清理空的过期 Hermes 会话: %w", err)
	}
	return messageTotal + conversationTotal, nil
}

func purgeRetentionBatches(
	ctx context.Context,
	batchLimit int32,
	purge func(context.Context, int32) (int64, error),
) (int64, error) {
	var total int64
	for {
		deleted, err := purge(ctx, batchLimit)
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted < int64(batchLimit) {
			return total, nil
		}
	}
}

func (w *MessageRetentionWorker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.running = false
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

type PostgresMessagePurgeStore struct {
	queries *dbhermes.Queries
}

func NewPostgresMessagePurgeStore(queries *dbhermes.Queries) *PostgresMessagePurgeStore {
	return &PostgresMessagePurgeStore{queries: queries}
}

func (s *PostgresMessagePurgeStore) PurgeMessagesBefore(ctx context.Context, cutoff time.Time, batchLimit int32) (int64, error) {
	if s == nil || s.queries == nil {
		return 0, ErrMisconfigured
	}
	if batchLimit <= 0 {
		batchLimit = defaultMessageRetentionBatch
	}
	return s.queries.PurgeMessagesBefore(ctx, dbhermes.PurgeMessagesBeforeParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true},
		BatchLimit: batchLimit,
	})
}

func (s *PostgresMessagePurgeStore) PurgeConversationsBefore(ctx context.Context, cutoff time.Time, batchLimit int32) (int64, error) {
	if s == nil || s.queries == nil {
		return 0, ErrMisconfigured
	}
	if batchLimit <= 0 {
		batchLimit = defaultMessageRetentionBatch
	}
	return s.queries.PurgeConversationsBefore(ctx, dbhermes.PurgeConversationsBeforeParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true},
		BatchLimit: batchLimit,
	})
}
