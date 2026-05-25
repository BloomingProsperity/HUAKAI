-- 0054_oauth_acquisition_session_devicecode.down.sql
--
-- Revert L-A auth_type/device_code_payload session columns.

BEGIN;

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_device_code_payload_object,
    DROP COLUMN IF EXISTS device_code_payload,
    DROP COLUMN IF EXISTS auth_type;

DROP TYPE IF EXISTS oauth_acquisition_auth_type;

COMMIT;
