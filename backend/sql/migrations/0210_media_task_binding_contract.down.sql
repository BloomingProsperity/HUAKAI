DROP INDEX IF EXISTS idx_media_tasks_binding_active;

ALTER TABLE media_tasks
    DROP CONSTRAINT IF EXISTS media_tasks_binding_max_parallel_nonnegative,
    DROP CONSTRAINT IF EXISTS media_tasks_binding_tpm_limit_nonnegative,
    DROP CONSTRAINT IF EXISTS media_tasks_binding_rpm_limit_nonnegative,
    DROP COLUMN IF EXISTS binding_max_parallel_requests,
    DROP COLUMN IF EXISTS binding_tpm_limit,
    DROP COLUMN IF EXISTS binding_rpm_limit,
    DROP COLUMN IF EXISTS binding_id;
