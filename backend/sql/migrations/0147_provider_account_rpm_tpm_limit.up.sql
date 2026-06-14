-- ROUTE-121: proactive per-account RPM/TPM budget for the rate pre-check gate.
-- Additive, NOT NULL DEFAULT 0; 0 means unlimited (opt-in), so every existing
-- account keeps its exact current behavior until an operator sets a budget.
-- Mirrors 0125 (window_cost_limit_cents).
ALTER TABLE provider_accounts ADD COLUMN IF NOT EXISTS rpm_limit bigint NOT NULL DEFAULT 0;
ALTER TABLE provider_accounts ADD COLUMN IF NOT EXISTS tpm_limit bigint NOT NULL DEFAULT 0;
