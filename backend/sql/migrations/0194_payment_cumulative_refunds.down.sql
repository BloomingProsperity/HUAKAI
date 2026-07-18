BEGIN;

DROP TRIGGER IF EXISTS payment_refunds_append_only_delete ON payment_refunds;
DROP TRIGGER IF EXISTS payment_refunds_append_only_update ON payment_refunds;
DROP TRIGGER IF EXISTS payment_refunds_validate_insert ON payment_refunds;
DROP FUNCTION IF EXISTS enforce_payment_refund_insert();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_refunds) THEN
        RAISE EXCEPTION 'refusing to roll back 0194: durable payment refund facts exist';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_payment_refunds_order_time;

ALTER TABLE payment_refunds
    DROP CONSTRAINT IF EXISTS payment_refunds_request_effect_check,
    DROP CONSTRAINT IF EXISTS payment_refunds_requested_amount_check,
    DROP COLUMN IF EXISTS require_exact,
    DROP COLUMN IF EXISTS requested_amount_cents,
    ADD CONSTRAINT uq_payment_refunds_order UNIQUE (tenant_id, order_id);

COMMIT;
