-- 0141_account_credentials_external_identity.up.sql
--
-- 为 account_credentials 增加上游 provider 账户身份列：external_account_id /
-- external_account_email。它们承载凭据获取时从 OAuth token-exchange 响应 /
-- id_token 声明里自动提取出来的上游账户标识与邮箱（accountident 包负责提取）。
--
-- 为什么放 account_credentials（而非 provider_accounts）：
--   身份与具体获取到的凭据/auth_mode 内在 1:1 —— 一个 provider_account 可以持有
--   每个 (vendor, auth_mode) 各一份凭据；把身份放在产生该 token 的凭据行上，
--   与 token 同生命周期。不复用 provider_accounts.name（operator 自定义 label，
--   非稳定上游 id），也不复用 encrypted_payload（加密 blob 不可查询，违背去重/查找目的）。
--
-- 风险等级：medium 加性 schema 变更（非 auth-core / 非 money/ledger）。
--   - nullable text、无 default、无回填、无 NOT NULL、不重写存量行 —— 完全后向兼容。
--   - 身份是“账户管理元数据”，绝不参与鉴权/计费/配额决策；列上无外键、无 CHECK 约束。
--
-- DDL 纪律：沿用 0128_provider_account_refresh_lead 的 ADD COLUMN IF NOT EXISTS 形态。

ALTER TABLE account_credentials ADD COLUMN IF NOT EXISTS external_account_id text;
ALTER TABLE account_credentials ADD COLUMN IF NOT EXISTS external_account_email text;

COMMENT ON COLUMN account_credentials.external_account_id IS
    '上游 provider 账户标识，凭据获取时从 token-exchange 响应/id_token 声明自动提取。'
    '账户管理元数据，非鉴权/计费/配额输入。手工绑定为回退；NULL 表示未提取到。';
COMMENT ON COLUMN account_credentials.external_account_email IS
    '上游 provider 账户邮箱，与 external_account_id 同源提取。账户管理元数据，非密文。';

-- 未来去重/查找用的非唯一部分索引：按 (tenant_id, vendor, external_account_id) 过滤
-- 未删除且已提取到 id 的行。非唯一，避免约束同一上游账户多凭据的合法场景。
CREATE INDEX IF NOT EXISTS idx_account_credentials_external_account
    ON account_credentials (tenant_id, vendor, external_account_id)
    WHERE deleted_at IS NULL AND external_account_id IS NOT NULL;
