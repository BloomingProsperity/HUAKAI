-- 0172_models_protocol_family_registered_adapters.down.sql
--
-- 回滚到 0011 的 models.protocol_family CHECK 集合。若已存在 0172 新增
-- family 的模型行，直接拒绝回滚，要求操作员先迁移或删除相关模型数据。

BEGIN;

LOCK TABLE models IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    bad_count integer;
BEGIN
    SELECT count(*) INTO bad_count
    FROM models
    WHERE protocol_family IN (
        'kimi_chat',
        'qwen_chat',
        'glm_chat',
        'yi_chat',
        'baichuan_chat',
        'doubao_chat',
        'ernie_chat',
        'step_chat',
        'hunyuan_chat',
        'minimax_chat',
        'cohere_chat',
        'ollama_chat',
        'ollama_native',
        'dify_chat',
        'replicate_image',
        'vertex_gemini',
        'vertex_anthropic',
        'gemini_code_assist'
    );
    IF bad_count > 0 THEN
        RAISE EXCEPTION
            '0172 down 拒绝执行：models 表含 % 行使用 0172 新增的 protocol_family。请先迁移或删除这些模型行，再回滚。', bad_count;
    END IF;
END $$;

ALTER TABLE models DROP CONSTRAINT IF EXISTS models_protocol_family_check;

ALTER TABLE models ADD CONSTRAINT models_protocol_family_check CHECK (
    protocol_family IN (
        'anthropic_messages',
        'openai_chat',
        'openai_responses',
        'gemini_messages',
        'openrouter_chat',
        'bedrock_invoke',
        'grok_chat',
        'deepseek_chat',
        'mistral_chat',
        'groqcloud_chat',
        'together_chat',
        'perplexity_chat',
        'fireworks_chat',
        'openai_codex',
        'cursor_session',
        'copilot_session',
        'gemini_advanced_session',
        'antigravity_session',
        'kiro_session',
        'windsurf_session'
    )
);

COMMENT ON COLUMN models.protocol_family IS
    'Protocol family 字符串。与 0011 迁移后的旧集合一致。';

COMMIT;
