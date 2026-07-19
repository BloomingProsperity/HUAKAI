BEGIN;

-- 上游目录同步只把未知模型放入发现箱；只有平台管理员显式上架后，模型才进入
-- 全局 registry。这里只保存公开目录元数据，不保存凭据、请求头或原始响应体。
CREATE TABLE model_discovery_inbox (
    id                          bigserial PRIMARY KEY,
    vendor                      text        NOT NULL
                                    CHECK (vendor IN ('openai', 'anthropic', 'gemini')),
    model_id_normalized         text        NOT NULL
                                    CHECK (length(model_id_normalized) BETWEEN 1 AND 512),
    provider_model_id           text        NOT NULL
                                    CHECK (length(provider_model_id) BETWEEN 1 AND 512),
    display_name                text        NOT NULL
                                    CHECK (length(display_name) BETWEEN 1 AND 512),
    owned_by                    text        NOT NULL
                                    CHECK (length(owned_by) BETWEEN 1 AND 128),
    protocol_family             text        NOT NULL
                                    CHECK (protocol_family IN
                                        ('anthropic_messages', 'openai_chat',
                                         'openai_responses', 'gemini_messages')),
    context_window              integer     NOT NULL DEFAULT 0
                                    CHECK (context_window >= 0),
    model_created_at            timestamptz,
    capabilities                text[]      NOT NULL DEFAULT '{}'::text[]
                                    CHECK (cardinality(capabilities) <= 64),
    status                      text        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending', 'promoted', 'ignored', 'absent')),
    first_seen_at               timestamptz NOT NULL DEFAULT now(),
    last_seen_at                timestamptz NOT NULL DEFAULT now(),
    last_absent_at              timestamptz,
    decided_at                  timestamptz,
    decided_by_actor            text,
    decision_reason             text,
    promoted_model_id           bigint REFERENCES models(id),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT model_discovery_inbox_vendor_model_unique
        UNIQUE (vendor, model_id_normalized),
    CONSTRAINT model_discovery_inbox_decision_consistency CHECK (
        (status IN ('pending', 'absent')
            AND decided_at IS NULL
            AND decided_by_actor IS NULL
            AND decision_reason IS NULL
            AND promoted_model_id IS NULL)
        OR
        (status = 'ignored'
            AND decided_at IS NOT NULL
            AND decided_by_actor IS NOT NULL
            AND decision_reason IS NOT NULL
            AND promoted_model_id IS NULL)
        OR
        (status = 'promoted'
            AND decided_at IS NOT NULL
            AND decided_by_actor IS NOT NULL
            AND decision_reason IS NOT NULL
            AND promoted_model_id IS NOT NULL)
    )
);

CREATE INDEX idx_model_discovery_inbox_status_id
    ON model_discovery_inbox (status, id DESC);
CREATE INDEX idx_model_discovery_inbox_vendor_status_id
    ON model_discovery_inbox (vendor, status, id DESC);
CREATE INDEX idx_model_discovery_inbox_last_seen
    ON model_discovery_inbox (last_seen_at DESC, id DESC);

COMMENT ON TABLE model_discovery_inbox IS
    '上游模型发现箱。未知模型必须经平台管理员显式上架，不能由同步任务直接进入全局目录。';
COMMENT ON COLUMN model_discovery_inbox.capabilities IS
    '经本地已知能力词表规范化后的公开能力摘要，不保存原始上游响应。';

-- 全局模型上架和忽略会改变所有部署租户可见的运营事实，必须与状态变更在同一
-- 事务写管理员日志。这里逐项延续 0192 的 action 白名单并加入两个新动作。
ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check CHECK (action = ANY (ARRAY[
        'issue_api_key', 'revoke_api_key', 'list_api_keys', 'issue_admin_token', 'revoke_admin_token',
        'admin_login', 'create_provider_account', 'disable_provider_account', 'enable_provider_account',
        'delete_provider_account', 'create_account_credential', 'rotate_account_credential',
        'disable_account_credential', 'delete_account_credential', 'list_account_credentials',
        'credential_acquisition_started', 'credential_acquisition_completed', 'credential_acquisition_failed',
        'credential_acquisition_cancelled', 'update_billing_settings', 'create_pool_group', 'update_pool_group',
        'update_platform_settings', 'unlock_user', 'force_disable_2fa', 'reset_passkey', 'set_user_group',
        'set_user_remark', 'set_user_status', 'create_user', 'delete_user', 'create_quota_policy',
        'update_quota_policy', 'delete_quota_policy', 'clear_provider_account_rate_limit',
        'recover_provider_account_state', 'update_provider_account',
        'hermes.tool.dlq_replay', 'hermes.tool.account_pause', 'hermes.tool.account_resume',
        'hermes.tool.renew_trigger', 'hermes.tool.alert_rule_enable', 'hermes.tool.alert_rule_disable',
        'hermes.tool.moderation_keyword_enable', 'hermes.tool.moderation_keyword_disable',
        'create_provider', 'update_provider', 'delete_provider',
        'create_channel', 'update_channel', 'delete_channel',
        'resolve_credential_project',
        'cleanup_runtime_logs',
        'promote_model_discovery', 'ignore_model_discovery'
    ]::text[]));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group', 'platform_setting',
             'quota_policy', 'dlq_event', 'alert_rule', 'moderation_keyword',
             'provider', 'channel', 'runtime_logs', 'model_discovery'));

COMMIT;
