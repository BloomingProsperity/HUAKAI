-- 0011_protocol_family_session_extension.down.sql
--
-- 回滚 0011_up：把 models.protocol_family 与 provider_accounts.account_type
-- CHECK 约束还原到 0008 / 0001 时的窄列表。
--
-- DDL 顺序原则（同 0011_up）：
--   §0 LOCK TABLE 先于一切（防 fail-fast 与 ALTER 之间 TOCTOU race）
--   §1 fail-fast 检查存量行；如果有新枚举值的行就 RAISE，操作员手动处理后再跑
--   §2 DROP 新 CHECK（必须先于 UPDATE，否则 'gemini_messages' → 'gemini' 会
--      被新 CHECK 拒绝；当前 CHECK 含 gemini_messages 但回滚目标不含）
--   §3 数据回滚（gemini_messages → gemini）
--   §4 ADD 旧 CHECK

BEGIN;

-- §0 锁表
LOCK TABLE models, provider_accounts IN ACCESS EXCLUSIVE MODE;

-- §1.1 fail-fast 检查 models 表是否含 0011 引入的 protocol_family
DO $$
DECLARE
    bad_count integer;
BEGIN
    SELECT count(*) INTO bad_count
    FROM models
    WHERE protocol_family IN (
        'openrouter_chat', 'bedrock_invoke', 'grok_chat',
        'deepseek_chat', 'mistral_chat', 'groqcloud_chat',
        'together_chat', 'perplexity_chat', 'fireworks_chat',
        'openai_codex',
        'cursor_session', 'copilot_session', 'gemini_advanced_session',
        'antigravity_session', 'kiro_session', 'windsurf_session'
    );
    IF bad_count > 0 THEN
        RAISE EXCEPTION
            '0011 down 拒绝执行：models 表含 % 行使用 0011 引入的 protocol_family。请先处理（DELETE 或 UPDATE 到 anthropic_messages/openai_chat/openai_responses/gemini 之一），然后再回滚。', bad_count;
    END IF;
END $$;

-- §1.2 fail-fast 检查 provider_accounts 表
DO $$
DECLARE
    bad_count integer;
BEGIN
    SELECT count(*) INTO bad_count
    FROM provider_accounts
    WHERE account_type = 'session';
    IF bad_count > 0 THEN
        RAISE EXCEPTION
            '0011 down 拒绝执行：provider_accounts 表含 % 行 account_type=session。请先处理（DELETE 或迁移到 oauth/api_key/service_account/upstream_static），然后再回滚。', bad_count;
    END IF;
END $$;

-- §2 DROP 新 CHECK（必须早于 UPDATE，否则 UPDATE 写 'gemini' 会被新 CHECK 拒绝）
ALTER TABLE models DROP CONSTRAINT IF EXISTS models_protocol_family_check;

-- §3 把 0011_up 引入的 'gemini_messages' 改名回退为 'gemini'
UPDATE models SET protocol_family = 'gemini' WHERE protocol_family = 'gemini_messages';

-- §4 还原 models.protocol_family 旧 CHECK
ALTER TABLE models ADD CONSTRAINT models_protocol_family_check CHECK (
    protocol_family IN (
        'anthropic_messages',
        'openai_chat',
        'openai_responses',
        'gemini'
    )
);

-- §5 还原 provider_accounts.account_type 旧 CHECK
ALTER TABLE provider_accounts DROP CONSTRAINT IF EXISTS provider_accounts_account_type_check;
ALTER TABLE provider_accounts ADD CONSTRAINT provider_accounts_account_type_check CHECK (
    account_type IN (
        'oauth',
        'api_key',
        'service_account',
        'upstream_static'
    )
);

COMMIT;
