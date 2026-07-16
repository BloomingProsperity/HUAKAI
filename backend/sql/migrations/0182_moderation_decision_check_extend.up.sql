BEGIN;

-- 与代码 Decision 枚举全集对齐，并保留收费审计事件；新增 decision 时必须同步
-- 检查此 CHECK，避免外部审核结论因数据库白名单滞后而无法落库。
ALTER TABLE moderation_log
    DROP CONSTRAINT IF EXISTS moderation_log_decision_check;

ALTER TABLE moderation_log
    ADD CONSTRAINT moderation_log_decision_check CHECK (
        decision IN (
            'pass',
            'block_keyword',
            'block_hash',
            'block_external',
            'block_backend',
            'fee_charged'
        )
    );

-- 与代码声明的阻断 Decision 全集对齐；新增 decision 时必须同步检查此
-- CHECK，避免违规事件写入失败后中断计数与封号链。
ALTER TABLE moderation_violation_events
    DROP CONSTRAINT IF EXISTS moderation_violation_events_decision_check;

ALTER TABLE moderation_violation_events
    ADD CONSTRAINT moderation_violation_events_decision_check CHECK (
        decision IN (
            'block_keyword',
            'block_hash',
            'block_external',
            'block_backend'
        )
    );

COMMIT;
