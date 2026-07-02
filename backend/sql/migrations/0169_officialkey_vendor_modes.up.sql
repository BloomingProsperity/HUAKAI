BEGIN;
-- 官 key 厂商扩容(Owner 2026-07-02 指派:接 Grok + 国内大厂):纯加性 CHECK 扩展,镜像 0143 的 DROP+ADD 形状。
-- ① 新增 vendor×api_key 组合:grok/deepseek/kimi + qwen/glm/yi/baichuan/doubao/minimax/ernie/hunyuan/step
--    (出站均为 OpenAI 兼容 Bearer 端点,协议族已在 registrydefault 注册,此前只差存储放行);
-- ② 治愈潜伏缺陷:代码里早有 handlerSpec+ModePlan 的组合(grok/xai_oauth、kimi/kimi_oauth、
--    copilot/copilot_oauth、antigravity/oauth、windsurf/oauth、gemini/oauth)从未进过 CHECK 白名单——
--    在真库下这些采集/落库路径必违反约束(dead-on-arrival),一并补入。
--    (gemini/oauth=operator 手动 OAuth,exchanger/刷新 adapter 全齐,为对抗审查抓回的第 6 组。)
-- 刻意不放行:openrouter/mistral/groqcloud/together/perplexity/fireworks(全球推理托管云,Owner 明确不接;
-- 代码层同样无 handlerSpec,存储+代码双层拒绝)。存量三家 vendor 的分支逐字保持 0143 原样。
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
            ('api_key', 'claude_ai_oauth', 'claude_code', 'bedrock', 'vertex_anthropic'))
        OR
        (vendor = 'openai' AND auth_mode IN
            ('api_key', 'chatgpt_oauth', 'codex_cli_oauth', 'azure', 'refresh_token', 'codex_web_oauth'))
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
