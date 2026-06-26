package usageretention

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	UsageRetentionDaysEnv = "HUAKAI_USAGE_RETENTION_DAYS"
	// 默认 0 表示永久保留;运维必须显式启用。
	DefaultUsageRetentionDays   = 0
	defaultUsageRetentionBatch  = int32(1000)
	defaultUsageRetentionTicker = time.Hour
)

var ErrMisconfigured = errors.New("usageretention: store not configured")

type UsagePurgeStore interface {
	PurgeUsageRecordsBefore(context.Context, time.Time, int32) (int64, error)
}

type Config struct {
	Store         UsagePurgeStore
	RetentionDays int
	Interval      time.Duration
	BatchLimit    int32
	Now           func() time.Time
}

type Worker struct {
	store         UsagePurgeStore
	retentionDays int
	interval      time.Duration
	batchLimit    int32
	now           func() time.Time

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewUsageRetentionWorker(cfg Config) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultUsageRetentionTicker
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultUsageRetentionBatch
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Worker{
		store:         cfg.Store,
		retentionDays: cfg.RetentionDays,
		interval:      cfg.Interval,
		batchLimit:    cfg.BatchLimit,
		now:           cfg.Now,
	}
}

func UsageRetentionDaysFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv(UsageRetentionDaysEnv))
	if raw == "" {
		return DefaultUsageRetentionDays, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", UsageRetentionDaysEnv, raw, err)
	}
	if days < 0 {
		return 0, fmt.Errorf("%s: must be non-negative, got %d", UsageRetentionDaysEnv, days)
	}
	return days, nil
}

func (w *Worker) Start(ctx context.Context) {
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

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	_, _ = w.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			_, _ = w.RunOnce(ctx)
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int64, error) {
	if w == nil || w.store == nil || w.retentionDays <= 0 {
		return 0, nil
	}
	now := w.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	batchLimit := w.batchLimit
	if batchLimit <= 0 {
		batchLimit = defaultUsageRetentionBatch
	}
	cutoff := now.UTC().AddDate(0, 0, -w.retentionDays)
	var total int64
	for {
		deleted, err := w.store.PurgeUsageRecordsBefore(ctx, cutoff, batchLimit)
		if err != nil {
			return total, fmt.Errorf("purge expired usage records: %w", err)
		}
		total += deleted
		if deleted < int64(batchLimit) {
			return total, nil
		}
	}
}

func (w *Worker) Stop() {
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

type PostgresUsagePurgeStore struct {
	queries *dbbilling.Queries
}

func NewPostgresUsagePurgeStore(queries *dbbilling.Queries) *PostgresUsagePurgeStore {
	return &PostgresUsagePurgeStore{queries: queries}
}

func (s *PostgresUsagePurgeStore) PurgeUsageRecordsBefore(ctx context.Context, cutoff time.Time, batchLimit int32) (int64, error) {
	if s == nil || s.queries == nil {
		return 0, ErrMisconfigured
	}
	if batchLimit <= 0 {
		batchLimit = defaultUsageRetentionBatch
	}
	return s.queries.PurgeUsageRecordsBefore(ctx, dbbilling.PurgeUsageRecordsBeforeParams{
		Cutoff:     pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true},
		BatchLimit: batchLimit,
	})
}
