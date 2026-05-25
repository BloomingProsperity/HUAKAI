BEGIN;

CREATE TABLE IF NOT EXISTS audit_signer_pubkeys (
    id BIGSERIAL PRIMARY KEY,
    fingerprint BYTEA UNIQUE NOT NULL,
    algorithm TEXT NOT NULL,
    public_key BYTEA NOT NULL,
    effective_from TIMESTAMP WITH TIME ZONE NOT NULL,
    effective_to TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT audit_signer_pubkeys_fingerprint_len CHECK (octet_length(fingerprint) = 16),
    CONSTRAINT audit_signer_pubkeys_public_key_len CHECK (octet_length(public_key) = 32),
    CONSTRAINT audit_signer_pubkeys_algorithm_check CHECK (algorithm IN ('ed25519')),
    CONSTRAINT audit_signer_pubkeys_effective_window_check CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_signer_pubkeys_active_algorithm
    ON audit_signer_pubkeys (algorithm)
    WHERE effective_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_audit_signer_pubkeys_effective_from
    ON audit_signer_pubkeys (effective_from);

COMMENT ON TABLE audit_signer_pubkeys IS
    'HUAKAI audit signer public-key registry. Keeps historical ed25519 public keys so old receipts and ledger entries remain verifiable after key rotation.';
COMMENT ON COLUMN audit_signer_pubkeys.fingerprint IS
    'Canonical lowercase sha256(pubkey)[:8] hex stored as 16 ASCII bytes; matches user_cost_receipts.signer_fingerprint.';
COMMENT ON COLUMN audit_signer_pubkeys.effective_to IS
    'NULL means current active key. Rotation closes the previous active row before opening the new row.';

COMMIT;
