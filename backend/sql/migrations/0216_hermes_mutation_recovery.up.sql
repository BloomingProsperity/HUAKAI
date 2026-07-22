BEGIN;

-- 每次已确认变更使用同一个操作号关联工具日志与管理员日志，唯一索引同时阻止恢复任务重复补记。
ALTER TABLE hermes_tool_calls
    ADD COLUMN operation_id UUID;

ALTER TABLE admin_audit_events
    ADD COLUMN operation_id UUID;

CREATE UNIQUE INDEX hermes_tool_calls_operation_id_unique
    ON hermes_tool_calls (operation_id)
    WHERE operation_id IS NOT NULL;

CREATE UNIQUE INDEX admin_audit_events_operation_id_unique
    ON admin_audit_events (operation_id)
    WHERE operation_id IS NOT NULL;

-- 独立事务工具不能与两类日志共用一个数据库事务。该表先持久化已确认意图，再记录执行结果，
-- 最后由同一事务补齐两类日志并标记完成。进程崩溃或数据库连接中断后，恢复任务可从任一阶段续跑。
CREATE TABLE hermes_mutation_recovery (
    operation_id       UUID PRIMARY KEY,
    tenant_id          BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_source       TEXT NOT NULL,
    actor_id           BIGINT NOT NULL,
    actor_role         TEXT NOT NULL,
    tool_name          TEXT NOT NULL,
    requested_args     JSONB NOT NULL DEFAULT '{}'::jsonb,
    admin_action       TEXT NOT NULL,
    target_type        TEXT NOT NULL,
    target_id          BIGINT NOT NULL,
    audit_payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    correlation_id     TEXT,
    request_id         TEXT,
    result_status      TEXT NOT NULL DEFAULT 'prepared',
    result_summary     JSONB,
    error_class        TEXT,
    called_at          TIMESTAMPTZ NOT NULL,
    returned_at        TIMESTAMPTZ,
    recovery_attempts  INTEGER NOT NULL DEFAULT 0,
    next_recovery_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_recovery_at   TIMESTAMPTZ,
    lease_owner        TEXT,
    lease_until        TIMESTAMPTZ,
    audit_committed_at TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    ingested_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    log_category       TEXT NOT NULL DEFAULT 'recovery',
    CONSTRAINT hermes_mutation_recovery_actor_source_check
        CHECK (actor_source IN ('token', 'session')),
    CONSTRAINT hermes_mutation_recovery_actor_id_check
        CHECK (actor_id > 0),
    CONSTRAINT hermes_mutation_recovery_actor_role_check
        CHECK (actor_role IN ('platform_admin', 'tenant_operator')),
    CONSTRAINT hermes_mutation_recovery_tool_check
        CHECK (tool_name = 'dlq_replay'),
    CONSTRAINT hermes_mutation_recovery_target_check
        CHECK (target_id > 0),
    CONSTRAINT hermes_mutation_recovery_status_check
        CHECK (result_status IN ('prepared', 'ok', 'error')),
    CONSTRAINT hermes_mutation_recovery_result_check
        CHECK (
            (result_status = 'prepared' AND returned_at IS NULL AND error_class IS NULL)
            OR (result_status = 'ok' AND returned_at IS NOT NULL AND error_class IS NULL)
            OR (result_status = 'error' AND returned_at IS NOT NULL AND btrim(error_class) <> '')
        ),
    CONSTRAINT hermes_mutation_recovery_lease_check
        CHECK ((lease_owner IS NULL) = (lease_until IS NULL)),
    CONSTRAINT hermes_mutation_recovery_audit_check
        CHECK (audit_committed_at IS NULL OR result_status <> 'prepared'),
    CONSTRAINT hermes_mutation_recovery_category_check
        CHECK (log_category = 'recovery')
);

CREATE INDEX hermes_mutation_recovery_due_idx
    ON hermes_mutation_recovery (next_recovery_at, created_at, operation_id)
    WHERE audit_committed_at IS NULL;

CREATE INDEX hermes_mutation_recovery_retention_idx
    ON hermes_mutation_recovery (ingested_at, operation_id);

COMMIT;
