-- 0140_provider_account_aws_sigv4_type.down.sql
--
-- 回滚 0140_up：把 provider_accounts.account_type CHECK 还原到 0011 的 5 值列表
-- {oauth, api_key, service_account, upstream_static, session}（去掉 'aws_sigv4'）。
--
-- DDL 顺序原则（沿用 0011_down 的纪律）：
--   §0 LOCK TABLE 先于一切（防 fail-fast 与 ALTER 之间 TOCTOU race）
--   §1 fail-fast 检查存量行；若有 account_type='aws_sigv4' 的行就 RAISE，
--      操作员手动处理（DELETE 或迁移到其余类型）后再回滚
--   §2 DROP 含 'aws_sigv4' 的 CHECK，ADD 0011 的 5 值 CHECK

BEGIN;

-- §0 锁表
LOCK TABLE provider_accounts IN ACCESS EXCLUSIVE MODE;

-- §1 fail-fast 检查 provider_accounts 表是否含 0140 引入的 aws_sigv4 行
DO $$
DECLARE
    bad_count integer;
BEGIN
    SELECT count(*) INTO bad_count
    FROM provider_accounts
    WHERE account_type = 'aws_sigv4';
    IF bad_count > 0 THEN
        RAISE EXCEPTION
            '0140 down 拒绝执行：provider_accounts 表含 % 行 account_type=aws_sigv4。请先处理（DELETE 或迁移到 oauth/api_key/service_account/upstream_static/session），然后再回滚。', bad_count;
    END IF;
END $$;

-- §2 还原到 0011 的 5 值 CHECK
ALTER TABLE provider_accounts DROP CONSTRAINT IF EXISTS provider_accounts_account_type_check;

ALTER TABLE provider_accounts ADD CONSTRAINT provider_accounts_account_type_check CHECK (
    account_type IN (
        'oauth',
        'api_key',
        'service_account',
        'upstream_static',
        'session'
    )
);

COMMENT ON COLUMN provider_accounts.account_type IS
    'Account type 决定 credentials JSONB 解析路径。'
    'session 类型用于订阅 session 反转（cursor / copilot / gemini_advanced 等）。'
    '新增类型时必须同步更新 PostgresCredentialVault.mapCredential。';

COMMIT;
