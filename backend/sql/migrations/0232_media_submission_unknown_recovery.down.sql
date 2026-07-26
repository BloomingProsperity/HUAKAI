BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM media_tasks
        WHERE status IN (
            'submitting',
            'submission_unknown',
            'submission_releasing',
            'settlement_pending'
        )
    ) OR EXISTS (
        SELECT 1
        FROM media_task_orphans
        WHERE orphan_kind = 'submission_unknown'
           OR reconcile_status = 'release_requested'
    ) THEN
        RAISE EXCEPTION 'refusing to roll back 0232 while media submission recovery state exists';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_media_task_orphans_recovery_queue;
DROP INDEX IF EXISTS uq_media_task_orphans_unknown_submission;
DROP INDEX IF EXISTS uq_media_task_orphans_task_provider;
DROP INDEX IF EXISTS idx_media_tasks_claim_recovery_active;
DROP INDEX IF EXISTS uq_media_tasks_provider_account_task;

ALTER TABLE media_task_orphans
    DROP CONSTRAINT IF EXISTS media_task_orphans_provider_identity_check,
    DROP CONSTRAINT IF EXISTS media_task_orphans_kind_check,
    DROP CONSTRAINT IF EXISTS media_task_orphans_reconcile_status_check,
    DROP COLUMN IF EXISTS error_class,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS orphan_kind,
    ALTER COLUMN provider_task_id SET NOT NULL,
    ADD CONSTRAINT media_task_orphans_reconcile_status_check
        CHECK (reconcile_status IN ('pending', 'reconciled', 'cancelled', 'ignored'));

CREATE UNIQUE INDEX uq_media_task_orphans_task_provider
    ON media_task_orphans (task_id, provider_task_id);

DROP INDEX IF EXISTS idx_media_tasks_binding_active;
CREATE INDEX idx_media_tasks_binding_active
    ON media_tasks (tenant_id, binding_id, status)
    WHERE binding_id IS NOT NULL
      AND status IN ('queued', 'in_progress');

DROP INDEX IF EXISTS idx_media_tasks_provider_account_active;
CREATE INDEX idx_media_tasks_provider_account_active
    ON media_tasks (tenant_id, provider_account_id, status)
    WHERE provider_account_id IS NOT NULL
      AND status IN ('queued', 'in_progress');

DROP INDEX IF EXISTS idx_media_tasks_runnable_status;
CREATE INDEX idx_media_tasks_runnable_status
    ON media_tasks (status)
    WHERE status IN ('queued', 'in_progress');

ALTER TABLE media_tasks
    DROP CONSTRAINT IF EXISTS media_tasks_status_check,
    ADD CONSTRAINT media_tasks_status_check
        CHECK (status IN ('queued', 'in_progress', 'succeeded', 'failed', 'expired'));

COMMIT;
