-- AUTH-194 user self-service API key audit log.
--
-- Additive append-only table for user-visible key issue/revoke history.
-- Payload is deliberately columnar and redacted: key_prefix is allowed;
-- plaintext bearer and key_hash are never stored.

BEGIN;

CREATE TABLE IF NOT EXISTS user_audit_events (
    id           bigserial   PRIMARY KEY,
    tenant_id    bigint      NOT NULL REFERENCES tenants(id),
    user_id      bigint      NOT NULL,
    action       text        NOT NULL CHECK (action IN ('issue_api_key', 'revoke_api_key')),
    outcome      text        NOT NULL CHECK (outcome IN ('committed', 'denied', 'error')),
    api_key_id   bigint,
    key_prefix   text,
    reason       text,
    request_id   text,
    occurred_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_audit_events_user_fk
        FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE INDEX idx_user_audit_events_tenant_user_time
    ON user_audit_events (tenant_id, user_id, occurred_at DESC);

COMMENT ON TABLE user_audit_events IS
    'AUTH-194 append-only user self-service API key audit events. Contains key_prefix only; never plaintext bearer or key_hash.';
COMMENT ON COLUMN user_audit_events.key_prefix IS
    'Non-secret API key prefix used for user-facing audit correlation. Full plaintext bearer and key_hash MUST NOT be stored here.';

COMMIT;
