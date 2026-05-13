-- name: InsertAuditLedgerEntry :exec
INSERT INTO audit_ledger_entries (
    ledger_id,
    occurred_at,
    request_id,
    tenant_id,
    hop_chain,
    model_chain,
    prev_merkle_root,
    merkle_root,
    pubkey_fingerprint,
    signature
) VALUES (
    sqlc.arg(ledger_id),
    COALESCE(sqlc.narg(occurred_at), NOW()),
    sqlc.arg(request_id),
    sqlc.narg(tenant_id),
    sqlc.arg(hop_chain),
    sqlc.narg(model_chain),
    sqlc.arg(prev_merkle_root),
    sqlc.arg(merkle_root),
    sqlc.arg(pubkey_fingerprint),
    sqlc.arg(signature)
);

-- name: GetAuditLedgerEntryByRequestID :one
SELECT
    ledger_id,
    occurred_at,
    request_id,
    tenant_id,
    hop_chain,
    model_chain,
    prev_merkle_root,
    merkle_root,
    pubkey_fingerprint,
    signature
FROM audit_ledger_entries
WHERE request_id = sqlc.arg(request_id);

-- name: GetLatestAuditLedgerMerkleRoot :one
SELECT merkle_root
FROM audit_ledger_entries
ORDER BY id DESC
LIMIT 1;

-- name: CountAuditLedgerEntries :one
SELECT COUNT(*) FROM audit_ledger_entries;
