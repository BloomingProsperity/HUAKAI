-- Hermes WAVE H3: tool-call audit ledger + tool-action audit whitelist.
--
-- Background: the admin-gated Hermes ops assistant gains a gateway-mediated
-- tool-execution spine. An operator (and later the assistant LLM) runs READ-ONLY
-- diagnostic tools through POST /v1/hermes/tool-execute. Every invocation —
-- success, error, OR RBAC denial — is recorded here so the operator-action
-- trail is complete and discriminating (a denied call is as auditable as an ok
-- one). This is distinct from hermes_audit_events (the per-action audit row, also
-- written by the handler) and from admin_audit_events (the operator ledger): this
-- table captures the tool-specific shape (tool_name, requested_args, structured
-- result_summary) that the generic audit rows do not.
--
-- PRIVACY: requested_args and result_summary are SANITIZED before insert — only
-- system-diagnostic enums / counts / ids / fingerprints, NEVER prompts,
-- completions, raw bodies, secrets, or PII. The application routes both JSONB
-- columns through the hermes sanitizer + privacy allowlist before this INSERT.
--
-- The tool_name CHECK is intentionally an explicit IN-list so the H4 mutating
-- tools extend it via the same DROP CONSTRAINT + ADD CONSTRAINT pattern the
-- hermes_audit_events action whitelist uses (0058/0059). This wave's six
-- read-only tools are the only allowed names.
--
-- Additive + reversible (.down drops the table and reverts the action whitelist).

BEGIN;

CREATE TABLE IF NOT EXISTS hermes_tool_calls (
    id                   BIGSERIAL PRIMARY KEY,
    tenant_id            BIGINT NOT NULL,
    -- actor_user_id is the tenant user whose ops context the operator acted within
    -- (mirrors hermes_audit_events.actor_user_id), so existing tenant isolation
    -- semantics carry over. Not FK-constrained here to keep the audit row durable
    -- even if the threaded user later changes; tenant scoping is enforced in-app.
    actor_user_id        BIGINT NOT NULL,
    -- admin_actor_token_id records WHICH operator token ran the tool. Nullable +
    -- ON DELETE SET NULL so revoking an operator token never deletes the audit row
    -- (mirrors 0144_hermes_admin_actor).
    admin_actor_token_id BIGINT REFERENCES admin_tokens(id) ON DELETE SET NULL,
    tool_name            TEXT NOT NULL,
    requested_args       JSONB,
    result_status        TEXT NOT NULL,
    result_summary       JSONB,
    error_class          TEXT,
    correlation_id       TEXT,
    request_id           TEXT,
    called_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    returned_at          TIMESTAMPTZ,
    CONSTRAINT hermes_tool_calls_tool_name_check
        CHECK (tool_name IN (
            'credential_diagnose',
            'account_health_diagnose',
            'request_diagnose',
            'dlq_inspect',
            'audit_lookup',
            'log_analyze')),
    CONSTRAINT hermes_tool_calls_result_status_check
        CHECK (result_status IN ('ok', 'error', 'denied'))
);

CREATE INDEX IF NOT EXISTS hermes_tool_calls_tenant_called_idx
    ON hermes_tool_calls (tenant_id, called_at DESC);

CREATE INDEX IF NOT EXISTS hermes_tool_calls_tenant_tool_called_idx
    ON hermes_tool_calls (tenant_id, tool_name, called_at DESC);

-- Extend the hermes_audit_events action whitelist so the tool-execute handler can
-- mirror each tool invocation into the existing hermes audit ledger as
-- hermes.tool.<name> (reusing hermes.Service.RecordAudit, which already folds in
-- the operator's admin_actor attribution). Mirrors the DROP+ADD CHECK shape of
-- 0058/0059; purely additive (existing values unchanged). Without this, the
-- RecordAudit insert with a hermes.tool.* action would violate the CHECK
-- constraint inside the same tool-execute request.
ALTER TABLE hermes_audit_events
    DROP CONSTRAINT IF EXISTS hermes_audit_events_action_check,
    ADD CONSTRAINT hermes_audit_events_action_check
        CHECK (action IN (
            'hermes.enable',
            'hermes.disable',
            'hermes.profile.create',
            'hermes.profile.rotate',
            'hermes.chat.start',
            'hermes.message.send',
            'hermes.conversation.delete',
            'hermes.tool.credential_diagnose',
            'hermes.tool.account_health_diagnose',
            'hermes.tool.request_diagnose',
            'hermes.tool.dlq_inspect',
            'hermes.tool.audit_lookup',
            'hermes.tool.log_analyze'
        ));

COMMIT;
