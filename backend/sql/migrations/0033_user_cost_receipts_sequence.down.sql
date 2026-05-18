BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM user_cost_receipts
        GROUP BY request_id
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot rollback 0033_user_cost_receipts_sequence: duplicate request_id receipts exist';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_user_cost_receipts_request_seq;

ALTER TABLE user_cost_receipts
    DROP COLUMN IF EXISTS receipt_sequence;

ALTER TABLE user_cost_receipts
    ADD CONSTRAINT user_cost_receipts_request_id_key UNIQUE (request_id);

COMMIT;

