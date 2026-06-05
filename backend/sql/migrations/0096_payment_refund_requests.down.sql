BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_refund_requests) THEN
        RAISE EXCEPTION 'refusing to roll back 0096: refund request workflow data exists; production rollback requires an Owner-gated data plan';
    END IF;
END $$;

DROP TABLE IF EXISTS payment_refund_requests;

COMMIT;
