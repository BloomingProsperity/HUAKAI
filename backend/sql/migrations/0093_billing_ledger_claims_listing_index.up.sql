BEGIN;

-- listBillingClaims (internal/db/billing/observability.sql.go) filters by
-- tenant_id and paginates with keyset (reserved_at, id) ORDER BY
-- reserved_at DESC, id DESC. Existing indexes on billing_ledger_claims only
-- cover (tenant_id, id) / (tenant_id, api_key_id, idempotency_key), so this
-- listing falls back to a sort over the tenant partition. Add a composite
-- index matching the ORDER BY direction so both the filter and the keyset
-- seek are index-served.
CREATE INDEX IF NOT EXISTS idx_billing_ledger_claims_tenant_reserved_at_id
    ON billing_ledger_claims (tenant_id, reserved_at DESC, id DESC);

COMMIT;
