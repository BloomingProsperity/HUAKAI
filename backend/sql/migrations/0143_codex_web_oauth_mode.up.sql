BEGIN;
-- 加性扩展:openai vendor 允许 auth_mode='codex_web_oauth'(Codex 网页授权码/PKCE 流),
-- 与已有 codex_cli_oauth(device-code)并列。镜像 0016/0019 的 DROP+ADD CHECK 形状;纯加性。
-- 不加此值,codex_web_oauth 的 flow-session(0019)与 account_credentials(0016)写入会触发 CHECK 违反 → 整条获取/持久化流在真库下不可用。
ALTER TABLE account_credentials
    DROP CONSTRAINT IF EXISTS account_credentials_vendor_mode_check,
    ADD CONSTRAINT account_credentials_vendor_mode_check CHECK (
        (vendor = 'anthropic' AND auth_mode IN
            ('api_key', 'claude_ai_oauth', 'claude_code', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token', 'codex_web_oauth'))
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
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token', 'codex_web_oauth'))
        OR
        (vendor = 'gemini' AND auth_mode IN
            ('aistudio_api_key', 'vertex_sa', 'code_assist', 'google_one', 'antigravity'))
    );
COMMIT;
