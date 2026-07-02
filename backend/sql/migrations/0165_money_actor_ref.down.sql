-- 回滚 Stage 1 归属 text 列(纯删列,不动既有 bigint 归属)。
ALTER TABLE payment_orders DROP COLUMN IF EXISTS created_by_actor;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS confirmed_by_actor;
ALTER TABLE payment_audit_events DROP COLUMN IF EXISTS actor_ref;
