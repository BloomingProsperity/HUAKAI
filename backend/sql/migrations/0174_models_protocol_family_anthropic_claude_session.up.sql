-- 0174_models_protocol_family_anthropic_claude_session.up.sql
--
-- 为已完成运行时闭合的 Claude OAuth/session 协议族开放模型配置。
-- CHECK 重建需要 ACCESS EXCLUSIVE 锁，部署前应先确认 models 体量与锁等待预算。

BEGIN;

LOCK TABLE models IN ACCESS EXCLUSIVE MODE;

ALTER TABLE models DROP CONSTRAINT IF EXISTS models_protocol_family_check;

ALTER TABLE models ADD CONSTRAINT models_protocol_family_check CHECK (
    protocol_family IN (
        'anthropic_messages',
        'anthropic_claude_session',
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
    'Protocol family 字符串。与 registrydefault 已有 adapter 注册路径对齐；新增 family 时必须同步更新 CHECK 与配置面校验。';

COMMIT;
