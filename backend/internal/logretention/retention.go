package logretention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
)

const (
	RetentionDays = 30
	batchSize     = 5000
	maxBatches    = 20
	startupDelay  = 15 * time.Second
	normalPeriod  = 24 * time.Hour
	backlogPeriod = time.Minute
	runTimeout    = 2 * time.Minute
)

var ErrAlreadyRunning = errors.New("日志保留任务正在运行")

type Result struct {
	RetentionDays  int              `json:"retention_days"`
	Cutoff         time.Time        `json:"cutoff"`
	StartedAt      time.Time        `json:"started_at"`
	FinishedAt     time.Time        `json:"finished_at"`
	Deleted        int64            `json:"deleted"`
	Batches        int              `json:"batches"`
	ByTable        map[string]int64 `json:"by_table"`
	ByCategory     map[string]int64 `json:"by_category"`
	HasMore        bool             `json:"has_more"`
	LeaseConflicts []string         `json:"lease_conflicts"`
	FailedTables   []string         `json:"failed_tables"`
}

type Health struct {
	RetentionDays       int       `json:"retention_days"`
	Running             bool      `json:"running"`
	LastAttemptAt       time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt       time.Time `json:"last_success_at,omitempty"`
	CurrentCutoff       time.Time `json:"current_cutoff,omitempty"`
	LastDurationMS      int64     `json:"last_duration_ms"`
	LastDeleted         int64     `json:"last_deleted"`
	TotalDeleted        int64     `json:"total_deleted"`
	LastBatches         int       `json:"last_batches"`
	HasMore             bool      `json:"has_more"`
	LeaseConflictCount  int64     `json:"lease_conflict_count"`
	ConsecutiveFailures int64     `json:"consecutive_failures"`
	LastErrorClass      string    `json:"last_error_class,omitempty"`
	LastErrorTable      string    `json:"last_error_table,omitempty"`
}

type settings struct {
	now           func() time.Time
	startupDelay  time.Duration
	normalPeriod  time.Duration
	backlogPeriod time.Duration
	runTimeout    time.Duration
	batchSize     int
	maxBatches    int
	tables        []tableSpec
}

type option func(*settings)

func defaultSettings() settings {
	return settings{
		now:           time.Now,
		startupDelay:  startupDelay,
		normalPeriod:  normalPeriod,
		backlogPeriod: backlogPeriod,
		runTimeout:    runTimeout,
		batchSize:     batchSize,
		maxBatches:    maxBatches,
		tables:        append([]tableSpec(nil), ordinaryLogTables...),
	}
}

type Manager struct {
	store    batchStore
	settings settings
	running  atomic.Bool

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}

	healthMu sync.RWMutex
	health   Health
}

func New(database db.DBTX) *Manager {
	return newManager(&postgresStore{db: database})
}

func newManager(store batchStore, options ...option) *Manager {
	cfg := defaultSettings()
	for _, apply := range options {
		if apply != nil {
			apply(&cfg)
		}
	}
	return &Manager{
		store:    store,
		settings: cfg,
		done:     make(chan struct{}),
		health:   Health{RetentionDays: RetentionDays},
	}
}

// Start 在短暂错峰后补扫一次；有积压时一分钟后续跑，否则每天执行。
func (m *Manager) Start(parent context.Context) {
	if m == nil || m.store == nil {
		return
	}
	m.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		m.cancel = cancel
		go m.loop(ctx)
	})
}

func (m *Manager) loop(ctx context.Context) {
	defer close(m.done)
	delay := m.settings.startupDelay
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		result, err := m.RunOnce(ctx)
		delay = m.settings.normalPeriod
		if result.HasMore || err != nil {
			delay = m.settings.backlogPeriod
		}
	}
}

func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		if m.cancel == nil {
			return
		}
		m.cancel()
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
		}
	})
}

// RunOnce 只能使用内部固定 30 天截止线，调用方不能传入任意时间点。
func (m *Manager) RunOnce(parent context.Context) (Result, error) {
	if m == nil || m.store == nil {
		return Result{}, fmt.Errorf("日志保留器未配置")
	}
	if !m.running.CompareAndSwap(false, true) {
		return Result{RetentionDays: RetentionDays}, ErrAlreadyRunning
	}
	defer m.running.Store(false)

	started := m.settings.now().UTC()
	result := Result{
		RetentionDays: RetentionDays,
		StartedAt:     started,
		ByTable:       map[string]int64{},
		ByCategory:    map[string]int64{},
	}
	m.markAttempt(started, time.Time{})
	ctx, cancel := context.WithTimeout(parent, m.settings.runTimeout)
	defer cancel()
	cutoff, cutoffErr := m.store.retentionCutoff(ctx)
	if cutoffErr != nil {
		result.HasMore = true
		result.FailedTables = append(result.FailedTables, "retention_clock")
		result.FinishedAt = m.settings.now().UTC()
		m.finish(result, cutoffErr)
		m.logFailure(result, cutoffErr)
		return result, fmt.Errorf("读取日志保留截止线: %w", cutoffErr)
	}
	result.Cutoff = cutoff
	m.setCutoff(cutoff)

	slog.Info("日志保留任务开始",
		logcontract.FieldCategory, string(logcontract.CategoryOperation),
		logcontract.FieldEventType, "log_retention.run_started",
		logcontract.FieldResult, string(logcontract.ResultSuccess),
		"retention_days", RetentionDays,
		"cutoff", cutoff.Format(time.RFC3339),
	)

	var failures []error
	for _, table := range m.settings.tables {
		lastDeleted := int64(0)
		for batch := 0; batch < m.settings.maxBatches; batch++ {
			if err := ctx.Err(); err != nil {
				result.HasMore = true
				failures = append(failures, err)
				result.FailedTables = appendUnique(result.FailedTables, table.name)
				break
			}
			batchResult, err := m.store.deleteExpiredBatch(ctx, table, cutoff, m.settings.batchSize)
			if err != nil {
				result.HasMore = true
				failures = append(failures, fmt.Errorf("%s: %w", table.name, err))
				result.FailedTables = appendUnique(result.FailedTables, table.name)
				break
			}
			if !batchResult.acquired {
				result.HasMore = true
				result.LeaseConflicts = appendUnique(result.LeaseConflicts, table.name)
				break
			}
			result.Batches++
			lastDeleted = batchResult.deleted
			result.Deleted += batchResult.deleted
			result.ByTable[table.name] += batchResult.deleted
			for category, count := range batchResult.byCategory {
				result.ByCategory[category] += count
			}
			if batchResult.deleted < int64(m.settings.batchSize) {
				break
			}
		}
		if lastDeleted == int64(m.settings.batchSize) {
			result.HasMore = true
		}
	}

	result.FinishedAt = m.settings.now().UTC()
	err := errors.Join(failures...)
	m.finish(result, err)
	if err != nil {
		m.logFailure(result, err)
		return result, err
	}
	slog.Info("日志保留任务完成",
		logcontract.FieldCategory, string(logcontract.CategoryOperation),
		logcontract.FieldEventType, "log_retention.run_completed",
		logcontract.FieldResult, string(logcontract.ResultSuccess),
		"deleted", result.Deleted,
		"batches", result.Batches,
		"has_more", result.HasMore,
	)
	return result, nil
}

func (m *Manager) markAttempt(at, cutoff time.Time) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	m.health.Running = true
	m.health.LastAttemptAt = at
	m.health.CurrentCutoff = cutoff
}

func (m *Manager) setCutoff(cutoff time.Time) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	m.health.CurrentCutoff = cutoff
}

func (m *Manager) finish(result Result, err error) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	m.health.Running = false
	m.health.LastDurationMS = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	m.health.LastDeleted = result.Deleted
	m.health.TotalDeleted += result.Deleted
	m.health.LastBatches = result.Batches
	m.health.HasMore = result.HasMore
	m.health.LeaseConflictCount += int64(len(result.LeaseConflicts))
	if err == nil {
		m.health.LastSuccessAt = result.FinishedAt
		m.health.ConsecutiveFailures = 0
		m.health.LastErrorClass = ""
		m.health.LastErrorTable = ""
		return
	}
	m.health.ConsecutiveFailures++
	m.health.LastErrorClass = string(classifyRetentionError(err))
	if len(result.FailedTables) > 0 {
		m.health.LastErrorTable = result.FailedTables[0]
	}
}

func (m *Manager) logFailure(result Result, err error) {
	errorClass := classifyRetentionError(err)
	resultCode := logcontract.ResultServerFailure
	errorCode := "log_retention_failed"
	recoveryState := logcontract.RecoveryRetrying
	if errorClass == logcontract.ErrorTimeout {
		resultCode = logcontract.ResultTimeout
		errorCode = "log_retention_timeout"
	} else if errorClass == logcontract.ErrorCanceled {
		resultCode = logcontract.ResultCanceled
		errorCode = "log_retention_canceled"
		recoveryState = logcontract.RecoveryPending
	}
	slog.Error("日志保留任务失败",
		logcontract.FieldCategory, string(logcontract.CategoryRecovery),
		logcontract.FieldEventType, "log_retention.run_failed",
		logcontract.FieldResult, string(resultCode),
		logcontract.FieldErrorClass, string(errorClass),
		logcontract.FieldErrorCode, errorCode,
		logcontract.FieldRetryable, true,
		logcontract.FieldRecoveryState, string(recoveryState),
		"failed_tables", result.FailedTables,
		"deleted", result.Deleted,
	)
}

func classifyRetentionError(err error) logcontract.ErrorClass {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return logcontract.ErrorTimeout
	case errors.Is(err, context.Canceled):
		return logcontract.ErrorCanceled
	default:
		return logcontract.ErrorDependency
	}
}

func (m *Manager) Health() Health {
	if m == nil {
		return Health{RetentionDays: RetentionDays}
	}
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()
	value := m.health
	value.Running = m.running.Load()
	return value
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
