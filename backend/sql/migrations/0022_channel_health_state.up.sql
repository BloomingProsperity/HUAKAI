-- 0022_channel_health_state.up.sql
--
-- F-CH-002 channel health auto-disable.
-- Additive schema only: tenant-scoped channel health state, safe audit events,
-- and operator alerts. No credential bytes, prompt bodies, billing ledger,
-- quota, or auth-core tables are modified.

BEGIN;

CREATE TABLE IF NOT EXISTS channel_health_state (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    channel_id                  text        NOT NULL,
    vendor                      text        NOT NULL,
    provider_account_id         bigint      REFERENCES provider_accounts(id),
    account_credential_id       bigint      NOT NULL REFERENCES account_credentials(id),
    credential_version          integer     NOT NULL CHECK (credential_version > 0),
    state                       text        NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'degraded', 'cooling_down', 'ramping', 'disabled', 'manual_paused')),
    score                       numeric(6,2) NOT NULL DEFAULT 100.00,
    reason_class                text        NOT NULL DEFAULT 'none',
    confidence_tier             text        NOT NULL DEFAULT 'observed'
        CHECK (confidence_tier IN ('observed', 'inferred', 'operator_override')),
    cooldown_until              timestamptz,
    ramp_stage_pct              integer
        CHECK (ramp_stage_pct IS NULL OR ramp_stage_pct IN (1, 10, 50, 100)),
    ramp_started_at             timestamptz,
    state_entered_at            timestamptz NOT NULL DEFAULT now(),
    last_transition_at          timestamptz NOT NULL DEFAULT now(),
    policy_version              text        NOT NULL,
    sample_window               jsonb       NOT NULL DEFAULT '{}'::jsonb,
    last_signal_class           text        NOT NULL DEFAULT 'none',
    last_signal_at              timestamptz,
    manual_pause_reason         text,
    manual_override_actor_id    text,
    manual_override_reason      text,
    ramp_failure_count          integer     NOT NULL DEFAULT 0 CHECK (ramp_failure_count >= 0),
    recovery_blocked_reason     text,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_channel_health_subject
    ON channel_health_state (tenant_id, vendor, account_credential_id, credential_version);
CREATE UNIQUE INDEX IF NOT EXISTS uq_channel_health_credential_version
    ON channel_health_state (account_credential_id, credential_version);
CREATE INDEX IF NOT EXISTS idx_channel_health_provider_account
    ON channel_health_state (tenant_id, provider_account_id, credential_version DESC, updated_at DESC)
    WHERE provider_account_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_health_state_until
    ON channel_health_state (tenant_id, state, cooldown_until)
    WHERE state IN ('cooling_down', 'disabled', 'ramping');

COMMENT ON TABLE channel_health_state IS
    'F-CH-002: tenant-scoped channel health state for (vendor, account credential, credential version). No raw upstream body or credential material.';
COMMENT ON COLUMN channel_health_state.sample_window IS
    'Safe rolling aggregate/window metadata: counts, status classes, latency values, and reason classes only.';

CREATE TABLE IF NOT EXISTS channel_health_audit_events (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    event_type                  text        NOT NULL CHECK (event_type IN (
        'channel_health_degraded',
        'channel_disabled',
        'channel_recovered',
        'channel_ramp_started',
        'channel_ramp_rolled_back',
        'channel_manual_override'
    )),
    channel_id                  text        NOT NULL,
    vendor                      text        NOT NULL,
    provider_account_id         bigint,
    account_credential_id       bigint      NOT NULL,
    credential_version          integer     NOT NULL,
    previous_state              text,
    new_state                   text        NOT NULL,
    reason_class                text        NOT NULL,
    policy_version              text        NOT NULL,
    request_id                  text,
    actor_id                    text,
    payload                     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at                 timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_channel_health_audit_subject_time
    ON channel_health_audit_events (tenant_id, account_credential_id, credential_version, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_channel_health_audit_event_time
    ON channel_health_audit_events (tenant_id, event_type, occurred_at DESC);

COMMENT ON TABLE channel_health_audit_events IS
    'F-TRUST/F-CH-002 operator audit. Payload allowlist excludes raw upstream text, prompts, cookies, tokens, and credential bytes.';

CREATE TABLE IF NOT EXISTS channel_health_admin_alerts (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    channel_id                  text        NOT NULL,
    provider_account_id         bigint,
    account_credential_id       bigint      NOT NULL,
    credential_version          integer     NOT NULL,
    alert_type                  text        NOT NULL CHECK (alert_type IN (
        'ban_signal',
        'repeated_ramp_rollback',
        'manual_force_active',
        'no_healthy_alternate'
    )),
    severity                    text        NOT NULL DEFAULT 'high'
        CHECK (severity IN ('low', 'medium', 'high', 'security')),
    reason_class                text        NOT NULL,
    payload                     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status                      text        NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'acknowledged', 'resolved')),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    acknowledged_at             timestamptz,
    resolved_at                 timestamptz
);

CREATE INDEX IF NOT EXISTS idx_channel_health_alerts_open
    ON channel_health_admin_alerts (tenant_id, status, created_at DESC)
    WHERE status = 'open';

COMMENT ON TABLE channel_health_admin_alerts IS
    'F-CH-002 admin alert sink for ban signals, repeated ramp rollback, manual force-active, and no-healthy-alternate cases.';

COMMIT;
