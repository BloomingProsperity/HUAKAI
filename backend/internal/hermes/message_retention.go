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
	// 默认 0 表示永久保留；只有运维显式设置正整数才启用硬删清理。
	DefaultMessageRetentionDays   = 0
	defaultMessageRetentionBatch  = int32(1000)
	defaultMessageRetentionTicker = time.Hour
)

type MessagePurgeStore interface {
	PurgeMessagesBefore(context.Context, time.Time, int32) (int64, error)
}

type MessageRetentionWorkerConfig struct {
	Store         MessagePurgeStore
	RetentionDays int
	Interval      time.Duration
	BatchLimit    int32
}

type MessageRetentionWorker struct {
	store         MessagePurgeStore
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
	if days < 0 {
		return 0, fmt.Errorf("%s: must be non-negative, got %d", MessageRetentionDaysEnv, days)
	}
	return days, nil
}

func (w *MessageRetentionWorker) Start(ctx context.Context) {
	// retention_days <= 0 时不启动后台清理，避免默认部署误删聊天历史。
	if w == nil || w.store == nil || w.retentionDays <= 0 {
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
	if w == nil || w.store == nil || w.retentionDays <= 0 {
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
	var total int64
	for {
		deleted, err := w.store.PurgeMessagesBefore(ctx, cutoff, batchLimit)
		if err != nil {
			return total, fmt.Errorf("purge expired hermes messages: %w", err)
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
