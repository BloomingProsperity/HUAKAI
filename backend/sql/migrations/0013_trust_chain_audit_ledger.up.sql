-- HUAKAI 信任链 T4.x：append-only audit ledger 表 + Merkle 链发布表。
--
-- 设计：
--   - audit_ledger_entries 是 append-only，每条记录含 hop_chain（jsonb）+
--     model_chain（jsonb）+ prev_merkle_root（bytea）+ merkle_root（bytea）+
--     pubkey_fingerprint + signature。
--   - 写入只走 ledger writer goroutine（无并发 INSERT，避免链断裂）。
--   - 不允许 UPDATE / DELETE；任何修改通过 INSERT 一条"corrective" entry，
--     原行保留（Sigstore / Trillian 风格 append-only）。
--   - request_id UNIQUE — 一个 request 一条 ledger entry。
--
-- 与现有 admin_audit_events / pool_routing_audit_events / oauth_refresh_
-- audit_events 的区别：
--   - 那些表是 operator-facing audit（管理员查谁干了啥）；
--   - 本表是 user-facing ledger（end user 验证 HUAKAI 没偷换/掺水）。

BEGIN;

CREATE TABLE IF NOT EXISTS audit_ledger_entries (
    id                  bigserial   PRIMARY KEY,
    ledger_id           text        NOT NULL UNIQUE,
    occurred_at         timestamptz NOT NULL DEFAULT now(),
    request_id          text        NOT NULL UNIQUE,
    tenant_id           bigint      REFERENCES tenants(id),
    hop_chain           jsonb       NOT NULL DEFAULT '[]'::jsonb,
    model_chain         jsonb,
    prev_merkle_root    bytea       NOT NULL CHECK (octet_length(prev_merkle_root) = 32),
    merkle_root         bytea       NOT NULL CHECK (octet_length(merkle_root) = 32),
    pubkey_fingerprint  text        NOT NULL CHECK (length(pubkey_fingerprint) = 16),
    signature           text        NOT NULL CHECK (length(signature) > 0)
);

-- request_id → entry 唯一映射（user 用 request_id 查自己的 entry）
CREATE INDEX idx_audit_ledger_entries_request_id ON audit_ledger_entries (request_id);

-- 按 tenant + 时间倒序查（dashboard 用）
CREATE INDEX idx_audit_ledger_entries_tenant_time ON audit_ledger_entries (tenant_id, occurred_at DESC);

-- 按 occurred_at 倒序（chain head 查询）
CREATE INDEX idx_audit_ledger_entries_occurred_at ON audit_ledger_entries (occurred_at DESC, id DESC);

COMMENT ON TABLE audit_ledger_entries IS
    'HUAKAI trust-chain T4: append-only signed ledger; each row = one user-verifiable request attestation. NEVER UPDATE/DELETE.';
COMMENT ON COLUMN audit_ledger_entries.hop_chain IS
    'JSON array of HopAttestation; 6 hops typical. NEVER contains user prompt/completion (redact allowlist enforced).';
COMMENT ON COLUMN audit_ledger_entries.model_chain IS
    'JSON object: {requested, route_decided, upstream_reported}. NULL when streaming-in-flight or not enabled.';
COMMENT ON COLUMN audit_ledger_entries.prev_merkle_root IS
    'sha256[:32] of previous entry chain root; first entry uses 32 zero bytes.';
COMMENT ON COLUMN audit_ledger_entries.merkle_root IS
    'sha256[:32] of this entry = sha256(prev_merkle_root || entry_hash).';
COMMENT ON COLUMN audit_ledger_entries.pubkey_fingerprint IS
    'sha256(pubkey)[:8] hex (16 chars); user looks up public key at /.well-known/huakai-pubkey.json.';
COMMENT ON COLUMN audit_ledger_entries.signature IS
    'base64-encoded ed25519 signature over entry_hash bytes (64-byte sig → ~88-char base64).';

COMMIT;
