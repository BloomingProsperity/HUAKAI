BEGIN;

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acquisition_flow_sessions_provider_account_id_fkey;

ALTER TABLE credential_acquisition_flow_sessions
    ADD CONSTRAINT credential_acquisition_flow_sessions_provider_account_id_fkey
    FOREIGN KEY (provider_account_id) REFERENCES provider_accounts(id);

COMMIT;
