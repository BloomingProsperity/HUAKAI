BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM settlement_intents
        WHERE recovery_payload IS NOT NULL
    ) THEN
        RAISE EXCEPTION '不能回滚 0227：仍存在尚未收敛的结算恢复载荷';
    END IF;
END
$$;

DROP INDEX IF EXISTS idx_settlement_intents_failed_recovery;

ALTER TABLE settlement_intents
    DROP CONSTRAINT IF EXISTS settlement_intents_recovery_payload_object,
    DROP COLUMN IF EXISTS recovery_failure_class,
    DROP COLUMN IF EXISTS recovery_payload;

COMMIT;
