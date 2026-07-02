-- 回滚 Stage 2 归属 text 列(纯删列,不动既有 bigint 归属)。
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS assigned_by_actor;
ALTER TABLE subscription_audit_events DROP COLUMN IF EXISTS actor_ref;
ALTER TABLE subscription_plan_audit_events DROP COLUMN IF EXISTS actor_ref;
