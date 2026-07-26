package mediatask

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const maxProviderTaskIDBytes = 512

type SubmissionRecoveryResult struct {
	OrphanID       int64
	TaskID         int64
	TenantID       int64
	UserID         int64
	Provider       string
	ProviderTaskID string
	TaskStatus     Status
	OrphanStatus   string
	EstimatedCents int64
}

type SubmissionRecoveryAuditHook func(
	context.Context,
	pgx.Tx,
	SubmissionRecoveryResult,
) error

type SubmissionRecoveryAccessHook func(
	context.Context,
	SubmissionRecoveryResult,
) error

// AttachUnknownSubmission 把运营人员从供应商侧核实到的任务号绑定回原任务。
// 绑定只允许在原 claim 仍为 reserving 时发生，随后 worker 只轮询原账号和原任务，
// 不会再次执行创建请求。
func (s *PostgresStore) AttachUnknownSubmission(
	ctx context.Context,
	orphanID int64,
	providerTaskID string,
	now time.Time,
	access SubmissionRecoveryAccessHook,
	audit SubmissionRecoveryAuditHook,
) (SubmissionRecoveryResult, bool, error) {
	if s == nil || s.pool == nil {
		return SubmissionRecoveryResult{}, false, ErrStoreNotConfigured
	}
	providerTaskID = strings.TrimSpace(providerTaskID)
	if !validProviderTaskID(providerTaskID) {
		return SubmissionRecoveryResult{}, false, ErrInvalidInput
	}
	var result SubmissionRecoveryResult
	var advanced bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		rec, task, err := lockUnknownSubmission(ctx, tx, orphanID, access)
		if err != nil {
			return err
		}
		result = submissionRecoveryResult(rec, task)
		if rec.ReconcileStatus == "reconciled" &&
			task.Status == StatusInProgress &&
			task.ProviderTaskID == providerTaskID {
			result.ProviderTaskID = task.ProviderTaskID
			return nil
		}
		if rec.ReconcileStatus != "pending" || task.Status != StatusSubmissionUnknown {
			return ErrSubmissionNotUnknown
		}
		claimID, err := claimIDFromHoldRef(task.HoldRef)
		if err != nil {
			return err
		}
		var claimStatus string
		if err := tx.QueryRow(ctx, `
SELECT status
FROM billing_ledger_claims
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, task.TenantID, claimID).Scan(&claimStatus); err != nil {
			return err
		}
		if claimStatus != "reserving" {
			return ErrSubmissionClaimClosed
		}
		var conflict bool
		if err := tx.QueryRow(ctx, `
	SELECT EXISTS (
	    SELECT 1
	    FROM media_tasks mt
	    WHERE mt.id <> $1
	      AND mt.provider = $2
	      AND COALESCE(mt.provider_account_id, 0) = $3
	      AND mt.provider_task_id = $4
	    UNION ALL
	    SELECT 1
	    FROM media_task_orphans mo
	    WHERE mo.provider = $2
	      AND mo.provider_task_id IS NOT NULL
	      AND (
	          (mo.task_id <> $1 AND mo.provider_task_id = $4)
	          OR (mo.task_id = $1 AND mo.provider_task_id <> $4)
	      )
	)`, task.ID, task.Provider, task.ProviderAccountID, providerTaskID).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			return ErrProviderTaskIDConflict
		}
		tag, err := tx.Exec(ctx, `
UPDATE media_tasks
SET status='in_progress', provider_task_id=$2, error_class=NULL,
    progress=GREATEST(progress, 1), lease_owner=NULL, lease_expires_at=NULL,
    updated_at=$3
WHERE id=$1 AND status='submission_unknown'`, task.ID, providerTaskID, now.UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrSubmissionNotUnknown
		}
		tag, err = tx.Exec(ctx, `
UPDATE media_task_orphans
SET reconcile_status='reconciled', reconciled_at=$2
WHERE id=$1 AND orphan_kind='submission_unknown' AND reconcile_status='pending'`,
			orphanID, now.UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrSubmissionNotUnknown
		}
		if _, err := tx.Exec(ctx, `
UPDATE media_task_orphans
SET reconcile_status='reconciled', reconciled_at=$4
WHERE task_id=$1
  AND provider=$2
  AND provider_task_id=$3
  AND orphan_kind='provider_task_orphan'
  AND reconcile_status='pending'`,
			task.ID, task.Provider, providerTaskID, now.UTC(),
		); err != nil {
			return err
		}
		result.ProviderTaskID = providerTaskID
		result.TaskStatus = StatusInProgress
		result.OrphanStatus = "reconciled"
		if audit != nil {
			if err := audit(ctx, tx, result); err != nil {
				return err
			}
		}
		advanced = true
		return nil
	})
	return result, advanced, err
}

// RequestUnknownSubmissionRelease 记录“供应商已确认未受理”的人工裁决，并把任务
// 推进到耐久退款队列。实际释放由 worker 调统一 Settler 完成；中途崩溃会继续重试。
func (s *PostgresStore) RequestUnknownSubmissionRelease(
	ctx context.Context,
	orphanID int64,
	now time.Time,
	access SubmissionRecoveryAccessHook,
	audit SubmissionRecoveryAuditHook,
) (SubmissionRecoveryResult, bool, error) {
	if s == nil || s.pool == nil {
		return SubmissionRecoveryResult{}, false, ErrStoreNotConfigured
	}
	var result SubmissionRecoveryResult
	var advanced bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		rec, task, err := lockUnknownSubmission(ctx, tx, orphanID, access)
		if err != nil {
			return err
		}
		result = submissionRecoveryResult(rec, task)
		if rec.ReconcileStatus == "release_requested" && task.Status == StatusSubmissionReleasing {
			return nil
		}
		if rec.ReconcileStatus != "pending" || task.Status != StatusSubmissionUnknown {
			return ErrSubmissionNotUnknown
		}
		claimID, err := claimIDFromHoldRef(task.HoldRef)
		if err != nil {
			return err
		}
		var claimStatus string
		if err := tx.QueryRow(ctx, `
SELECT status
FROM billing_ledger_claims
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, task.TenantID, claimID).Scan(&claimStatus); err != nil {
			return err
		}
		if claimStatus != "reserving" {
			return ErrSubmissionClaimClosed
		}
		var acceptedEvidence bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM media_task_orphans
    WHERE task_id=$1
      AND provider=$2
      AND provider_task_id IS NOT NULL
)`, task.ID, task.Provider).Scan(&acceptedEvidence); err != nil {
			return err
		}
		if acceptedEvidence {
			return ErrProviderTaskIDConflict
		}
		tag, err := tx.Exec(ctx, `
UPDATE media_tasks
SET status='submission_releasing', error_class='provider_submit_confirmed_not_accepted',
    lease_owner=NULL, lease_expires_at=NULL, updated_at=$2
WHERE id=$1 AND status='submission_unknown'`, task.ID, now.UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrSubmissionNotUnknown
		}
		tag, err = tx.Exec(ctx, `
UPDATE media_task_orphans
SET reconcile_status='release_requested'
WHERE id=$1 AND orphan_kind='submission_unknown' AND reconcile_status='pending'`, orphanID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrSubmissionNotUnknown
		}
		result.TaskStatus = StatusSubmissionReleasing
		result.OrphanStatus = "release_requested"
		if audit != nil {
			if err := audit(ctx, tx, result); err != nil {
				return err
			}
		}
		advanced = true
		return nil
	})
	return result, advanced, err
}

func lockUnknownSubmission(
	ctx context.Context,
	tx pgx.Tx,
	orphanID int64,
	access SubmissionRecoveryAccessHook,
) (OrphanRecord, Task, error) {
	var rec OrphanRecord
	var providerTaskID pgtype.Text
	err := tx.QueryRow(ctx, `
SELECT id, task_id, tenant_id, user_id, provider, provider_task_id,
       orphan_kind, reconcile_status, observed_at
FROM media_task_orphans
WHERE id=$1
FOR UPDATE`, orphanID).Scan(
		&rec.ID, &rec.TaskID, &rec.TenantID, &rec.UserID, &rec.Provider,
		&providerTaskID, &rec.OrphanKind, &rec.ReconcileStatus, &rec.ObservedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrphanRecord{}, Task{}, ErrNotFound
	}
	if err != nil {
		return OrphanRecord{}, Task{}, err
	}
	if providerTaskID.Valid {
		rec.ProviderTaskID = providerTaskID.String
	}
	if access == nil {
		return OrphanRecord{}, Task{}, ErrSubmissionAccessNotConfigured
	}
	// 一旦拿到归属事实就先鉴权，不能先暴露孤儿类型、状态、幂等终态或任务号冲突。
	if err := access(ctx, SubmissionRecoveryResult{
		OrphanID:       rec.ID,
		TaskID:         rec.TaskID,
		TenantID:       rec.TenantID,
		UserID:         rec.UserID,
		Provider:       rec.Provider,
		ProviderTaskID: rec.ProviderTaskID,
		OrphanStatus:   rec.ReconcileStatus,
	}); err != nil {
		return OrphanRecord{}, Task{}, err
	}
	if rec.OrphanKind != "submission_unknown" {
		return OrphanRecord{}, Task{}, ErrSubmissionNotUnknown
	}
	task, err := scanTask(tx.QueryRow(ctx, selectTaskSQL+`
 WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, rec.TaskID, rec.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OrphanRecord{}, Task{}, ErrNotFound
	}
	return rec, task, err
}

func submissionRecoveryResult(rec OrphanRecord, task Task) SubmissionRecoveryResult {
	return SubmissionRecoveryResult{
		OrphanID: rec.ID, TaskID: task.ID, TenantID: task.TenantID, UserID: task.UserID,
		Provider: task.Provider, ProviderTaskID: rec.ProviderTaskID,
		TaskStatus: task.Status, OrphanStatus: rec.ReconcileStatus,
		EstimatedCents: task.EstimatedCents,
	}
}

func validProviderTaskID(value string) bool {
	if value == "" || len(value) > maxProviderTaskIDBytes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func finalizeUnknownSubmissionRelease(
	ctx context.Context,
	tx pgx.Tx,
	taskID int64,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
UPDATE media_task_orphans
SET reconcile_status='cancelled', reconciled_at=$2
WHERE task_id=$1
  AND orphan_kind='submission_unknown'
  AND reconcile_status='release_requested'`, taskID, now.UTC())
	return err
}
