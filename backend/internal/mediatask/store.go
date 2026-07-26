package mediatask

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
)

type BalanceModeResolver interface {
	ResolveBalanceEnforcementMode(context.Context, int64) billing.BalanceEnforcementMode
}

type PostgresStoreConfig struct {
	BillingPolicyVersion string
	RequestClass         string
	BalanceModeResolver  BalanceModeResolver
	ClaimGate            billing.ClaimGate
	QuotaReserver        quotaenforce.Reserver
	Settler              billing.Settler
}

type PostgresStore struct {
	pool             *pgxpool.Pool
	billingVersion   string
	requestClass     string
	balanceResolver  BalanceModeResolver
	claimGate        billing.ClaimGate
	quotaReserver    quotaenforce.Reserver
	settler          billing.Settler
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
		claimGate:       cfg.ClaimGate,
		quotaReserver:   cfg.QuotaReserver,
		settler:         cfg.Settler,
	}
}

func (s *PostgresStore) CreateTask(ctx context.Context, input CreateTaskInput) (Task, bool, error) {
	if s == nil || s.pool == nil {
		return Task{}, false, ErrStoreNotConfigured
	}
	if isDurablyBoundVideoProvider(input.Provider) {
		if !hasUnifiedMoneyBinding(input) {
			return Task{}, false, fmt.Errorf("%w: durable video provider requires exact request, key, pool, account, protocol, model and route binding", ErrInvalidInput)
		}
		if s.claimGate == nil || s.settler == nil {
			return Task{}, false, ErrStoreNotConfigured
		}
		return s.createTaskWithUnifiedMoney(ctx, input)
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

func hasUnifiedMoneyBinding(input CreateTaskInput) bool {
	return input.APIKeyID > 0 &&
		input.PoolGroupID > 0 &&
		input.ProviderAccountID > 0 &&
		strings.TrimSpace(input.RequestID) != "" &&
		strings.TrimSpace(input.ProtocolFamily) != "" &&
		strings.TrimSpace(input.RequestedModel) != "" &&
		strings.TrimSpace(input.ProviderModelID) != "" &&
		strings.TrimSpace(input.RouteID) != "" &&
		input.BindingID > 0 && input.BindingRPMLimit >= 0 && input.BindingTPMLimit >= 0 &&
		input.BindingMaxParallelRequests >= 0
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

func (s *PostgresStore) GetTaskForAPIKey(ctx context.Context, tenantID, userID, apiKeyID int64, requestID string) (Task, error) {
	if s == nil || s.pool == nil {
		return Task{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 || apiKeyID <= 0 || strings.TrimSpace(requestID) == "" {
		return Task{}, ErrNotFound
	}
	task, err := scanTask(s.pool.QueryRow(ctx, selectTaskSQL+`
WHERE tenant_id=$1 AND user_id=$2 AND api_key_id=$3 AND request_id=$4`,
		tenantID, userID, apiKeyID, strings.TrimSpace(requestID)))
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

func (s *PostgresStore) MarkSubmitting(ctx context.Context, task Task, owner string, now time.Time) (Task, error) {
	if s == nil || s.pool == nil {
		return Task{}, ErrStoreNotConfigured
	}
	updated, err := scanTask(s.pool.QueryRow(ctx, markSubmittingSQL, task.ID, owner, now.UTC()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrLeaseLost
	}
	return updated, err
}

// DeferSubmission 只用于能够证明尚未产生上游副作用的提交前失败。它把写前状态
// submitting 恢复为 queued 并设置下一次运行时间；提交结果未知时绝不能调用本方法。
func (s *PostgresStore) DeferSubmission(ctx context.Context, task Task, owner string, now, retryAt time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	if !retryAt.After(now) {
		retryAt = now.Add(5 * time.Second)
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE media_tasks
SET status='queued', error_class=NULL, lease_owner=NULL, lease_expires_at=$3, updated_at=$4
WHERE id=$1 AND lease_owner=$2 AND status='submitting'`,
		task.ID, owner, retryAt.UTC(), now.UTC(),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
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

// ReleaseLease 仅释放当前 worker 的任务租约，不改变任务状态或计费状态。
// 临时容量不足和可重试查询错误用它快速让任务回到队列，避免空等整个租约周期。
func (s *PostgresStore) ReleaseLease(ctx context.Context, task Task, owner string, now time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE media_tasks
SET lease_owner=NULL, lease_expires_at=NULL, updated_at=$3
WHERE id=$1 AND lease_owner=$2 AND status IN ('queued','in_progress')`, task.ID, owner, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// DeferLease 释放当前 worker 的租约，并把任务推迟到 retryAt 后再进入可运行队列。
func (s *PostgresStore) DeferLease(ctx context.Context, task Task, owner string, now, retryAt time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	if !retryAt.After(now) {
		retryAt = now.Add(5 * time.Second)
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE media_tasks
SET lease_owner=NULL, lease_expires_at=$3, updated_at=$4
WHERE id=$1 AND lease_owner=$2 AND status IN ('queued','in_progress')`,
		task.ID, owner, retryAt.UTC(), now.UTC())
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
		(input.APIKeyID <= 0 || existing.APIKeyID == input.APIKeyID) &&
		existing.TaskType == input.TaskType &&
		existing.Provider == input.Provider &&
		(input.ProviderAccountID <= 0 || existing.ProviderAccountID == input.ProviderAccountID) &&
		(input.PoolGroupID <= 0 || existing.PoolGroupID == input.PoolGroupID) &&
		(input.ProtocolFamily == "" || existing.ProtocolFamily == input.ProtocolFamily) &&
		(input.RequestedModel == "" || existing.RequestedModel == input.RequestedModel) &&
		(input.ProviderModelID == "" || existing.ProviderModelID == input.ProviderModelID) &&
		(input.RouteID == "" || existing.RouteID == input.RouteID) &&
		(input.BindingID <= 0 || existing.BindingID == input.BindingID) &&
		existing.BindingRPMLimit == input.BindingRPMLimit &&
		existing.BindingTPMLimit == input.BindingTPMLimit &&
		existing.BindingMaxParallelRequests == input.BindingMaxParallelRequests &&
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
	var protocolFamily, requestedModel, providerModelID, routeID sql.NullString
	var actual, apiKeyID, providerAccountID, poolGroupID sql.NullInt64
	var bindingID, bindingRPM, bindingTPM, bindingMaxParallel sql.NullInt64
	var leaseExpires, finished pgtype.Timestamptz
	err := row.Scan(
		&task.ID, &task.TenantID, &task.UserID, &apiKeyID, &task.TaskType, &task.Status, &task.Provider,
		&providerTaskID, &task.RequestID, &task.InputParams, &task.Result, &task.EstimatedCents,
		&actual, &holdRef, &errorClass, &task.Progress, &providerAccountID, &poolGroupID,
		&protocolFamily, &requestedModel, &providerModelID, &routeID,
		&bindingID, &bindingRPM, &bindingTPM, &bindingMaxParallel,
		&leaseOwner, &leaseExpires,
		&task.CreatedAt, &task.UpdatedAt, &finished,
	)
	if err != nil {
		return Task{}, err
	}
	task.ProviderTaskID = providerTaskID.String
	task.APIKeyID = apiKeyID.Int64
	task.ProviderAccountID = providerAccountID.Int64
	task.PoolGroupID = poolGroupID.Int64
	task.ProtocolFamily = protocolFamily.String
	task.RequestedModel = requestedModel.String
	task.ProviderModelID = providerModelID.String
	task.RouteID = routeID.String
	task.BindingID = bindingID.Int64
	task.BindingRPMLimit = bindingRPM.Int64
	task.BindingTPMLimit = bindingTPM.Int64
	task.BindingMaxParallelRequests = bindingMaxParallel.Int64
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
SELECT id, tenant_id, user_id, api_key_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, provider_account_id, pool_group_id, protocol_family,
       requested_model, provider_model_id, route_id, binding_id, binding_rpm_limit,
       binding_tpm_limit, binding_max_parallel_requests, lease_owner, lease_expires_at,
       created_at, updated_at, finished_at
FROM media_tasks`

const insertTaskSQL = `
INSERT INTO media_tasks (
	tenant_id, user_id, api_key_id, task_type, status, provider, request_id, input_params,
	estimated_cents, hold_ref, provider_account_id, pool_group_id, protocol_family,
	requested_model, provider_model_id, route_id, binding_id, binding_rpm_limit,
	binding_tpm_limit, binding_max_parallel_requests
) VALUES ($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10,NULLIF($11,0),NULLIF($12,0),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),NULLIF($17,0),$18,$19,$20)
RETURNING id, tenant_id, user_id, api_key_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, provider_account_id, pool_group_id, protocol_family,
       requested_model, provider_model_id, route_id, binding_id, binding_rpm_limit,
       binding_tpm_limit, binding_max_parallel_requests, lease_owner, lease_expires_at,
       created_at, updated_at, finished_at`

const acquireLeaseSQL = `
WITH candidate AS (
	SELECT id
	FROM media_tasks
	WHERE status IN ('queued','submitting','submission_releasing','in_progress','settlement_pending')
	  AND (lease_expires_at IS NULL OR lease_expires_at <= $3)
	ORDER BY updated_at ASC, id ASC
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
UPDATE media_tasks mt
SET lease_owner=$1, lease_expires_at=$2, updated_at=$3
FROM candidate c
WHERE mt.id=c.id
RETURNING mt.id, mt.tenant_id, mt.user_id, mt.api_key_id, mt.task_type, mt.status, mt.provider, mt.provider_task_id,
       mt.request_id, mt.input_params, mt.result, mt.estimated_cents, mt.actual_cents, mt.hold_ref,
       mt.error_class, mt.progress, mt.provider_account_id, mt.pool_group_id, mt.protocol_family,
       mt.requested_model, mt.provider_model_id, mt.route_id, mt.binding_id, mt.binding_rpm_limit,
       mt.binding_tpm_limit, mt.binding_max_parallel_requests, mt.lease_owner, mt.lease_expires_at,
       mt.created_at, mt.updated_at, mt.finished_at`

const markSubmittedSQL = `
UPDATE media_tasks
SET status='in_progress', provider_task_id=NULLIF(btrim($3), ''), progress=GREATEST(progress, 1),
    lease_owner=NULL, lease_expires_at=NULL, updated_at=$4
WHERE id=$1 AND lease_owner=$2 AND status='submitting'
  AND NULLIF(btrim($3), '') IS NOT NULL
RETURNING id, tenant_id, user_id, api_key_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, provider_account_id, pool_group_id, protocol_family,
       requested_model, provider_model_id, route_id, binding_id, binding_rpm_limit,
       binding_tpm_limit, binding_max_parallel_requests, lease_owner, lease_expires_at,
       created_at, updated_at, finished_at`

const markSubmittingSQL = `
UPDATE media_tasks
SET status='submitting', error_class=NULL, updated_at=$3
WHERE id=$1 AND lease_owner=$2 AND status='queued'
RETURNING id, tenant_id, user_id, api_key_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, provider_account_id, pool_group_id, protocol_family,
       requested_model, provider_model_id, route_id, binding_id, binding_rpm_limit,
       binding_tpm_limit, binding_max_parallel_requests, lease_owner, lease_expires_at,
       created_at, updated_at, finished_at`
