BEGIN;

-- 旧版进行中流程依赖明文设备授权载荷，清除后已无法继续。先明确转入失败态，
-- 保留稳定错误分类和重新发起提示，避免运维误判为仍可轮询。
UPDATE credential_acquisition_flow_sessions
SET status = 'failed',
    error_class = 'legacy_plaintext_auth_payload_removed',
    error_message_redacted = '升级已移除旧版明文授权载荷，请重新发起授权流程',
    encrypted_pkce_verifier = NULL,
    nonce_hash = NULL,
    device_code_payload = '{}'::jsonb,
    updated_at = clock_timestamp()
WHERE device_code_payload <> '{}'::jsonb
  AND status NOT IN ('finalized', 'cancelled', 'expired', 'failed')
  AND consumed_at IS NULL;

-- 终态或已进入最终化的旧行只销毁不再需要的兼容载荷。
UPDATE credential_acquisition_flow_sessions
SET device_code_payload = '{}'::jsonb
WHERE device_code_payload <> '{}'::jsonb;

-- 终态流程不再需要 PKCE 或设备授权临时材料。
UPDATE credential_acquisition_flow_sessions
SET encrypted_pkce_verifier = NULL,
    nonce_hash = NULL,
    device_code_payload = '{}'::jsonb
WHERE status IN ('finalized', 'cancelled', 'expired', 'failed')
   OR consumed_at IS NOT NULL;

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_no_plaintext_auth_payload,
    DROP CONSTRAINT IF EXISTS credential_acq_terminal_auth_material_cleared,
    ADD CONSTRAINT credential_acq_no_plaintext_auth_payload
        CHECK (device_code_payload = '{}'::jsonb),
    ADD CONSTRAINT credential_acq_terminal_auth_material_cleared
        CHECK (
            (
                status NOT IN ('finalized', 'cancelled', 'expired', 'failed')
                AND consumed_at IS NULL
            )
            OR (
                encrypted_pkce_verifier IS NULL
                AND nonce_hash IS NULL
                AND device_code_payload = '{}'::jsonb
            )
        );

COMMENT ON COLUMN credential_acquisition_flow_sessions.encrypted_pkce_verifier IS
    '加密的短期 OAuth 授权载荷；PKCE 保存 verifier，device-code/SSO 保存轮询材料，终态必须清空。';
COMMENT ON COLUMN credential_acquisition_flow_sessions.nonce_hash IS
    '短期 OAuth 授权载荷的加密元数据；与 encrypted_pkce_verifier 配对，终态必须清空。';
COMMENT ON COLUMN credential_acquisition_flow_sessions.device_code_payload IS
    '兼容占位列；生产代码禁止保存明文设备授权材料，值必须为空 JSON 对象。';

COMMIT;
