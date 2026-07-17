BEGIN;

CREATE TABLE tenant_capability_grants (
    tenant_id         bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    capability        text NOT NULL CHECK (capability IN (
        'account_intake.claude_cookie',
        'account_intake.claude_setup_token',
        'account_intake.codex_agent_identity',
        'account_sync.crs',
        'account_bundle.structure',
        'account_bundle.secrets'
    )),
    status            text NOT NULL CHECK (status IN ('granted', 'revoked')),
    expires_at        timestamptz,
    revision          bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    granted_by_actor  text,
    revoked_by_actor  text,
    reason            text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, capability),
    CONSTRAINT tenant_capability_grant_actor_state CHECK (
        (status = 'granted' AND granted_by_actor IS NOT NULL AND revoked_by_actor IS NULL)
        OR
        (status = 'revoked' AND revoked_by_actor IS NOT NULL)
    )
);

CREATE INDEX tenant_capability_grants_active_expiry_idx
    ON tenant_capability_grants (expires_at, tenant_id, capability)
    WHERE status = 'granted' AND expires_at IS NOT NULL;

CREATE TABLE tenant_capability_grant_events (
    id             bigserial PRIMARY KEY,
    tenant_id      bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    capability     text NOT NULL,
    action         text NOT NULL CHECK (action IN ('grant', 'revoke')),
    revision       bigint NOT NULL CHECK (revision > 0),
    actor_id       text NOT NULL,
    reason         text NOT NULL,
    expires_at     timestamptz,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, capability, revision)
);

CREATE INDEX tenant_capability_grant_events_tenant_idx
    ON tenant_capability_grant_events (tenant_id, occurred_at DESC, id DESC);

COMMENT ON TABLE tenant_capability_grants IS
    '部署治理主体向单层租户授予的高敏账号能力；没有有效 granted 行时固定拒绝。';
COMMENT ON TABLE tenant_capability_grant_events IS
    '租户能力授权的追加事件，和当前授权状态在同一事务写入。';

COMMIT;
