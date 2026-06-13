-- 0140_provider_account_aws_sigv4_type.up.sql
--
-- 扩展 provider_accounts.account_type CHECK 约束以容纳 'aws_sigv4'。
--
-- 背景（latent bug）：
--   PostgresCredentialVault.mapCredential 已有 case "aws_sigv4" → mapAWSSigV4，
--   解析出 Bedrock PassthroughAdapter 期望的 CredentialTypeAWSSigV4 凭据。
--   但 provider_accounts.account_type 的 CHECK 约束（最新形态由 0011 定义为
--   {oauth, api_key, service_account, upstream_static, session}）不含 'aws_sigv4'，
--   也没有后续 migration 补充。结果：任何 account_type='aws_sigv4' 的行都无法落库
--   → Bedrock-aws_sigv4 解析路径事实上不可达（dead code）。
--
-- 本 migration 把 'aws_sigv4' 加入该 CHECK，使 Bedrock SigV4 凭据形态可写入并被
-- vault 解析。account_type='aws_sigv4' 的 credentials JSONB 形态由
-- PostgresCredentialVault.mapAWSSigV4 解析：
--   {"aws_access_key_id": "...", "aws_secret_access_key": "...",
--    "aws_region": "...", "aws_session_token": "..." (可选, STS)}
--
-- DDL 顺序原则（沿用 0011_up 的纪律）：
--   §0 LOCK TABLE 先于一切，避免 fail-fast 检查与 ALTER 之间的 TOCTOU race
--   §1 DROP 旧 CHECK 后立即 ADD 含 'aws_sigv4' 的新 CHECK；纯加性，无数据回填

BEGIN;

-- §0 锁表：避免并发写入塞进只在旧 CHECK 通过的行
LOCK TABLE provider_accounts IN ACCESS EXCLUSIVE MODE;

-- §1 替换 account_type CHECK：在 0011 的 5 个值基础上加 'aws_sigv4'
ALTER TABLE provider_accounts DROP CONSTRAINT IF EXISTS provider_accounts_account_type_check;

ALTER TABLE provider_accounts ADD CONSTRAINT provider_accounts_account_type_check CHECK (
    account_type IN (
        'oauth',
        'api_key',
        'service_account',
        'upstream_static',
        'session',
        -- 新增：AWS SigV4 凭据形态（Bedrock InvokeModel 直签名直连）。
        -- credentials JSONB 形态：
        --   {"aws_access_key_id": "...", "aws_secret_access_key": "...",
        --    "aws_region": "...", "aws_session_token": "..." (可选)}
        -- 由 PostgresCredentialVault.mapAWSSigV4 解析为 CredentialTypeAWSSigV4。
        'aws_sigv4'
    )
);

COMMENT ON COLUMN provider_accounts.account_type IS
    'Account type 决定 credentials JSONB 解析路径。'
    'session 类型用于订阅 session 反转（cursor / copilot / gemini_advanced 等）。'
    'aws_sigv4 类型用于 Bedrock SigV4 直签名直连。'
    '新增类型时必须同步更新 PostgresCredentialVault.mapCredential 与写入路径的 account_type 校验白名单。';

COMMIT;
