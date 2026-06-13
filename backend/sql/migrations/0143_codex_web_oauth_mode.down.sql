BEGIN;
-- 回滚 0143:把 openai vendor 的 auth_mode 白名单还原到 0016/0019 态(移除 codex_web_oauth)。dev/test only。
ALTER TABLE account_credentials
    DROP CONSTRAINT IF EXISTS account_credentials_vendor_mode_check,
    ADD CONSTRAINT account_credentials_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity'))
    );

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_vendor_mode_check,
    ADD CONSTRAINT credential_acq_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity'))
    );
COMMIT;
