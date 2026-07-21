-- 异步视频任务必须保存创建时命中的模型绑定合同，后台提交不得脱离原绑定的
-- RPM、TPM 与并发上限。历史任务允许为空，由兼容路径按无限制处理。
ALTER TABLE media_tasks
    ADD COLUMN IF NOT EXISTS binding_id bigint REFERENCES model_pool_bindings(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS binding_rpm_limit bigint,
    ADD COLUMN IF NOT EXISTS binding_tpm_limit bigint,
    ADD COLUMN IF NOT EXISTS binding_max_parallel_requests bigint;

ALTER TABLE media_tasks
    ADD CONSTRAINT media_tasks_binding_rpm_limit_nonnegative
        CHECK (binding_rpm_limit IS NULL OR binding_rpm_limit >= 0),
    ADD CONSTRAINT media_tasks_binding_tpm_limit_nonnegative
        CHECK (binding_tpm_limit IS NULL OR binding_tpm_limit >= 0),
    ADD CONSTRAINT media_tasks_binding_max_parallel_nonnegative
        CHECK (binding_max_parallel_requests IS NULL OR binding_max_parallel_requests >= 0);

CREATE INDEX IF NOT EXISTS idx_media_tasks_binding_active
    ON media_tasks (tenant_id, binding_id, status)
    WHERE binding_id IS NOT NULL
      AND status IN ('queued', 'in_progress');
