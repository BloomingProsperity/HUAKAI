-- Hermes admin-actor attribution (WAVE H1 re-gate to admin/operator-only).
--
-- Background: under the admin-only repositioning, Hermes is reached by an
-- operator authenticated against admin_tokens (NOT users). The existing
-- composite FK (tenant_id, owner_user_id/actor_user_id) -> users(tenant_id, id)
-- on hermes_conversations / hermes_audit_events / hermes_api_profiles /
-- hermes_settings stays UNCHANGED: the operator still acts within an explicit
-- tenant user's ops context (so those FKs keep pointing at a real users row and
-- tenant isolation is preserved). This migration only ADDS a nullable
-- admin_actor_token_id column so a row can additionally record WHICH operator
-- token performed the action. End-user-path rows leave it NULL.
--
-- Purely additive + nullable + reversible. No CHECK constraints are altered and
-- no existing column is touched, so prior sqlc-generated INSERTs that do not
-- mention the new column continue to work (the column defaults to NULL). The
-- admin attribution is also mirrored into hermes_audit_events.sanitized_args
-- JSONB by the application until a future slice migrates it to this structured
-- column, so no sqlc regeneration is forced by this wave.

BEGIN;

ALTER TABLE hermes_audit_events
    ADD COLUMN IF NOT EXISTS admin_actor_token_id BIGINT
        REFERENCES admin_tokens(id) ON DELETE SET NULL;

ALTER TABLE hermes_conversations
    ADD COLUMN IF NOT EXISTS admin_actor_token_id BIGINT
        REFERENCES admin_tokens(id) ON DELETE SET NULL;

COMMIT;
