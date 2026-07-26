BEGIN;

ALTER TABLE settlement_intents
    ADD COLUMN recovery_payload jsonb,
    ADD COLUMN recovery_failure_class text,
    ADD CONSTRAINT settlement_intents_recovery_payload_object
        CHECK (recovery_payload IS NULL OR jsonb_typeof(recovery_payload) = 'object');

CREATE INDEX idx_settlement_intents_failed_recovery
    ON settlement_intents (updated_at, id)
    WHERE status = 'failed' AND recovery_payload IS NOT NULL;

COMMENT ON COLUMN settlement_intents.recovery_payload IS
    '已交付请求在主结算和恢复入队同时失败时保存的脱敏结算恢复载荷；追平终态后自动清除。';
COMMENT ON COLUMN settlement_intents.recovery_failure_class IS
    '恢复载荷对应的稳定失败分类，不保存原始错误文本。';

COMMIT;
