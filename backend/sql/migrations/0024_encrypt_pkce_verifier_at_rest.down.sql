-- 0024_encrypt_pkce_verifier_at_rest.down.sql
--
-- Roll back the additive F-AUTH-007 round-2 PKCE verifier storage change.

BEGIN;

ALTER TABLE oauth_flow_sessions
    DROP COLUMN IF EXISTS pkce_verifier_ciphertext;

COMMIT;
