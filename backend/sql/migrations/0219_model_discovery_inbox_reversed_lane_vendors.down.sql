-- 0219 回滚:恢复窄白名单前,先删掉反转号车道产生的、旧 CHECK 无法容纳的发现行。
-- 发现箱只存公开目录元数据,删除不影响已 promote 出的 models(promoted_model_id 不级联删)。

BEGIN;

DELETE FROM model_discovery_inbox
WHERE vendor NOT IN ('openai', 'anthropic', 'gemini')
   OR protocol_family NOT IN (
        'anthropic_messages', 'openai_chat', 'openai_responses', 'gemini_messages'
   );

ALTER TABLE model_discovery_inbox
    DROP CONSTRAINT IF EXISTS model_discovery_inbox_vendor_check,
    ADD CONSTRAINT model_discovery_inbox_vendor_check
        CHECK (vendor IN ('openai', 'anthropic', 'gemini'));

ALTER TABLE model_discovery_inbox
    DROP CONSTRAINT IF EXISTS model_discovery_inbox_protocol_family_check,
    ADD CONSTRAINT model_discovery_inbox_protocol_family_check
        CHECK (protocol_family IN (
            'anthropic_messages', 'openai_chat', 'openai_responses', 'gemini_messages'
        ));

COMMIT;
