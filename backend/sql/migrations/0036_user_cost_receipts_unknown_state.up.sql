BEGIN;

ALTER TABLE user_cost_receipts
    DROP CONSTRAINT IF EXISTS user_cost_receipts_validation_state_check,
    ADD CONSTRAINT user_cost_receipts_validation_state_check
        CHECK (validation_state IN (
            'valid', 'provisional', 'mismatch_pending', 'mismatch_refunded',
            'not_billable', 'receipt_unavailable', 'unknown'
        ));

COMMIT;

