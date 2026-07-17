BEGIN;

-- 回滚到 0082 的原始审计 decision 白名单；若仍有新增 decision 数据，
-- PostgreSQL 会拒绝恢复约束并回滚整个事务，不会静默删除审计记录。
ALTER TABLE moderation_log
    DROP CONSTRAINT IF EXISTS moderation_log_decision_check;

ALTER TABLE moderation_log
    ADD CONSTRAINT moderation_log_decision_check CHECK (
        decision IN (
            'pass',
            'block_keyword',
            'block_hash',
            'block_backend',
            'fee_charged'
        )
    );

-- 回滚到 0090 的原始违规事件 decision 白名单；调用方必须先清理新增
-- decision 测试数据，保证 CI 的空数据往返可恢复旧约束。
ALTER TABLE moderation_violation_events
    DROP CONSTRAINT IF EXISTS moderation_violation_events_decision_check;

ALTER TABLE moderation_violation_events
    ADD CONSTRAINT moderation_violation_events_decision_check CHECK (
        decision IN (
            'block_keyword',
            'block_hash'
        )
    );

COMMIT;
