BEGIN;

ALTER TABLE credential_acquisition_flow_sessions
    DROP CONSTRAINT IF EXISTS credential_acq_terminal_auth_material_cleared,
    DROP CONSTRAINT IF EXISTS credential_acq_no_plaintext_auth_payload;

COMMENT ON COLUMN credential_acquisition_flow_sessions.encrypted_pkce_verifier IS
    '仅保存加密的 PKCE verifier；按过期和终态清理策略销毁。';
COMMENT ON COLUMN credential_acquisition_flow_sessions.nonce_hash IS
    '旧版 PKCE 加密元数据。';
COMMENT ON COLUMN credential_acquisition_flow_sessions.device_code_payload IS
    '旧版设备授权载荷列；回滚不会重建已经销毁的短期授权材料。';

COMMIT;
