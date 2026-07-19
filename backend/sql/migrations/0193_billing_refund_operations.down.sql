BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM billing_refund_operations) THEN
        RAISE EXCEPTION 'refusing to roll back 0193: durable refund operation facts exist';
    END IF;
END $$;

DROP TRIGGER IF EXISTS billing_refund_operations_append_only_delete ON billing_refund_operations;
DROP TRIGGER IF EXISTS billing_refund_operations_append_only_update ON billing_refund_operations;
DROP TABLE IF EXISTS billing_refund_operations;
DROP INDEX IF EXISTS uq_billing_events_tenant_claim_id;

COMMENT ON TABLE cost_disputes IS
    '用户发起的费用争议记录；处理结果与资金动作由当前争议处理合同决定。';

COMMIT;
