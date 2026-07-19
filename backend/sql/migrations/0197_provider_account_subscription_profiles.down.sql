BEGIN;

DROP TRIGGER IF EXISTS provider_account_subscription_observations_append_only
    ON provider_account_subscription_observations;
DROP FUNCTION IF EXISTS reject_provider_account_subscription_observation_mutation();
DROP TABLE IF EXISTS provider_account_subscription_states;
DROP TABLE IF EXISTS provider_account_subscription_observations;

ALTER TABLE account_credentials
    DROP CONSTRAINT IF EXISTS account_credentials_tenant_id_id_key;

COMMIT;
