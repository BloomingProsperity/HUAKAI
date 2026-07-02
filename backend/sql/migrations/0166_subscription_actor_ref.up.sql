-- money-via-login Stage 2(subscription):双身份归属 text 列(同 0165 pattern)。
-- 旧 bigint 列(assigned_by_admin_id / actor_id)语义不变;新列存 AuditActor() 串
-- ("admin_token:<id>"/"admin_user:<id>")。纯加列非破坏,存量不回填。
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS assigned_by_actor text;
ALTER TABLE subscription_audit_events ADD COLUMN IF NOT EXISTS actor_ref text;
ALTER TABLE subscription_plan_audit_events ADD COLUMN IF NOT EXISTS actor_ref text;
