-- 回滚 Stage 3 归属 text 列。
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS actor_ref;
ALTER TABLE payment_refund_requests DROP COLUMN IF EXISTS decided_by_actor;
