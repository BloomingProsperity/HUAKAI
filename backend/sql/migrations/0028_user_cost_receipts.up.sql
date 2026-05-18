BEGIN;

CREATE TABLE IF NOT EXISTS user_cost_receipts (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    request_id TEXT NOT NULL UNIQUE,
    model TEXT NOT NULL,
    input_tokens BIGINT NOT NULL CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL CHECK (output_tokens >= 0),
    cached_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cached_tokens >= 0),
    cost_usd_micros BIGINT NOT NULL CHECK (cost_usd_micros >= 0),
    rate_table_snapshot_id BIGINT NOT NULL,
    signer_fingerprint BYTEA NOT NULL,
    signed_hash BYTEA NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_cost_receipts_tenant_request
    ON user_cost_receipts(tenant_id, request_id);

CREATE INDEX IF NOT EXISTS idx_user_cost_receipts_created
    ON user_cost_receipts(created_at);

CREATE OR REPLACE FUNCTION enforce_audit_append_only() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'audit snapshot table is append-only: %', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS enforce_user_cost_receipts_append_only_update ON user_cost_receipts;
CREATE TRIGGER enforce_user_cost_receipts_append_only_update
    BEFORE UPDATE ON user_cost_receipts
    FOR EACH ROW
    EXECUTE FUNCTION enforce_audit_append_only();

DROP TRIGGER IF EXISTS enforce_user_cost_receipts_append_only_delete ON user_cost_receipts;
CREATE TRIGGER enforce_user_cost_receipts_append_only_delete
    BEFORE DELETE ON user_cost_receipts
    FOR EACH ROW
    EXECUTE FUNCTION enforce_audit_append_only();

COMMIT;
