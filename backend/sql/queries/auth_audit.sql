-- name: InsertOAuthRefreshAuditEvent :exec
INSERT INTO oauth_refresh_audit_events (
    tenant_id,
    provider_account_id,
    outcome,
    storm_scope,
    old_token_fingerprint,
    new_token_fingerprint,
    mimicry_components_applied,
    mimicry_policy_version,
    request_id,
    client_protocol,
    model,
    error_class,
    error_message_redacted,
    occurred_at
) VALUES (
    sqlc.arg(tenant_id),
    sqlc.arg(provider_account_id),
    sqlc.arg(outcome),
    sqlc.narg(storm_scope),
    sqlc.narg(old_token_fingerprint),
    sqlc.narg(new_token_fingerprint),
    sqlc.narg(mimicry_components_applied),
    sqlc.narg(mimicry_policy_version),
    sqlc.narg(request_id),
    sqlc.narg(client_protocol),
    sqlc.narg(model),
    sqlc.narg(error_class),
    sqlc.narg(error_message_redacted),
    COALESCE(sqlc.narg(occurred_at), NOW())
);
