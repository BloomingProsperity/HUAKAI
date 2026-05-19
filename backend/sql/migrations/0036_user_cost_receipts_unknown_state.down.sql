BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM user_cost_receipts
        WHERE validation_state = 'unknown'
    ) THEN
        RAISE EXCEPTION 'cannot rollback 0036_user_cost_receipts_unknown_state: unknown validation_state rows exist';
    END IF;
END $$;

ALTER TABLE user_cost_receipts
    DROP CONSTRAINT IF EXISTS user_cost_receipts_validation_state_check,
    ADD CONSTRAINT user_cost_receipts_validation_state_check
        CHECK (validation_state IN (
            'valid', 'provisional', 'mismatch_pending', 'mismatch_refunded',
            'not_billable', 'receipt_unavailable'
        ));

COMMIT;

