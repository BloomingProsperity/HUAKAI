-- 0025_email_settings.up.sql
--
-- F-AUTH-007 email delivery settings. Additive tenant-scoped SMTP config with
-- encrypted password material stored in setting_value.

BEGIN;

CREATE TABLE IF NOT EXISTS email_settings (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    setting_key     text        NOT NULL,
    setting_value   text        NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    updated_by      text        NOT NULL,
    UNIQUE (tenant_id, setting_key)
);

CREATE INDEX IF NOT EXISTS idx_email_settings_tenant_updated
    ON email_settings (tenant_id, updated_at DESC);

COMMENT ON TABLE email_settings IS
    'F-AUTH-007 tenant-scoped email delivery settings. SMTP password is stored as an AES-GCM envelope.';
COMMENT ON COLUMN email_settings.setting_value IS
    'Text setting value. smtp_password must be an encrypted envelope, never plaintext.';

COMMIT;
