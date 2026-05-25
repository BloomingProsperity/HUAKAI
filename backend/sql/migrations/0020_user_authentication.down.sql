-- 0020_user_authentication.down.sql
--
-- Roll back F-AUTH-007 additive user-auth state.

BEGIN;

DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS email_verification_tokens;
DROP TABLE IF EXISTS oauth_flow_sessions;
DROP TABLE IF EXISTS social_identity_links;
DROP TABLE IF EXISTS invite_bindings;
DROP TABLE IF EXISTS invite_codes;

UPDATE users
SET status = 'disabled'
WHERE status IN ('pending_verification', 'locked', 'reset_required');

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_status_check,
    ADD CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled', 'deleted'));

ALTER TABLE users
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS failed_login_count,
    DROP COLUMN IF EXISTS password_version,
    DROP COLUMN IF EXISTS social_login_provider,
    DROP COLUMN IF EXISTS invite_code_used,
    DROP COLUMN IF EXISTS email_verified,
    DROP COLUMN IF EXISTS password_hash;

COMMIT;
