-- 0219_model_discovery_inbox_reversed_lane_vendors.up.sql
--
-- 账号级模型发现(accountmodeldiscovery)覆盖 openai/anthropic/gemini/grok/kimi/antigravity
-- 全车道,含反转号(claude session / codex / code assist / antigravity)。但发现箱
-- model_discovery_inbox 原始 CHECK 只放行 vendor∈(openai,anthropic,gemini) 与 4 个协议族,
-- 反转号车道模型无法入箱 → 上架管道第 2 关对账号级发现整体断掉。
-- 这里把 vendor 与 protocol_family 白名单扩到与 accountmodeldiscovery 请求合同一致,
-- 与 models 表 protocol_family CHECK(0172/0174)对齐,仍只存公开目录元数据。

BEGIN;

ALTER TABLE model_discovery_inbox
    DROP CONSTRAINT IF EXISTS model_discovery_inbox_vendor_check,
    ADD CONSTRAINT model_discovery_inbox_vendor_check
        CHECK (vendor IN ('openai', 'anthropic', 'gemini', 'grok', 'kimi', 'antigravity'));

ALTER TABLE model_discovery_inbox
    DROP CONSTRAINT IF EXISTS model_discovery_inbox_protocol_family_check,
    ADD CONSTRAINT model_discovery_inbox_protocol_family_check
        CHECK (protocol_family IN (
            'anthropic_messages',
            'anthropic_claude_session',
            'openai_chat',
            'openai_responses',
            'openai_codex',
            'gemini_messages',
            'gemini_code_assist',
            'antigravity_session',
            'grok_chat',
            'kimi_chat'
        ));

COMMIT;
