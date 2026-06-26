package mediatask

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

type BalanceModeResolver interface {
	ResolveBalanceEnforcementMode(context.Context, int64) billing.BalanceEnforcementMode
}

type PostgresStoreConfig struct {
	BillingPolicyVersion string
	RequestClass         string
	BalanceModeResolver  BalanceModeResolver
}

type PostgresStore struct {
	pool             *pgxpool.Pool
	billingVersion   string
	requestClass     string
	balanceResolver  BalanceModeResolver
	beforeInsertTask func() error
}

func NewPostgresStore(pool *pgxpool.Pool, cfg PostgresStoreConfig) *PostgresStore {
	if cfg.RequestClass == "" {
		cfg.RequestClass = "standard"
	}
	return &PostgresStore{
		pool:            pool,
		billingVersion:  strings.TrimSpace(cfg.BillingPolicyVersion),
		requestClass:    strings.TrimSpace(cfg.RequestClass),
		balanceResolver: cfg.BalanceModeResolver,
	}
}

func (s *PostgresStore) CreateTask(ctx context.Context, input CreateTaskInput) (Task, bool, error) {
	if s == nil || s.pool == nil {
		return Task{}, false, ErrStoreNotConfigured
	}
	var out Task
	var hit bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		existing, err := selectTaskByRequestForUpdate(ctx, tx, input.TenantID, input.RequestID)
		if err == nil {
			if !sameIdempotentTask(existing, input) {
				return ErrRequestIDConflict
			}
			out, hit = existing, true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		created, err := s.insertReservedTask(ctx, tx, input)
		if err != nil {
			return err
		}
		out = created
		return nil
	})
	return out, hit, err
}

func (s *PostgresStore) GetTask(ctx context.Context, tenantID, userID, id int64) (Task, error) {
	if s == nil || s.pool == nil {
		return Task{}, ErrStoreNotConfigured
	}
	task, err := scanTask(s.pool.QueryRow(ctx, selectTaskSQL+` WHERE id=$1 AND tenant_id=$2 AND user_id=$3`, id, tenantID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return task, err
}

func (s *PostgresStore) ListTasks(ctx context.Context, tenantID, userID int64, limit int) ([]Task, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, selectTaskSQL+`
WHERE tenant_id=$1 AND user_id=$2
ORDER BY created_at DESC, id DESC
LIMIT $3`, tenantID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *PostgresStore) AcquireLease(ctx context.Context, owner string, ttl time.Duration, now time.Time) (Task, error) {
	if s == nil || s.pool == nil {
		return Task{}, ErrStoreNotConfigured
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	task, err := scanTask(s.pool.QueryRow(ctx, acquireLeaseSQL, owner, now.Add(ttl).UTC(), now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNoRunnableTask
	}
	return task, err
}

func (s *PostgresStore) MarkProviderSubmitted(ctx context.Context, task Task, owner, providerTaskID string, now time.Time) (Task, error) {
	if s == nil || s.pool == nil {
		return Task{}, ErrStoreNotConfigured
	}
	updated, err := scanTask(s.pool.QueryRow(ctx, markSubmittedSQL, task.ID, owner, strings.TrimSpace(providerTaskID), now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrLeaseLost
	}
	return updated, err
}

func (s *PostgresStore) UpdateProgress(ctx context.Context, task Task, owner string, progress int, now time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	progress = clampProgress(progress)
	if progress >= 100 {
		progress = 99
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE media_tasks
SET progress=$3, lease_owner=NULL, lease_expires_at=NULL, updated_at=$4
WHERE id=$1 AND lease_owner=$2 AND status='in_progress'`,
		task.ID, owner, progress, now.UTC(),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) withSerializableRetry(ctx context.Context, fn func(pgx.Tx) error) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return err
		}
		err = fn(tx)
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err == nil {
			return nil
		}
		_ = tx.Rollback(ctx)
		if !isRetryablePg(err) {
			return err
		}
		last = err
	}
	return last
}

func selectTaskByRequestForUpdate(ctx context.Context, tx pgx.Tx, tenantID int64, requestID string) (Task, error) {
	return scanTask(tx.QueryRow(ctx, selectTaskSQL+` WHERE tenant_id=$1 AND request_id=$2 FOR UPDATE`, tenantID, requestID))
}

func sameIdempotentTask(existing Task, input CreateTaskInput) bool {
	return existing.UserID == input.UserID &&
		existing.TaskType == input.TaskType &&
		existing.Provider == input.Provider &&
		jsonCanonicalEqual(existing.InputParams, input.InputParams)
}

// jsonCanonicalEqual 按值比较两个 JSON payload,容忍 PostgreSQL JSONB
// 在往返过程中施加的表示形态变化(例如 key 冒号后的空格、key 重排序)。
// 对非 JSON 输入回退到字节相等比较。
func jsonCanonicalEqual(a, b []byte) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return bytes.Equal(a, b)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ab, err1 := json.Marshal(av)
	bb, err2 := json.Marshal(bv)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

func scanTask(row pgx.Row) (Task, error) {
	var task Task
	var providerTaskID, holdRef, errorClass, leaseOwner sql.NullString
	var actual sql.NullInt64
	var leaseExpires, finished pgtype.Timestamptz
	err := row.Scan(
		&task.ID, &task.TenantID, &task.UserID, &task.TaskType, &task.Status, &task.Provider,
		&providerTaskID, &task.RequestID, &task.InputParams, &task.Result, &task.EstimatedCents,
		&actual, &holdRef, &errorClass, &task.Progress, &leaseOwner, &leaseExpires,
		&task.CreatedAt, &task.UpdatedAt, &finished,
	)
	if err != nil {
		return Task{}, err
	}
	task.ProviderTaskID = providerTaskID.String
	task.HoldRef = holdRef.String
	task.ErrorClass = errorClass.String
	if actual.Valid {
		task.ActualCents = &actual.Int64
	}
	if leaseOwner.Valid {
		task.LeaseOwner = leaseOwner.String
	}
	if leaseExpires.Valid {
		v := leaseExpires.Time.UTC()
		task.LeaseExpiresAt = &v
	}
	if finished.Valid {
		v := finished.Time.UTC()
		task.FinishedAt = &v
	}
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
	return task, nil
}

func scanTaskRows(rows pgx.Rows) (Task, error) {
	return scanTask(rowScanner{rows: rows})
}

type rowScanner struct {
	rows pgx.Rows
}

func (r rowScanner) Scan(dest ...any) error {
	return r.rows.Scan(dest...)
}

func isRetryablePg(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

func clampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func jsonOrNull(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

const selectTaskSQL = `
SELECT id, tenant_id, user_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, lease_owner, lease_expires_at, created_at, updated_at, finished_at
FROM media_tasks`

const insertTaskSQL = `
INSERT INTO media_tasks (
	tenant_id, user_id, task_type, status, provider, request_id, input_params,
	estimated_cents, hold_ref
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING id, tenant_id, user_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, lease_owner, lease_expires_at, created_at, updated_at, finished_at`

const acquireLeaseSQL = `
WITH candidate AS (
	SELECT id
	FROM media_tasks
	WHERE status IN ('queued','in_progress')
	  AND (lease_expires_at IS NULL OR lease_expires_at <= $3)
	ORDER BY updated_at ASC, id ASC
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
UPDATE media_tasks mt
SET lease_owner=$1, lease_expires_at=$2, updated_at=$3
FROM candidate c
WHERE mt.id=c.id
RETURNING mt.id, mt.tenant_id, mt.user_id, mt.task_type, mt.status, mt.provider, mt.provider_task_id,
       mt.request_id, mt.input_params, mt.result, mt.estimated_cents, mt.actual_cents, mt.hold_ref,
       mt.error_class, mt.progress, mt.lease_owner, mt.lease_expires_at, mt.created_at, mt.updated_at, mt.finished_at`

const markSubmittedSQL = `
UPDATE media_tasks
SET status='in_progress', provider_task_id=$3, progress=GREATEST(progress, 1),
    lease_owner=NULL, lease_expires_at=NULL, updated_at=$4
WHERE id=$1 AND lease_owner=$2 AND status='queued'
RETURNING id, tenant_id, user_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, lease_owner, lease_expires_at, created_at, updated_at, finished_at`
