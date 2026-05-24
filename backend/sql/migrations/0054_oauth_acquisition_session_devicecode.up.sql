-- 0054_oauth_acquisition_session_devicecode.up.sql
--
-- L-A fake/test-mode OAuth acquisition bootstrap.
-- Adds first-class auth type and transient device authorization payload
-- storage to the existing credential acquisition session table.

BEGIN;

DO $$
BEGIN
    CREATE TYPE oauth_acquisition_auth_type AS ENUM ('pkce', 'device_code', 'sso');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE credential_acquisition_flow_sessions
    ADD COLUMN IF NOT EXISTS auth_type oauth_acquisition_auth_type NOT NULL DEFAULT 'pkce',
    ADD COLUMN IF NOT EXISTS device_code_payload jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_device_code_payload_object,
    ADD CONSTRAINT credential_acq_device_code_payload_object
        CHECK (jsonb_typeof(device_code_payload) = 'object');

COMMENT ON COLUMN credential_acquisition_flow_sessions.auth_type IS
    'L-A acquisition auth type: pkce, device_code, or sso. First rollout is fake/test mode only.';
COMMENT ON COLUMN credential_acquisition_flow_sessions.device_code_payload IS
    'L-A transient fake/test-mode device authorization state. Do not store finalized upstream credentials here.';

COMMIT;
