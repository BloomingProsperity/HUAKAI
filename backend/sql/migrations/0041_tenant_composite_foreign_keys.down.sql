BEGIN;

ALTER TABLE account_credentials DROP CONSTRAINT IF EXISTS account_credentials_provider_account_id_fkey;
ALTER TABLE account_credentials
    ADD CONSTRAINT account_credentials_provider_account_id_fkey
    FOREIGN KEY (provider_account_id) REFERENCES provider_accounts(id);

ALTER TABLE model_pool_bindings DROP CONSTRAINT IF EXISTS model_pool_bindings_pool_group_id_fkey;
ALTER TABLE model_pool_bindings
    ADD CONSTRAINT model_pool_bindings_pool_group_id_fkey
    FOREIGN KEY (pool_group_id) REFERENCES pool_groups(id);

ALTER TABLE provider_accounts DROP CONSTRAINT IF EXISTS provider_accounts_channel_id_fkey;
ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_channel_id_fkey
    FOREIGN KEY (channel_id) REFERENCES channels(id);

ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_pool_group_id_fkey;
ALTER TABLE channels
    ADD CONSTRAINT channels_pool_group_id_fkey
    FOREIGN KEY (pool_group_id) REFERENCES pool_groups(id);

ALTER TABLE provider_accounts DROP CONSTRAINT IF EXISTS provider_accounts_tenant_id_id_key;
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_tenant_id_id_key;
ALTER TABLE pool_groups DROP CONSTRAINT IF EXISTS pool_groups_tenant_id_id_key;

COMMIT;
