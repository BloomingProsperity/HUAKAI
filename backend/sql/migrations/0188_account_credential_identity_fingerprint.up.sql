BEGIN;

ALTER TABLE account_credentials
    ADD COLUMN IF NOT EXISTS external_subject_id text,
    ADD COLUMN IF NOT EXISTS external_identity_source text,
    ADD COLUMN IF NOT EXISTS credential_material_fingerprint text;

COMMENT ON COLUMN account_credentials.external_subject_id IS
    '上游个人主体标识，来自凭据获取时的身份声明；仅用于租户范围内的账号识别与人工消歧，不参与鉴权、计费或配额。';
COMMENT ON COLUMN account_credentials.external_identity_source IS
    '上游身份元数据的来源；用于区分 provider token 交换结果与手工或导入声明，决定账号接入是否允许自动匹配。';
COMMENT ON COLUMN account_credentials.credential_material_fingerprint IS
    '有效凭据材料的租户域隔离 SHA-256 指纹；用于账号接入去重与冲突检测，绝不保存或还原凭据明文。';

CREATE INDEX IF NOT EXISTS idx_account_credentials_external_subject
    ON account_credentials (tenant_id, vendor, external_subject_id)
    WHERE deleted_at IS NULL AND external_subject_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_account_credentials_material_fingerprint
    ON account_credentials (tenant_id, vendor, credential_material_fingerprint)
    WHERE deleted_at IS NULL AND credential_material_fingerprint IS NOT NULL;

COMMIT;
