package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbillingmaint "github.com/BloomingProsperity/HUAKAI/internal/db/billingmaint"
)

// defaultReplayTTL 是持久幂等重放记录默认存活时长。
const defaultReplayTTL = 24 * time.Hour

// ReplayRecord 是一条持久幂等重放记录的可重放内容。
type ReplayRecord struct {
	ResponseStatus int
	ContentType    string
	ResponseBody   []byte
}

// ReplayStore 是 Phase E 持久幂等重放存储 (migration 0044
// idempotency_replay_records 表)。 带 Idempotency-Key 的请求成功完成后存原始
// 响应; 同 key 重试时 ClaimGate 返 IdempotencyHit, caller 按原 claim_id 取回
// 重放 —— 路由无关、不受 L2 response cache 淘汰影响。
type ReplayStore interface {
	// Record 存一条重放记录。 幂等: 同 (tenant, claim) 重复写被忽略。
	// ttl <= 0 时用默认 24h。
	Record(ctx context.Context, tenantID, claimID int64, status int, contentType string, body []byte, ttl time.Duration) error
	// Lookup 取未过期的重放记录; 不存在或已过期返回 (nil, false, nil)。
	Lookup(ctx context.Context, tenantID, claimID int64) (*ReplayRecord, bool, error)
	// DeleteExpired 物理删除已过期记录, 返回删除行数 (供 ReplayJanitor 周期调用)。
	DeleteExpired(ctx context.Context) (int64, error)
}

func normalizeReplayInputs(contentType string, ttl time.Duration) (string, time.Duration) {
	if contentType == "" {
		contentType = "application/json"
	}
	if ttl <= 0 {
		ttl = defaultReplayTTL
	}
	return contentType, ttl
}

// DefaultReplayStore 是 ReplayStore 的 Postgres 实现。
type DefaultReplayStore struct {
	q *dbbillingmaint.Queries
}

// NewReplayStore 构造 Postgres 持久重放存储; pool 为 nil 时返回未配置实例。
func NewReplayStore(pool *pgxpool.Pool) *DefaultReplayStore {
	if pool == nil {
		return &DefaultReplayStore{}
	}
	return &DefaultReplayStore{q: dbbillingmaint.New(pool)}
}

func (s *DefaultReplayStore) Record(ctx context.Context, tenantID, claimID int64, status int, contentType string, body []byte, ttl time.Duration) error {
	if s == nil || s.q == nil {
		return ErrPoolNotConfigured
	}
	contentType, ttl = normalizeReplayInputs(contentType, ttl)
	return s.q.InsertIdempotencyReplayRecord(ctx, dbbillingmaint.InsertIdempotencyReplayRecordParams{
		TenantID:       tenantID,
		ClaimID:        claimID,
		ResponseStatus: int32(status),
		ContentType:    contentType,
		ResponseBody:   body,
		ExpiresAt:      pgtype.Timestamptz{Time: time.Now().UTC().Add(ttl), Valid: true},
	})
}

func (s *DefaultReplayStore) Lookup(ctx context.Context, tenantID, claimID int64) (*ReplayRecord, bool, error) {
	if s == nil || s.q == nil {
		return nil, false, ErrPoolNotConfigured
	}
	row, err := s.q.GetIdempotencyReplayRecord(ctx, dbbillingmaint.GetIdempotencyReplayRecordParams{
		TenantID: tenantID,
		ClaimID:  claimID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &ReplayRecord{
		ResponseStatus: int(row.ResponseStatus),
		ContentType:    row.ContentType,
		ResponseBody:   row.ResponseBody,
	}, true, nil
}

func (s *DefaultReplayStore) DeleteExpired(ctx context.Context) (int64, error) {
	if s == nil || s.q == nil {
		return 0, ErrPoolNotConfigured
	}
	return s.q.DeleteExpiredIdempotencyReplayRecords(ctx)
}

var _ ReplayStore = (*DefaultReplayStore)(nil)

// MemoryReplayStore 是 ReplayStore 的内存实现, 供测试与单机 dev 模式使用。
type MemoryReplayStore struct {
	mu      sync.Mutex
	records map[string]memReplayEntry
}

type memReplayEntry struct {
	rec       ReplayRecord
	expiresAt time.Time
}

// NewMemoryReplayStore 构造内存持久重放存储。
func NewMemoryReplayStore() *MemoryReplayStore {
	return &MemoryReplayStore{records: make(map[string]memReplayEntry)}
}

func memReplayKey(tenantID, claimID int64) string {
	return fmt.Sprintf("%d:%d", tenantID, claimID)
}

func (m *MemoryReplayStore) Record(_ context.Context, tenantID, claimID int64, status int, contentType string, body []byte, ttl time.Duration) error {
	contentType, ttl = normalizeReplayInputs(contentType, ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memReplayKey(tenantID, claimID)
	if _, exists := m.records[key]; exists {
		return nil // ON CONFLICT DO NOTHING 语义
	}
	m.records[key] = memReplayEntry{
		rec: ReplayRecord{
			ResponseStatus: status,
			ContentType:    contentType,
			ResponseBody:   append([]byte(nil), body...),
		},
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (m *MemoryReplayStore) Lookup(_ context.Context, tenantID, claimID int64) (*ReplayRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.records[memReplayKey(tenantID, claimID)]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false, nil
	}
	rec := entry.rec
	rec.ResponseBody = append([]byte(nil), entry.rec.ResponseBody...)
	return &rec, true, nil
}

func (m *MemoryReplayStore) DeleteExpired(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var deleted int64
	for key, entry := range m.records {
		if now.After(entry.expiresAt) {
			delete(m.records, key)
			deleted++
		}
	}
	return deleted, nil
}

var _ ReplayStore = (*MemoryReplayStore)(nil)

// defaultReplayJanitorInterval 是 ReplayJanitor 默认清理周期。
const defaultReplayJanitorInterval = 10 * time.Minute

// ReplayJanitor 是后台 ticker, 周期物理清理过期的持久重放记录, 防表无界增长
// (response_body 是原始响应体, 不清会持续占空间)。 接 context cancellation
// 与 Stop 优雅退出。
type ReplayJanitor struct {
	store    ReplayStore
	interval time.Duration

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

// NewReplayJanitor 构造清理 worker; interval <= 0 用默认 10min。
func NewReplayJanitor(store ReplayStore, interval time.Duration) *ReplayJanitor {
	if interval <= 0 {
		interval = defaultReplayJanitorInterval
	}
	return &ReplayJanitor{store: store, interval: interval}
}

// Start 启动 ticker goroutine。重复 Start no-op (idempotent)。
func (j *ReplayJanitor) Start(ctx context.Context) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running || j.store == nil {
		return
	}
	j.stop = make(chan struct{})
	j.done = make(chan struct{})
	j.running = true
	go j.loop(ctx)
}

func (j *ReplayJanitor) loop(ctx context.Context) {
	defer close(j.done)
	t := time.NewTicker(j.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-j.stop:
			return
		case <-t.C:
			// best-effort: 清理失败 (DB 抖动) 等下个 tick 重试。
			_, _ = j.store.DeleteExpired(ctx)
		}
	}
}

// Stop 优雅停止, 等 loop 退出。 多次调 no-op (idempotent)。
func (j *ReplayJanitor) Stop() {
	j.mu.Lock()
	if !j.running {
		j.mu.Unlock()
		return
	}
	close(j.stop)
	j.running = false
	done := j.done
	j.mu.Unlock()
	if done != nil {
		<-done
	}
}
