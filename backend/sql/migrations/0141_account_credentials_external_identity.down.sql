-- 0141_account_credentials_external_identity.down.sql
--
-- 回滚 0141 up：删除上游账户身份索引与两列。纯加性变更的精确逆操作，
-- 沿用 0128_provider_account_refresh_lead 的 DROP ... IF EXISTS 形态。

DROP INDEX IF EXISTS idx_account_credentials_external_account;

ALTER TABLE account_credentials DROP COLUMN IF EXISTS external_account_email;
ALTER TABLE account_credentials DROP COLUMN IF EXISTS external_account_id;
