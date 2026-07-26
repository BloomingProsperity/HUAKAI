package mediatask

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// OrphanRecord 是一条持久化的孤儿上游任务对账线索(media_task_orphans 行)。
// 孤儿 = worker 已在上游创建任务(ProviderTaskID),却因租约在 Submit 期间被抢走而无法
// 把该 ID 落回 media_tasks;上游可能跑完并计费,本平台却无对应扣费,需对账。
type OrphanRecord struct {
	ID              int64
	TaskID          int64
	TenantID        int64
	UserID          int64
	Provider        string
	ProviderTaskID  string
	OrphanKind      string
	IdempotencyKey  string
	ErrorClass      string
	TaskStatus      Status
	EstimatedCents  int64
	LeaseOwner      string
	ObservedAt      time.Time
	ReconcileStatus string
	ReconciledAt    *time.Time
}

// 幂等插入:(task_id, provider_task_id) 唯一,重复上报(多 worker 撞同一孤儿或重试)不重复入账。
const insertOrphanSQL = `
INSERT INTO media_task_orphans
    (task_id, tenant_id, user_id, provider, provider_task_id, lease_owner, observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (task_id, provider_task_id) WHERE provider_task_id IS NOT NULL DO NOTHING`

// PersistOrphan 幂等持久化一条孤儿线索。ProviderTaskID 为空时无对账价值,直接跳过不入账。
func (s *PostgresStore) PersistOrphan(ctx context.Context, rec OrphanRecord) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	providerTaskID := strings.TrimSpace(rec.ProviderTaskID)
	if providerTaskID == "" {
		return nil
	}
	observedAt := rec.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	_, err := s.pool.Exec(ctx, insertOrphanSQL,
		rec.TaskID, rec.TenantID, rec.UserID, rec.Provider,
		providerTaskID, rec.LeaseOwner, observedAt.UTC())
	return err
}

const listPendingOrphansSQL = `
SELECT o.id, o.task_id, o.tenant_id, o.user_id, o.provider, o.provider_task_id,
       o.orphan_kind, o.idempotency_key, o.error_class, mt.status, mt.estimated_cents,
       o.lease_owner, o.observed_at, o.reconcile_status, o.reconciled_at
FROM media_task_orphans o
LEFT JOIN media_tasks mt
  ON mt.id = o.task_id
 AND mt.tenant_id = o.tenant_id
WHERE o.reconcile_status IN ('pending', 'release_requested')
  AND ($1 <= 0 OR o.tenant_id = $1)
ORDER BY o.observed_at ASC, o.id ASC
LIMIT $2`

// ListPendingOrphans 列出待对账孤儿(对账消费者 / 运维用)。tenantID<=0 表示跨租户全局扫(管理员);
// 否则限定该租户。limit<=0 取默认 100,上限 1000 防一次性拉爆。
func (s *PostgresStore) ListPendingOrphans(ctx context.Context, tenantID int64, limit int) ([]OrphanRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, listPendingOrphansSQL, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrphanRecord
	for rows.Next() {
		var rec OrphanRecord
		var providerTaskID, idempotencyKey, errorClass pgtype.Text
		var taskStatus pgtype.Text
		var estimatedCents pgtype.Int8
		var reconciledAt pgtype.Timestamptz
		if err := rows.Scan(&rec.ID, &rec.TaskID, &rec.TenantID, &rec.UserID, &rec.Provider,
			&providerTaskID, &rec.OrphanKind, &idempotencyKey, &errorClass, &taskStatus, &estimatedCents,
			&rec.LeaseOwner, &rec.ObservedAt, &rec.ReconcileStatus, &reconciledAt); err != nil {
			return nil, err
		}
		if providerTaskID.Valid {
			rec.ProviderTaskID = providerTaskID.String
		}
		if idempotencyKey.Valid {
			rec.IdempotencyKey = idempotencyKey.String
		}
		if errorClass.Valid {
			rec.ErrorClass = errorClass.String
		}
		if taskStatus.Valid {
			rec.TaskStatus = Status(taskStatus.String)
		}
		if estimatedCents.Valid {
			rec.EstimatedCents = estimatedCents.Int64
		}
		if reconciledAt.Valid {
			t := reconciledAt.Time
			rec.ReconciledAt = &t
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// 终态守卫:仅 pending 行可被推进到终态,已对账的行不再变更(幂等,防重复对账动作覆盖时间戳)。
const markOrphanReconciledSQL = `
UPDATE media_task_orphans
SET reconcile_status = $2, reconciled_at = $3
WHERE id = $1 AND reconcile_status = 'pending'`

// MarkOrphanReconciled 把孤儿从 pending 推进到终态(对账消费者用)。status 必须是
// reconciled / cancelled / ignored 之一;非法值或 pending 自身一律拒绝。返回是否真改了行
// (false = 该行不存在或已是终态,调用方据此判断是否重复对账)。
func (s *PostgresStore) MarkOrphanReconciled(ctx context.Context, id int64, status string, now time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	switch status {
	case "reconciled", "cancelled", "ignored":
	default:
		return false, ErrInvalidOrphanStatus
	}
	tag, err := s.pool.Exec(ctx, markOrphanReconciledSQL, id, status, now.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
