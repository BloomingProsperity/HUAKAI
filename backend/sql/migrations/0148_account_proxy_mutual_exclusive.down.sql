-- 0148_account_proxy_mutual_exclusive.down.sql
ALTER TABLE provider_accounts
    DROP CONSTRAINT IF EXISTS chk_account_proxy_mutual_exclusive;
