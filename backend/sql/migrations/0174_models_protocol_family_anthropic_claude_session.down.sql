-- 0174_models_protocol_family_anthropic_claude_session.down.sql
--
-- 回到 0172 的完整 allowlist。若模型仍使用 session family，拒绝回滚，
-- 避免先缩 CHECK 再留下无法表达的生产配置。

BEGIN;

LOCK TABLE models IN ACCESS EXCLUSIVE MODE;

DO $$
DECLARE
    bad_count integer;
BEGIN
    SELECT count(*) INTO bad_count
    FROM models
    WHERE protocol_family = 'anthropic_claude_session';
    IF bad_count > 0 THEN
        RAISE EXCEPTION
            '0174 down 拒绝执行：models 表含 % 行使用 anthropic_claude_session。请先迁移或删除这些模型行，再回滚。', bad_count;
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
        'windsurf_session',
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
    )
);

COMMENT ON COLUMN models.protocol_family IS
    'Protocol family 字符串。与 0172 迁移后的完整集合一致。';

COMMIT;
