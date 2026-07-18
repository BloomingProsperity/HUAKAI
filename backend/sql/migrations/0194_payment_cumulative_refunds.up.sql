BEGIN;

ALTER TABLE payment_refunds
    DROP CONSTRAINT IF EXISTS uq_payment_refunds_order,
    ADD COLUMN IF NOT EXISTS requested_amount_cents BIGINT,
    ADD COLUMN IF NOT EXISTS require_exact BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE payment_refunds
SET requested_amount_cents = amount_cents
WHERE requested_amount_cents IS NULL;

ALTER TABLE payment_refunds
    ALTER COLUMN requested_amount_cents SET NOT NULL,
    DROP CONSTRAINT IF EXISTS payment_refunds_requested_amount_check,
    ADD CONSTRAINT payment_refunds_requested_amount_check
        CHECK (requested_amount_cents > 0),
    DROP CONSTRAINT IF EXISTS payment_refunds_request_effect_check,
    ADD CONSTRAINT payment_refunds_request_effect_check CHECK (
        (require_exact = FALSE AND requested_amount_cents = amount_cents)
        OR
        (require_exact = TRUE AND requested_amount_cents >= amount_cents)
    );

CREATE INDEX IF NOT EXISTS idx_payment_refunds_order_time
    ON payment_refunds (tenant_id, order_id, created_at, id);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM payment_refunds AS refund
        LEFT JOIN payment_credits AS credit
          ON credit.tenant_id = refund.tenant_id
         AND credit.payment_order_id = refund.order_id
        WHERE credit.id IS NULL
           OR credit.user_id <> refund.user_id
           OR btrim(credit.currency_code) <> btrim(refund.currency)
    ) THEN
        RAISE EXCEPTION 'payment refund does not match its credited order fact';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM payment_refunds AS refund
        JOIN payment_credits AS credit
          ON credit.tenant_id = refund.tenant_id
         AND credit.payment_order_id = refund.order_id
        GROUP BY refund.tenant_id, refund.order_id, credit.amount_cents
        HAVING SUM(refund.amount_cents) > credit.amount_cents
    ) THEN
        RAISE EXCEPTION 'payment refund total exceeds credited amount';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION enforce_payment_refund_insert() RETURNS TRIGGER AS $$
DECLARE
    credited_amount BIGINT;
    credited_user BIGINT;
    credited_currency TEXT;
    refunded_amount BIGINT;
BEGIN
    SELECT credit.amount_cents, credit.user_id, btrim(credit.currency_code)
      INTO credited_amount, credited_user, credited_currency
      FROM payment_orders AS orders
      JOIN payment_credits AS credit
        ON credit.tenant_id = orders.tenant_id
       AND credit.payment_order_id = orders.id
     WHERE orders.tenant_id = NEW.tenant_id
       AND orders.id = NEW.order_id
     FOR UPDATE OF orders;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'payment refund requires a credited order';
    END IF;
    IF credited_user <> NEW.user_id OR credited_currency <> btrim(NEW.currency) THEN
        RAISE EXCEPTION 'payment refund identity does not match credited order';
    END IF;

    SELECT COALESCE(SUM(amount_cents), 0)::BIGINT
      INTO refunded_amount
      FROM payment_refunds
     WHERE tenant_id = NEW.tenant_id
       AND order_id = NEW.order_id;
    IF refunded_amount + NEW.amount_cents > credited_amount THEN
        RAISE EXCEPTION 'payment refund total exceeds credited amount';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS payment_refunds_validate_insert ON payment_refunds;
CREATE TRIGGER payment_refunds_validate_insert
    BEFORE INSERT ON payment_refunds
    FOR EACH ROW EXECUTE FUNCTION enforce_payment_refund_insert();

DROP TRIGGER IF EXISTS payment_refunds_append_only_update ON payment_refunds;
CREATE TRIGGER payment_refunds_append_only_update
    BEFORE UPDATE ON payment_refunds
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

DROP TRIGGER IF EXISTS payment_refunds_append_only_delete ON payment_refunds;
CREATE TRIGGER payment_refunds_append_only_delete
    BEFORE DELETE ON payment_refunds
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

WITH refund_totals AS (
    SELECT tenant_id, order_id, SUM(amount_cents)::BIGINT AS refunded_cents
    FROM payment_refunds
    GROUP BY tenant_id, order_id
)
UPDATE payment_orders AS orders
SET status = CASE
    WHEN totals.refunded_cents = credit.amount_cents THEN 'refunded'
    ELSE 'completed'
END
FROM payment_credits AS credit, refund_totals AS totals
WHERE orders.tenant_id = credit.tenant_id
  AND orders.id = credit.payment_order_id
  AND totals.tenant_id = orders.tenant_id
  AND totals.order_id = orders.id
  AND orders.status IN ('completed', 'refunded');

COMMIT;
