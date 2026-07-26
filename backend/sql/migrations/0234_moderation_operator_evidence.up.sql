BEGIN;

ALTER TABLE api_keys
    ADD COLUMN status_generation bigint NOT NULL DEFAULT 0
        CHECK (status_generation >= 0);

CREATE OR REPLACE FUNCTION bump_api_key_status_generation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        NEW.status_generation := OLD.status_generation + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_api_keys_status_generation
    BEFORE UPDATE OF status ON api_keys
    FOR EACH ROW
    EXECUTE FUNCTION bump_api_key_status_generation();

ALTER TABLE moderation_config
    ADD COLUMN auto_disable_key_on_ban boolean NOT NULL DEFAULT false;

ALTER TABLE moderation_log
    DROP COLUMN payload_hash,
    ADD COLUMN violation_event_id bigint,
    ADD COLUMN input_excerpt text NOT NULL DEFAULT '',
    ADD COLUMN violation_count bigint NOT NULL DEFAULT 0
        CHECK (violation_count >= 0),
    ADD COLUMN threshold_reached boolean NOT NULL DEFAULT false,
    ADD COLUMN key_disabled boolean NOT NULL DEFAULT false,
    ADD COLUMN actor_id text,
    ADD COLUMN actor_role text,
    ADD CONSTRAINT moderation_log_input_excerpt_length_check
        CHECK (char_length(input_excerpt) <= 240);

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
            'fee_charged',
            'admin_disable',
            'admin_unban'
        )
    );

-- 旧表没有跨租户外键和请求唯一约束，历史坏关联不能升级为“永久事实”。
-- 项目尚未上线，迁移时删除无法绑定真实租户用户 Key 的旧事件。
DELETE FROM moderation_violation_events v
WHERE NOT EXISTS (
        SELECT 1
        FROM api_keys ak
        WHERE ak.tenant_id = v.tenant_id
          AND ak.id = v.api_key_id
          AND ak.user_id = v.user_id
    )
   OR NOT EXISTS (
        SELECT 1
        FROM users u
        WHERE u.tenant_id = v.tenant_id
          AND u.id = v.user_id
    );

-- 保留原本有效且唯一的请求身份；只修补空值和同一 Key 下的重复值。
UPDATE moderation_violation_events
SET request_id = 'legacy:m234:empty:' || id::text
WHERE request_id IS NULL
   OR btrim(request_id) = '';

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY tenant_id, api_key_id, request_id
               ORDER BY id
           ) AS duplicate_rank
    FROM moderation_violation_events
)
UPDATE moderation_violation_events v
SET request_id = 'legacy:m234:duplicate:' || v.id::text
FROM ranked r
WHERE r.id = v.id
  AND r.duplicate_rank > 1;

ALTER TABLE moderation_violation_events
    ALTER COLUMN request_id SET NOT NULL,
    DROP COLUMN payload_hash,
    ADD COLUMN ban_threshold_snapshot integer NOT NULL DEFAULT 0
        CHECK (ban_threshold_snapshot >= 0),
    ADD COLUMN ban_window_seconds_snapshot integer NOT NULL DEFAULT 0
        CHECK (ban_window_seconds_snapshot >= 0),
    ADD COLUMN violation_count bigint NOT NULL DEFAULT 0
        CHECK (violation_count >= 0),
    ADD COLUMN threshold_reached boolean NOT NULL DEFAULT false,
    ADD COLUMN auto_disable_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN disposition_source text NOT NULL DEFAULT 'none'
        CHECK (disposition_source IN ('none', 'auto', 'manual')),
    ADD COLUMN disposition_result text NOT NULL DEFAULT 'unchanged'
        CHECK (disposition_result IN ('unchanged', 'disabled', 'already_non_active'));

ALTER TABLE moderation_violation_events
    DROP CONSTRAINT IF EXISTS moderation_violation_events_decision_check;

ALTER TABLE moderation_violation_events
    ADD CONSTRAINT moderation_violation_events_decision_check CHECK (
        decision IN ('block_keyword', 'block_hash', 'block_external')
    );

CREATE UNIQUE INDEX uq_moderation_violation_request
    ON moderation_violation_events (tenant_id, api_key_id, request_id);

CREATE UNIQUE INDEX uq_moderation_violation_tenant_id
    ON moderation_violation_events (tenant_id, id);

ALTER TABLE moderation_violation_events
    ADD CONSTRAINT moderation_violation_api_key_fk
        FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT moderation_violation_user_fk
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id)
        ON DELETE RESTRICT;

ALTER TABLE moderation_log
    ADD CONSTRAINT moderation_log_violation_event_fk
        FOREIGN KEY (tenant_id, violation_event_id)
        REFERENCES moderation_violation_events(tenant_id, id)
        ON DELETE RESTRICT;

CREATE TABLE moderation_key_states (
    tenant_id          bigint      NOT NULL,
    api_key_id         bigint      NOT NULL,
    state              text        NOT NULL CHECK (state IN ('active', 'disabled')),
    source             text        NOT NULL CHECK (source IN ('auto', 'manual')),
    trigger_event_id   bigint      NOT NULL,
    reason_code        text        NOT NULL,
    actor_id           text        NOT NULL,
    actor_role         text        NOT NULL,
    disable_generation bigint      NOT NULL CHECK (disable_generation > 0),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, api_key_id),
    FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, trigger_event_id)
        REFERENCES moderation_violation_events (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_moderation_key_states_disabled
    ON moderation_key_states (tenant_id, updated_at DESC)
    WHERE state = 'disabled';

CREATE TABLE moderation_key_operations (
    id                  bigserial   PRIMARY KEY,
    tenant_id           bigint      NOT NULL,
    api_key_id          bigint      NOT NULL,
    idempotency_key     text        NOT NULL,
    request_fingerprint text        NOT NULL,
    action              text        NOT NULL
        CHECK (action IN ('disable', 'unban')),
    violation_event_id  bigint,
    actor_id            text        NOT NULL,
    actor_role          text        NOT NULL,
    result_status       text        NOT NULL
        CHECK (result_status IN ('active', 'disabled')),
    result_log_id       bigint      NOT NULL CHECK (result_log_id > 0),
    result_generation   bigint      NOT NULL CHECK (result_generation >= 0),
    result_updated_at   timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT moderation_key_operations_idempotency_key_check
        CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 256),
    CONSTRAINT moderation_key_operations_request_fingerprint_check
        CHECK (request_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT moderation_key_operations_action_event_check CHECK (
        (action = 'disable' AND violation_event_id IS NOT NULL)
        OR (action = 'unban' AND violation_event_id IS NULL)
    ),
    CONSTRAINT uq_moderation_key_operations_tenant_key
        UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT moderation_key_operations_api_key_fk
        FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT moderation_key_operations_violation_event_fk
        FOREIGN KEY (tenant_id, violation_event_id)
        REFERENCES moderation_violation_events (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX idx_moderation_key_operations_key_time
    ON moderation_key_operations (tenant_id, api_key_id, created_at DESC, id DESC);

COMMENT ON TABLE moderation_violation_events IS
    '内容审核永久违规事实；保存请求身份、策略快照、窗口结果和处置结果，不保存正文、摘录、凭据或普通载荷摘要。';

COMMENT ON TABLE moderation_log IS
    '内容审核运营日志；可保存管理员获准查看的脱敏截断用户文本摘录，按全局普通日志合同保留 30 天。';

COMMENT ON TABLE moderation_key_states IS
    '内容审核对 API Key 的当前可逆状态与状态代次；不是待审队列。';

COMMENT ON TABLE moderation_key_operations IS
    '人工禁用与解封的永久幂等事实；同一租户的幂等键只能对应一个规范化请求和稳定结果。';

COMMIT;
