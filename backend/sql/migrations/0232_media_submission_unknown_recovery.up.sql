BEGIN;

ALTER TABLE media_tasks
    DROP CONSTRAINT IF EXISTS media_tasks_status_check;

ALTER TABLE media_tasks
    ADD CONSTRAINT media_tasks_status_check
        CHECK (status IN (
            'queued',
            'submitting',
            'submission_unknown',
            'submission_releasing',
            'in_progress',
            'settlement_pending',
            'succeeded',
            'failed',
            'expired'
        ));

DROP INDEX IF EXISTS idx_media_tasks_runnable_status;
CREATE INDEX idx_media_tasks_runnable_status
    ON media_tasks (status, lease_expires_at, updated_at, id)
    WHERE status IN (
        'queued',
        'submitting',
        'submission_releasing',
        'in_progress',
        'settlement_pending'
    );

CREATE INDEX idx_media_tasks_claim_recovery_active
    ON media_tasks (tenant_id, hold_ref)
    WHERE status IN ('submission_unknown', 'submission_releasing', 'settlement_pending')
      AND hold_ref IS NOT NULL;

DO $$
DECLARE
    duplicate_count BIGINT;
BEGIN
    UPDATE media_tasks
    SET provider_task_id = NULLIF(btrim(provider_task_id), '')
    WHERE provider_task_id IS DISTINCT FROM NULLIF(btrim(provider_task_id), '');

    SELECT count(*)
    INTO duplicate_count
    FROM (
        SELECT provider, COALESCE(provider_account_id, 0), provider_task_id
        FROM media_tasks
        WHERE provider_task_id IS NOT NULL
        GROUP BY provider, COALESCE(provider_account_id, 0), provider_task_id
        HAVING count(*) > 1
    ) AS conflicts;

    IF duplicate_count > 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '23505',
            MESSAGE = format(
                '0232 检测到 %s 组重复媒体上游任务号，拒绝自动选择账号；请按 provider、provider_account_id、provider_task_id 人工消歧后重试',
                duplicate_count
            );
    END IF;
END $$;

CREATE UNIQUE INDEX uq_media_tasks_provider_account_task
    ON media_tasks (provider, COALESCE(provider_account_id, 0), provider_task_id)
    WHERE provider_task_id IS NOT NULL;

DROP INDEX IF EXISTS idx_media_tasks_provider_account_active;
CREATE INDEX idx_media_tasks_provider_account_active
    ON media_tasks (tenant_id, provider_account_id, status)
    WHERE provider_account_id IS NOT NULL
      AND status IN (
          'queued',
          'submitting',
          'submission_unknown',
          'submission_releasing',
          'in_progress',
          'settlement_pending'
      );

DROP INDEX IF EXISTS idx_media_tasks_binding_active;
CREATE INDEX idx_media_tasks_binding_active
    ON media_tasks (tenant_id, binding_id, status)
    WHERE binding_id IS NOT NULL
      AND status IN (
          'queued',
          'submitting',
          'submission_unknown',
          'submission_releasing',
          'in_progress',
          'settlement_pending'
      );

ALTER TABLE media_task_orphans
    DROP CONSTRAINT IF EXISTS media_task_orphans_reconcile_status_check;

ALTER TABLE media_task_orphans
    ALTER COLUMN provider_task_id DROP NOT NULL;

DO $$
DECLARE
    duplicate_count BIGINT;
BEGIN
    UPDATE media_task_orphans
    SET provider_task_id = NULLIF(btrim(provider_task_id), '')
    WHERE provider_task_id IS DISTINCT FROM NULLIF(btrim(provider_task_id), '');

    SELECT count(*)
    INTO duplicate_count
    FROM (
        SELECT task_id, provider_task_id
        FROM media_task_orphans
        WHERE provider_task_id IS NOT NULL
        GROUP BY task_id, provider_task_id
        HAVING count(*) > 1
    ) AS conflicts;

    IF duplicate_count > 0 THEN
        RAISE EXCEPTION USING
            ERRCODE = '23505',
            MESSAGE = format(
                '0232 检测到 %s 组重复媒体孤儿任务号，拒绝自动合并；请按 task_id、provider_task_id 人工消歧后重试',
                duplicate_count
            );
    END IF;
END $$;

ALTER TABLE media_task_orphans
    ADD COLUMN orphan_kind TEXT NOT NULL DEFAULT 'provider_task_orphan',
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN error_class TEXT,
    ADD CONSTRAINT media_task_orphans_reconcile_status_check
        CHECK (reconcile_status IN (
            'pending',
            'release_requested',
            'reconciled',
            'cancelled',
            'ignored'
        )),
    ADD CONSTRAINT media_task_orphans_kind_check
        CHECK (orphan_kind IN ('provider_task_orphan', 'submission_unknown')),
    ADD CONSTRAINT media_task_orphans_provider_identity_check
        CHECK (
            (orphan_kind = 'provider_task_orphan' AND NULLIF(btrim(provider_task_id), '') IS NOT NULL)
            OR orphan_kind = 'submission_unknown'
        );

DROP INDEX IF EXISTS uq_media_task_orphans_task_provider;
CREATE UNIQUE INDEX uq_media_task_orphans_task_provider
    ON media_task_orphans (task_id, provider_task_id)
    WHERE provider_task_id IS NOT NULL;

CREATE UNIQUE INDEX uq_media_task_orphans_unknown_submission
    ON media_task_orphans (task_id)
    WHERE orphan_kind = 'submission_unknown';

CREATE INDEX idx_media_task_orphans_recovery_queue
    ON media_task_orphans (reconcile_status, observed_at, id)
    WHERE orphan_kind = 'submission_unknown'
      AND reconcile_status IN ('pending', 'release_requested');

COMMIT;
