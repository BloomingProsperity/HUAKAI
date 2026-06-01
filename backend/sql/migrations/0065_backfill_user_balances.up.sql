-- 0065_backfill_user_balances.up.sql
--
-- MONEY-6: reconcile user_balances from durable USD money events so mandatory
-- enforcement does not mis-block old customers whose voucher credit predates
-- the voucher -> user_balances bridge.

BEGIN;

-- balance backfill begin
WITH money_events AS (
    SELECT
        vr.tenant_id,
        vr.user_id,
        (vr.amount_cents::numeric / 100)::numeric(20, 8) AS delta,
        vr.redeemed_at AS occurred_at
    FROM voucher_redemption vr
    WHERE vr.status = 'succeeded'
      AND vr.currency_code = 'USD'

    UNION ALL

    SELECT
        ro.tenant_id,
        ro.user_id,
        be.actual_cost_signed::numeric(20, 8) AS delta,
        be.occurred_at
    FROM billing_events be
    JOIN recharge_orders ro
      ON ro.tenant_id = be.tenant_id
     AND ro.id = be.recharge_order_id
    WHERE be.event_type = 'balance_recharged'
      AND ro.currency_code = 'USD'
      AND be.actual_cost_signed > 0

    UNION ALL

    SELECT
        blc.tenant_id,
        blc.user_id,
        (-bh.captured)::numeric(20, 8) AS delta,
        be.occurred_at
    FROM billing_events be
    JOIN billing_ledger_claims blc
      ON blc.tenant_id = be.tenant_id
     AND blc.id = be.claim_id
    JOIN balance_holds bh
      ON bh.tenant_id = be.tenant_id
     AND bh.claim_id = be.claim_id
     AND bh.user_id = blc.user_id
    WHERE be.event_type = 'claim_committed'
      AND blc.currency_code = 'USD'
      AND bh.state = 'captured'
      AND bh.captured > 0

    UNION ALL

    SELECT
        blc.tenant_id,
        blc.user_id,
        (-be.actual_cost_signed)::numeric(20, 8) AS delta,
        be.occurred_at
    FROM billing_events be
    JOIN billing_ledger_claims blc
      ON blc.tenant_id = be.tenant_id
     AND blc.id = be.claim_id
    WHERE be.event_type = 'reconciliation_appended'
      AND blc.currency_code = 'USD'
      AND be.actual_cost_signed < 0
),
ledger_balances AS (
    SELECT
        tenant_id,
        user_id,
        SUM(delta)::numeric(20, 8) AS balance,
        COALESCE(MAX(occurred_at), NOW()) AS updated_at
    FROM money_events
    GROUP BY tenant_id, user_id
    HAVING SUM(delta) > 0
)
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
SELECT tenant_id, user_id, balance, 0, 1, updated_at
FROM ledger_balances
ON CONFLICT (tenant_id, user_id) DO UPDATE
SET balance = GREATEST(user_balances.balance, EXCLUDED.balance),
    version = CASE
        WHEN EXCLUDED.balance > user_balances.balance THEN user_balances.version + 1
        ELSE user_balances.version
    END,
    updated_at = CASE
        WHEN EXCLUDED.balance > user_balances.balance THEN GREATEST(user_balances.updated_at, EXCLUDED.updated_at)
        ELSE user_balances.updated_at
    END
WHERE EXCLUDED.balance > user_balances.balance;
-- balance backfill end

COMMIT;
