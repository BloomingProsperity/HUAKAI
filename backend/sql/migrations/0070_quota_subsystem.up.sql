-- HUAKAI 配额子系统 Slice A schema。
-- 本 migration 只建立配额基础账本, 不扩展 billing_ledger_claims。
-- 所有 quota 表都以 tenant_id 作为第一列, money 使用 numeric(20,8)。

BEGIN;

-- ----------------------------------------------------------------------------
-- 表: quota_policies
-- ----------------------------------------------------------------------------
-- 配额策略定义: scope + metric + window + mode 的租户内策略面。
-- scope_id 使用 HUAKAI 中性编码: global 用 '*', 其他 scope 写本地 id 字符串。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_policies (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    id                          bigserial   PRIMARY KEY,
    scope_kind                  text        NOT NULL CHECK (scope_kind IN (
                                    'global', 'user', 'api_key', 'channel',
                                    'pool_group', 'provider_account'
                                )),
    scope_id                    text        NOT NULL DEFAULT '*',
    metric                      text        NOT NULL CHECK (metric IN (
                                    'requests', 'tokens_estimated',
                                    'cost_usd', 'concurrency'
                                )),
    window_kind                 text        NOT NULL DEFAULT 'fixed'
                                CHECK (window_kind IN (
                                    'none', 'fixed', 'calendar_day',
                                    'calendar_week', 'manual'
                                )),
    window_seconds              integer     NOT NULL DEFAULT 0 CHECK (window_seconds >= 0),
    limit_value                 numeric(20,8) NOT NULL CHECK (limit_value >= 0),
    burst_value                 numeric(20,8) NOT NULL DEFAULT 0 CHECK (burst_value >= 0),
    mode                        text        NOT NULL DEFAULT 'enforce'
                                CHECK (mode IN (
                                    'enforce', 'observe', 'manual_first', 'disabled'
                                )),
    priority                    integer     NOT NULL DEFAULT 100,
    enabled                     boolean     NOT NULL DEFAULT true,
    valid_from                  timestamptz NOT NULL DEFAULT now(),
    valid_until                 timestamptz,
    created_by_actor            text,
    last_modified_by_actor      text,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quota_policies_valid_range
        CHECK (valid_until IS NULL OR valid_until > valid_from),
    CONSTRAINT uq_quota_policies_tenant_id_id UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_quota_policies_live_scope_metric
    ON quota_policies (
        tenant_id, scope_kind, scope_id, metric,
        window_kind, window_seconds, priority
    )
    WHERE enabled = true AND valid_until IS NULL;

CREATE INDEX IF NOT EXISTS idx_quota_policies_tenant_scope_metric
    ON quota_policies (
        tenant_id, scope_kind, scope_id, metric,
        enabled, priority
    );

CREATE INDEX IF NOT EXISTS idx_quota_policies_tenant_validity
    ON quota_policies (tenant_id, enabled, valid_from, valid_until);

COMMENT ON TABLE quota_policies IS 'HUAKAI 配额策略面。tenant-first, 与 billing core 独立。';
COMMENT ON COLUMN quota_policies.scope_id IS 'HUAKAI 中性 scope 编码: global 为 *, 其他 scope 使用本地 id 字符串。';

-- ----------------------------------------------------------------------------
-- 表: quota_windows
-- ----------------------------------------------------------------------------
-- 每个 policy/window_start 一行, reserve 阶段统计 reserved + settled。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_windows (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    id                          bigserial   PRIMARY KEY,
    policy_id                   bigint      NOT NULL,
    window_start                timestamptz NOT NULL,
    window_end                  timestamptz NOT NULL,
    reserved_value              numeric(20,8) NOT NULL DEFAULT 0 CHECK (reserved_value >= 0),
    settled_value               numeric(20,8) NOT NULL DEFAULT 0 CHECK (settled_value >= 0),
    overage_value               numeric(20,8) NOT NULL DEFAULT 0 CHECK (overage_value >= 0),
    request_count               bigint      NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    version                     integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quota_windows_time_range CHECK (window_end > window_start),
    CONSTRAINT uq_quota_windows_policy_start
        UNIQUE (tenant_id, policy_id, window_start),
    CONSTRAINT uq_quota_windows_tenant_id_id UNIQUE (tenant_id, id),
    CONSTRAINT fk_quota_windows_policy
        FOREIGN KEY (tenant_id, policy_id)
        REFERENCES quota_policies (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_quota_windows_tenant_policy_end
    ON quota_windows (tenant_id, policy_id, window_end);

CREATE INDEX IF NOT EXISTS idx_quota_windows_tenant_open
    ON quota_windows (tenant_id, window_start, window_end);

COMMENT ON TABLE quota_windows IS '配额窗口计数。Reserve 判定同时计算 reserved_value + settled_value。';

-- ----------------------------------------------------------------------------
-- 表: quota_reservations
-- ----------------------------------------------------------------------------
-- claim_id + tenant_id 是配额预留幂等键; 与 billing claim 分账本保存。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_reservations (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    id                          bigserial   PRIMARY KEY,
    claim_id                    bigint      NOT NULL,
    request_fingerprint         text        NOT NULL,
    scope_snapshot              jsonb       NOT NULL DEFAULT '[]'::jsonb,
    policy_snapshot             jsonb       NOT NULL DEFAULT '[]'::jsonb,
    predicted_cost              numeric(20,8) NOT NULL DEFAULT 0 CHECK (predicted_cost >= 0),
    reserved_units              numeric(20,8) NOT NULL DEFAULT 0 CHECK (reserved_units >= 0),
    settled_cost                numeric(20,8) NOT NULL DEFAULT 0 CHECK (settled_cost >= 0),
    settled_units               numeric(20,8) NOT NULL DEFAULT 0 CHECK (settled_units >= 0),
    overage_units               numeric(20,8) NOT NULL DEFAULT 0 CHECK (overage_units >= 0),
    status                      text        NOT NULL DEFAULT 'reserved'
                                CHECK (status IN (
                                    'reserved', 'settled', 'released',
                                    'expired', 'reconciliation_needed'
                                )),
    lease_expires_at            timestamptz NOT NULL,
    settled_at                  timestamptz,
    released_at                 timestamptz,
    release_reason              text,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_quota_reservations_tenant_claim UNIQUE (tenant_id, claim_id),
    CONSTRAINT uq_quota_reservations_tenant_id_id UNIQUE (tenant_id, id),
    CONSTRAINT fk_quota_reservations_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_quota_reservations_tenant_status_lease
    ON quota_reservations (tenant_id, status, lease_expires_at)
    WHERE status = 'reserved';

CREATE INDEX IF NOT EXISTS idx_quota_reservations_tenant_created
    ON quota_reservations (tenant_id, created_at DESC);

COMMENT ON TABLE quota_reservations IS '配额预留账本。按 (tenant_id, claim_id) 幂等。';

-- ----------------------------------------------------------------------------
-- 表: quota_concurrency_scope_locks
-- ----------------------------------------------------------------------------
-- 每个 tenant/scope 一行, 只作为本地 quota 并发槽的行锁串行化点。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_concurrency_scope_locks (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    scope_kind                  text        NOT NULL CHECK (scope_kind IN (
                                    'global', 'user', 'api_key', 'channel', 'pool_group'
                                )),
    scope_id                    text        NOT NULL DEFAULT '*',
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_quota_concurrency_scope_locks_tenant_scope
        UNIQUE (tenant_id, scope_kind, scope_id)
);

COMMENT ON TABLE quota_concurrency_scope_locks IS 'quota 本地并发槽的 tenant/scope 行锁表, 防 READ COMMITTED 下 COUNT+INSERT 竞态。';

-- ----------------------------------------------------------------------------
-- 表: quota_concurrency_slots
-- ----------------------------------------------------------------------------
-- global/user/api_key/channel 并发槽; provider-account in-flight 仍由 pool 管。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_concurrency_slots (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    id                          bigserial   PRIMARY KEY,
    reservation_id              bigint      NOT NULL,
    claim_id                    bigint      NOT NULL,
    scope_kind                  text        NOT NULL CHECK (scope_kind IN (
                                    'global', 'user', 'api_key', 'channel', 'pool_group'
                                )),
    scope_id                    text        NOT NULL DEFAULT '*',
    lease_expires_at            timestamptz NOT NULL,
    released_at                 timestamptz,
    release_reason              text,
    status                      text        NOT NULL DEFAULT 'acquired'
                                CHECK (status IN ('acquired', 'released', 'expired')),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_quota_slots_tenant_reservation_scope
        UNIQUE (tenant_id, reservation_id, scope_kind, scope_id),
    CONSTRAINT uq_quota_slots_tenant_id_id UNIQUE (tenant_id, id),
    CONSTRAINT fk_quota_slots_reservation
        FOREIGN KEY (tenant_id, reservation_id)
        REFERENCES quota_reservations (tenant_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT fk_quota_slots_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_quota_slots_tenant_active_scope
    ON quota_concurrency_slots (tenant_id, scope_kind, scope_id, lease_expires_at)
    WHERE status = 'acquired';

CREATE INDEX IF NOT EXISTS idx_quota_slots_tenant_reservation_status
    ON quota_concurrency_slots (tenant_id, reservation_id, status);

COMMENT ON TABLE quota_concurrency_slots IS 'quota 自有本地 scope 并发槽; provider-account 槽仍由 pool 维护。';

-- quota_acquire_concurrency_slot 在单个 DB 函数内完成:
-- 1. 校验 reservation 属于同一 tenant/claim;
-- 2. 获取 tenant/scope 锁行;
-- 3. 清理该 scope 已过 lease 的槽;
-- 4. 在锁内重新 COUNT 并 UPSERT 本次 reservation 槽。
-- 这样不依赖调用方 SERIALIZABLE, READ COMMITTED 下也不会超过 slot_limit。
CREATE OR REPLACE FUNCTION quota_acquire_concurrency_slot(
    p_tenant_id bigint,
    p_reservation_id bigint,
    p_claim_id bigint,
    p_scope_kind text,
    p_scope_id text,
    p_at_time timestamptz,
    p_lease_expires_at timestamptz,
    p_slot_limit bigint
) RETURNS TABLE (
    tenant_id bigint,
    id bigint,
    reservation_id bigint,
    scope_kind text,
    scope_id text,
    status text,
    lease_expires_at timestamptz
) AS $$
DECLARE
    active_count bigint;
BEGIN
    IF p_slot_limit IS NULL OR p_slot_limit <= 0 THEN
        RETURN;
    END IF;

    IF p_lease_expires_at IS NULL OR p_lease_expires_at <= p_at_time THEN
        RETURN;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM quota_reservations qr
        WHERE qr.tenant_id = p_tenant_id
          AND qr.id = p_reservation_id
          AND qr.claim_id = p_claim_id
    ) THEN
        RETURN;
    END IF;

    INSERT INTO quota_concurrency_scope_locks (
        tenant_id, scope_kind, scope_id
    )
    VALUES (
        p_tenant_id, p_scope_kind, p_scope_id
    )
    ON CONFLICT ON CONSTRAINT uq_quota_concurrency_scope_locks_tenant_scope
    DO UPDATE SET
        updated_at = quota_concurrency_scope_locks.updated_at;

    PERFORM 1
    FROM quota_concurrency_scope_locks qcsl
    WHERE qcsl.tenant_id = p_tenant_id
      AND qcsl.scope_kind = p_scope_kind
      AND qcsl.scope_id = p_scope_id
    FOR UPDATE;

    IF NOT FOUND THEN
        RETURN;
    END IF;

    UPDATE quota_concurrency_slots qcs
    SET status = 'expired',
        released_at = NOW(),
        release_reason = 'lease_expired',
        updated_at = NOW()
    WHERE qcs.tenant_id = p_tenant_id
      AND qcs.scope_kind = p_scope_kind
      AND qcs.scope_id = p_scope_id
      AND qcs.status = 'acquired'
      AND qcs.lease_expires_at <= p_at_time;

    RETURN QUERY
    UPDATE quota_concurrency_slots qcs
    SET lease_expires_at = p_lease_expires_at,
        released_at = NULL,
        release_reason = NULL,
        updated_at = NOW()
    WHERE qcs.tenant_id = p_tenant_id
      AND qcs.reservation_id = p_reservation_id
      AND qcs.scope_kind = p_scope_kind
      AND qcs.scope_id = p_scope_id
      AND qcs.status = 'acquired'
      AND qcs.lease_expires_at > p_at_time
    RETURNING
        qcs.tenant_id,
        qcs.id,
        qcs.reservation_id,
        qcs.scope_kind,
        qcs.scope_id,
        qcs.status,
        qcs.lease_expires_at;

    IF FOUND THEN
        RETURN;
    END IF;

    SELECT COUNT(*)::bigint
    INTO active_count
    FROM quota_concurrency_slots qcs
    WHERE qcs.tenant_id = p_tenant_id
      AND qcs.scope_kind = p_scope_kind
      AND qcs.scope_id = p_scope_id
      AND qcs.status = 'acquired'
      AND qcs.lease_expires_at > p_at_time;

    IF active_count >= p_slot_limit THEN
        RETURN;
    END IF;

    RETURN QUERY
    INSERT INTO quota_concurrency_slots (
        tenant_id,
        reservation_id,
        claim_id,
        scope_kind,
        scope_id,
        lease_expires_at
    )
    VALUES (
        p_tenant_id,
        p_reservation_id,
        p_claim_id,
        p_scope_kind,
        p_scope_id,
        p_lease_expires_at
    )
    ON CONFLICT ON CONSTRAINT uq_quota_slots_tenant_reservation_scope
    DO UPDATE SET
        lease_expires_at = EXCLUDED.lease_expires_at,
        released_at = NULL,
        release_reason = NULL,
        status = 'acquired',
        updated_at = NOW()
    WHERE quota_concurrency_slots.tenant_id = p_tenant_id
      AND (
          quota_concurrency_slots.status <> 'acquired'
          OR quota_concurrency_slots.lease_expires_at <= p_at_time
      )
    RETURNING
        quota_concurrency_slots.tenant_id,
        quota_concurrency_slots.id,
        quota_concurrency_slots.reservation_id,
        quota_concurrency_slots.scope_kind,
        quota_concurrency_slots.scope_id,
        quota_concurrency_slots.status,
        quota_concurrency_slots.lease_expires_at;
END;
$$ LANGUAGE plpgsql;

-- ----------------------------------------------------------------------------
-- 表: quota_audit_events
-- ----------------------------------------------------------------------------
-- 配额 allow/deny/settle/release/overage/reconcile 的审计流水。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_audit_events (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    id                          bigserial   PRIMARY KEY,
    reservation_id              bigint,
    claim_id                    bigint,
    event_type                  text        NOT NULL CHECK (event_type IN (
                                    'reserve_allowed', 'reserve_denied',
                                    'observe_exceeded', 'settled',
                                    'released', 'overage_recorded',
                                    'reconciliation_enqueued',
                                    'reconciliation_completed'
                                )),
    decision_code               text        NOT NULL,
    scope_kind                  text        NOT NULL DEFAULT 'global',
    scope_id                    text        NOT NULL DEFAULT '*',
    metric                      text        NOT NULL DEFAULT 'requests'
                                CHECK (metric IN (
                                    'requests', 'tokens_estimated',
                                    'cost_usd', 'concurrency'
                                )),
    amount_reserved             numeric(20,8) NOT NULL DEFAULT 0,
    amount_settled              numeric(20,8) NOT NULL DEFAULT 0,
    retry_after_seconds         integer,
    payload                     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    actor                       text,
    occurred_at                 timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_quota_audit_reservation
        FOREIGN KEY (tenant_id, reservation_id)
        REFERENCES quota_reservations (tenant_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT fk_quota_audit_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_quota_audit_tenant_time
    ON quota_audit_events (tenant_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_quota_audit_tenant_decision_time
    ON quota_audit_events (tenant_id, decision_code, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_quota_audit_tenant_claim_time
    ON quota_audit_events (tenant_id, claim_id, occurred_at DESC)
    WHERE claim_id IS NOT NULL;

COMMENT ON TABLE quota_audit_events IS '租户内配额审计流; 本地 quota deny 不写 provider cooldown。';

-- ----------------------------------------------------------------------------
-- 表: quota_reconciliation_jobs
-- ----------------------------------------------------------------------------
-- billing 成功但 quota settle/release 失败时的补偿队列。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS quota_reconciliation_jobs (
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    id                          bigserial   PRIMARY KEY,
    claim_id                    bigint      NOT NULL,
    reservation_id              bigint,
    job_kind                    text        NOT NULL CHECK (job_kind IN (
                                    'settle_after_billing_success',
                                    'release_after_abort',
                                    'release_after_cache_hit',
                                    'expire_leased_reservation'
                                )),
    status                      text        NOT NULL DEFAULT 'queued'
                                CHECK (status IN (
                                    'queued', 'running', 'succeeded',
                                    'failed', 'cancelled'
                                )),
    attempt_count               integer     NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error                  text,
    next_run_at                 timestamptz NOT NULL DEFAULT now(),
    locked_at                   timestamptz,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_quota_reconciliation_jobs_tenant_id_id UNIQUE (tenant_id, id),
    CONSTRAINT fk_quota_reconciliation_claim
        FOREIGN KEY (tenant_id, claim_id)
        REFERENCES billing_ledger_claims (tenant_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT fk_quota_reconciliation_reservation
        FOREIGN KEY (tenant_id, reservation_id)
        REFERENCES quota_reservations (tenant_id, id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_quota_reconciliation_active_claim_kind
    ON quota_reconciliation_jobs (tenant_id, claim_id, job_kind)
    WHERE status IN ('queued', 'running');

CREATE INDEX IF NOT EXISTS idx_quota_reconciliation_tenant_due
    ON quota_reconciliation_jobs (tenant_id, status, next_run_at, id)
    WHERE status IN ('queued', 'failed');

CREATE INDEX IF NOT EXISTS idx_quota_reconciliation_tenant_stale_running
    ON quota_reconciliation_jobs (tenant_id, locked_at, id)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_quota_reconciliation_tenant_reservation
    ON quota_reconciliation_jobs (tenant_id, reservation_id)
    WHERE reservation_id IS NOT NULL;

COMMENT ON TABLE quota_reconciliation_jobs IS 'B1 wrapper + 独立事务 reconciliation 的 quota 补偿队列。';

COMMIT;
