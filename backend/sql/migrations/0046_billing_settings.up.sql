-- 0046_billing_settings.up.sql
--
-- Case C 计费策略的租户级设置表。当前用于保存流式仅输入后中断
-- 场景的结算策略; 不存放密钥材料, 也不复用 email_settings 的 SMTP 语义。

BEGIN;

CREATE TABLE IF NOT EXISTS billing_settings (
    id            bigserial PRIMARY KEY,
    tenant_id     bigint      NOT NULL REFERENCES tenants(id),
    setting_key   text        NOT NULL,
    setting_value text        NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    text        NOT NULL,
    UNIQUE (tenant_id, setting_key),
    CHECK (setting_key <> ''),
    CHECK (
        setting_key <> 'stream_input_only_interrupted_policy'
        OR setting_value IN ('no_bill', 'no_bill_record')
    )
);

CREATE INDEX IF NOT EXISTS idx_billing_settings_tenant_updated
    ON billing_settings (tenant_id, updated_at DESC);

COMMENT ON TABLE billing_settings IS
    'Case C 计费策略的租户级设置表, 保存每个租户可配置的计费行为。';

COMMIT;
