-- 0024_encrypt_pkce_verifier_at_rest.up.sql
--
-- F-AUTH-007 round 2: store user-auth OAuth PKCE verifier material
-- encrypted at rest. This migration is additive only; existing short-lived
-- flow rows keep their legacy verifier until natural expiry so deploys do not
-- break in-flight OAuth callbacks.

BEGIN;

ALTER TABLE oauth_flow_sessions
    ADD COLUMN IF NOT EXISTS pkce_verifier_ciphertext bytea;

COMMENT ON COLUMN oauth_flow_sessions.pkce_verifier_ciphertext IS
    'AES-256-GCM envelope for short-lived PKCE verifier material. Raw verifier must never be stored here.';

COMMENT ON COLUMN oauth_flow_sessions.pkce_verifier IS
    'Deprecated compatibility column. New application writes a non-secret sentinel; legacy rows may contain plaintext until expiry.';

COMMIT;
