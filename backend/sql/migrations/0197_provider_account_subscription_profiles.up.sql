BEGIN;

ALTER TABLE account_credentials
    ADD CONSTRAINT account_credentials_tenant_id_id_key UNIQUE (tenant_id, id);

CREATE TABLE provider_account_subscription_observations (
    id                    bigserial PRIMARY KEY,
    tenant_id             bigint      NOT NULL,
    provider_account_id   bigint      NOT NULL,
    account_credential_id bigint      NOT NULL,
    credential_version    integer     NOT NULL CHECK (credential_version > 0),
    vendor                text        NOT NULL CHECK (vendor <> ''),
    normalized_plan       text        NOT NULL CHECK (normalized_plan <> ''),
    raw_plan              text,
    scope_kind            text        NOT NULL CHECK (scope_kind IN ('unknown', 'personal', 'workspace')),
    subject_ref           text,
    workspace_ref         text,
    source_type           text        NOT NULL CHECK (source_type <> ''),
    trust_level           text        NOT NULL CHECK (trust_level IN (
        'verified_api', 'issuer_response', 'verified_token',
        'unverified_token', 'imported', 'manual'
    )),
    verification_status   text        NOT NULL CHECK (verification_status IN (
        'verified', 'issuer_response', 'unverified', 'operator'
    )),
    observation_status    text        NOT NULL CHECK (observation_status IN (
        'observed', 'unknown_value', 'missing', 'stale', 'parse_failed', 'conflict'
    )),
    mapping_version       integer     NOT NULL CHECK (mapping_version > 0),
    error_class           text,
    observed_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT provider_account_subscription_observations_account_fkey
        FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id),
    CONSTRAINT provider_account_subscription_observations_credential_fkey
        FOREIGN KEY (tenant_id, account_credential_id)
        REFERENCES account_credentials (tenant_id, id),
    CONSTRAINT provider_account_subscription_observations_raw_check
        CHECK (observation_status <> 'observed' OR raw_plan IS NOT NULL),
    CONSTRAINT provider_account_subscription_observations_workspace_check
        CHECK (scope_kind <> 'personal' OR workspace_ref IS NULL)
);

ALTER TABLE provider_account_subscription_observations
    ADD CONSTRAINT provider_account_subscription_observations_tenant_id_id_key
    UNIQUE (tenant_id, id);

CREATE TABLE provider_account_subscription_states (
    tenant_id             bigint      NOT NULL,
    provider_account_id   bigint      NOT NULL,
    current_observation_id bigint     NOT NULL,
    vendor                text        NOT NULL CHECK (vendor <> ''),
    normalized_plan       text        NOT NULL CHECK (normalized_plan <> ''),
    raw_plan              text,
    scope_kind            text        NOT NULL CHECK (scope_kind IN ('unknown', 'personal', 'workspace')),
    subject_ref           text,
    workspace_ref         text,
    source_type           text        NOT NULL CHECK (source_type <> ''),
    trust_level           text        NOT NULL CHECK (trust_level IN (
        'verified_api', 'issuer_response', 'verified_token',
        'unverified_token', 'imported', 'manual'
    )),
    verification_status   text        NOT NULL CHECK (verification_status IN (
        'verified', 'issuer_response', 'unverified', 'operator'
    )),
    state_status          text        NOT NULL CHECK (state_status IN (
        'observed', 'unknown_value', 'missing', 'stale', 'parse_failed', 'conflict'
    )),
    mapping_version       integer     NOT NULL CHECK (mapping_version > 0),
    error_class           text,
    first_observed_at     timestamptz NOT NULL,
    observed_at           timestamptz NOT NULL,
    changed_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider_account_id),
    CONSTRAINT provider_account_subscription_states_account_fkey
        FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id),
    CONSTRAINT provider_account_subscription_states_observation_fkey
        FOREIGN KEY (tenant_id, current_observation_id)
        REFERENCES provider_account_subscription_observations (tenant_id, id),
    CONSTRAINT provider_account_subscription_states_workspace_check
        CHECK (scope_kind <> 'personal' OR workspace_ref IS NULL)
);

CREATE INDEX idx_provider_account_subscription_filter
    ON provider_account_subscription_states (
        tenant_id, vendor, normalized_plan, scope_kind, state_status, provider_account_id
    );

CREATE INDEX idx_provider_account_subscription_source
    ON provider_account_subscription_states (
        tenant_id, source_type, observed_at DESC, provider_account_id
    );

CREATE INDEX idx_provider_account_subscription_history
    ON provider_account_subscription_observations (
        tenant_id, provider_account_id, observed_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION reject_provider_account_subscription_observation_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'provider account subscription observations are append-only';
END;
$$;

CREATE TRIGGER provider_account_subscription_observations_append_only
BEFORE UPDATE OR DELETE ON provider_account_subscription_observations
FOR EACH ROW EXECUTE FUNCTION reject_provider_account_subscription_observation_mutation();

COMMENT ON TABLE provider_account_subscription_observations IS
    '账号套餐的只增观测历史；不保存访问令牌、Cookie 或其他秘密。';
COMMENT ON TABLE provider_account_subscription_states IS
    '账号套餐当前投影；系统标签由 vendor 与 normalized_plan 派生，不能写入人工 tags。';

COMMIT;
