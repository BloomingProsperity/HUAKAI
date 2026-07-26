package mediatask

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const insertUnknownSubmissionSQL = `
INSERT INTO media_task_orphans (
    task_id, tenant_id, user_id, provider, provider_task_id, lease_owner, observed_at,
    orphan_kind, idempotency_key, error_class
) VALUES ($1,$2,$3,$4,NULL,$5,$6,'submission_unknown',NULLIF($7,''),NULLIF($8,''))
ON CONFLICT (task_id) WHERE orphan_kind = 'submission_unknown'
DO UPDATE SET
    observed_at = LEAST(media_task_orphans.observed_at, EXCLUDED.observed_at),
    idempotency_key = COALESCE(media_task_orphans.idempotency_key, EXCLUDED.idempotency_key),
    error_class = COALESCE(media_task_orphans.error_class, EXCLUDED.error_class)`

// MarkSubmissionUnknown 在一个事务里把写前提交态推进为未知态并建立运维恢复记录。
// 该事务不释放预扣，也不生成新的上游请求；账务清扫器会在未知态存续期间保护 claim。
func (s *PostgresStore) MarkSubmissionUnknown(
	ctx context.Context,
	task Task,
	owner string,
	errorClass string,
	idempotencyKey string,
	now time.Time,
) (Task, error) {
	if s == nil || s.pool == nil {
		return Task{}, ErrStoreNotConfigured
	}
	errorClass = firstNonEmpty(strings.TrimSpace(errorClass), "provider_submit_outcome_unknown")
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	var updated Task
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		locked, err := scanTask(tx.QueryRow(ctx, selectTaskSQL+` WHERE id=$1 FOR UPDATE`, task.ID))
		if err != nil {
			return err
		}
		if locked.LeaseOwner != owner ||
			(locked.Status != StatusSubmitting && locked.Status != StatusInProgress) ||
			strings.TrimSpace(locked.ProviderTaskID) != "" {
			return ErrLeaseLost
		}
		updated, err = scanTask(tx.QueryRow(ctx, `
UPDATE media_tasks
SET status='submission_unknown', error_class=$3,
    lease_owner=NULL, lease_expires_at=NULL, updated_at=$4
WHERE id=$1 AND lease_owner=$2
  AND status IN ('submitting','in_progress')
  AND provider_task_id IS NULL
RETURNING id, tenant_id, user_id, api_key_id, task_type, status, provider, provider_task_id,
       request_id, input_params, result, estimated_cents, actual_cents, hold_ref,
       error_class, progress, provider_account_id, pool_group_id, protocol_family,
       requested_model, provider_model_id, route_id, binding_id, binding_rpm_limit,
       binding_tpm_limit, binding_max_parallel_requests, lease_owner, lease_expires_at,
       created_at, updated_at, finished_at`,
			locked.ID, owner, errorClass, now.UTC()))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrLeaseLost
			}
			return err
		}
		_, err = tx.Exec(ctx, insertUnknownSubmissionSQL,
			locked.ID, locked.TenantID, locked.UserID, locked.Provider,
			owner, now.UTC(), idempotencyKey, errorClass)
		return err
	})
	return updated, err
}
