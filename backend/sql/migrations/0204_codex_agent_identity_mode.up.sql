BEGIN;

ALTER TABLE account_credentials
    DROP CONSTRAINT IF EXISTS account_credentials_vendor_mode_check,
    ADD CONSTRAINT account_credentials_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'claude_setup_token', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token', 'codex_web_oauth', 'codex_agent_identity'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity', 'oauth'))
        OR
        (vendor = 'grok' AND auth_mode IN ('api_key', 'xai_oauth'))
        OR
        (vendor = 'kimi' AND auth_mode IN ('api_key', 'kimi_oauth'))
        OR
        (vendor = 'copilot' AND auth_mode = 'copilot_oauth')
        OR
        (vendor IN ('antigravity', 'windsurf') AND auth_mode = 'oauth')
        OR
        (vendor IN ('deepseek', 'qwen', 'glm', 'yi', 'baichuan', 'doubao', 'minimax', 'ernie', 'hunyuan', 'step')
            AND auth_mode = 'api_key')
    );

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_vendor_mode_check,
    ADD CONSTRAINT credential_acq_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'claude_setup_token', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token', 'codex_web_oauth', 'codex_agent_identity'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity', 'oauth'))
        OR
        (vendor = 'grok' AND auth_mode IN ('api_key', 'xai_oauth'))
        OR
        (vendor = 'kimi' AND auth_mode IN ('api_key', 'kimi_oauth'))
        OR
        (vendor = 'copilot' AND auth_mode = 'copilot_oauth')
        OR
        (vendor IN ('antigravity', 'windsurf') AND auth_mode = 'oauth')
        OR
        (vendor IN ('deepseek', 'qwen', 'glm', 'yi', 'baichuan', 'doubao', 'minimax', 'ernie', 'hunyuan', 'step')
            AND auth_mode = 'api_key')
    );

COMMIT;
