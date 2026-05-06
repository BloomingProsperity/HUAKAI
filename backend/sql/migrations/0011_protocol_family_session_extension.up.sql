-- 0011_protocol_family_session_extension.up.sql
--
-- 扩展两条已有 CHECK 约束以容纳 N+5d / 反转 atomic 引入的新 protocol family
-- 与新凭据形态。背景：
--
-- 1. models.protocol_family（0008 定义）原仅允许 4 个值：
--      anthropic_messages / openai_chat / openai_responses / gemini
--    但 registrydefault.Build() 现已注册的 protocol family 远超此列表。
--    如不扩 CHECK，operator 无法把这些 family 写入 models 表。
--
-- 2. provider_accounts.account_type（0001 定义）原仅允许 4 类：
--      oauth / api_key / service_account / upstream_static
--    缺 session 类型 → 反转 adapter 的 session_token 凭据无法落 DB。
--
-- 同时把 'gemini' 修正为 'gemini_messages'（与 registrydefault 常量对齐）。
--
-- DDL 顺序原则（codex 第 3 轮 review 修正）：
--   - LOCK TABLE 先于一切，避免 fail-fast 检查与 ALTER 之间的 TOCTOU race
--   - DROP CONSTRAINT 必须在 UPDATE 之前，否则 UPDATE 'gemini' → 'gemini_messages'
--     会被旧 CHECK 拒绝（旧 CHECK 不含 'gemini_messages'）
--   - ADD CONSTRAINT 在 UPDATE 之后，新枚举值此时已存在且数据已归位

BEGIN;

-- §0 锁表：避免并发写入塞进只在旧 CHECK 通过的行
LOCK TABLE models, provider_accounts IN ACCESS EXCLUSIVE MODE;

-- ============================================================================
-- §1 models.protocol_family 扩展
-- ============================================================================

-- §1.1 先 drop 旧 CHECK，否则下一步的 UPDATE 写入 'gemini_messages' 会违反旧约束
ALTER TABLE models DROP CONSTRAINT IF EXISTS models_protocol_family_check;

-- §1.2 兼容老数据：把 'gemini' 改名为 'gemini_messages'
UPDATE models SET protocol_family = 'gemini_messages' WHERE protocol_family = 'gemini';

-- §1.3 加新 CHECK，覆盖全部已实现 protocol family
ALTER TABLE models ADD CONSTRAINT models_protocol_family_check CHECK (
    protocol_family IN (
        -- 既有官方 API 路径
        'anthropic_messages',
        'openai_chat',
        'openai_responses',
        'gemini_messages',
        'openrouter_chat',
        'bedrock_invoke',
        'grok_chat',
        -- 6 家 OpenAI 兼容直 API key 路径
        'deepseek_chat',
        'mistral_chat',
        'groqcloud_chat',
        'together_chat',
        'perplexity_chat',
        'fireworks_chat',
        -- OpenAI 系订阅反转
        'openai_codex',
        -- 6 家订阅 session 反转
        'cursor_session',
        'copilot_session',
        'gemini_advanced_session',
        'antigravity_session',
        'kiro_session',
        'windsurf_session'
    )
);

COMMENT ON COLUMN models.protocol_family IS
    'Protocol family 字符串。与 registrydefault.Protocol* 常量一一对应。'
    '新增 family 时必须同步更新此 CHECK 约束。';

-- ============================================================================
-- §2 provider_accounts.account_type 扩展（加 session）
-- ============================================================================

ALTER TABLE provider_accounts DROP CONSTRAINT IF EXISTS provider_accounts_account_type_check;

ALTER TABLE provider_accounts ADD CONSTRAINT provider_accounts_account_type_check CHECK (
    account_type IN (
        'oauth',
        'api_key',
        'service_account',
        'upstream_static',
        -- 新增：订阅反转 session token 凭据形态。
        -- credentials JSONB 形态：{"session_token": "...", "extra": {...}}
        -- 由 PostgresCredentialVault.mapSession 解析为 CredentialTypeSessionToken。
        'session'
    )
);

COMMENT ON COLUMN provider_accounts.account_type IS
    'Account type 决定 credentials JSONB 解析路径。'
    'session 类型用于订阅 session 反转（cursor / copilot / gemini_advanced 等）。'
    '新增类型时必须同步更新 PostgresCredentialVault.mapCredential。';

COMMIT;
