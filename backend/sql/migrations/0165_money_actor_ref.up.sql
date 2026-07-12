-- role 制单登录 money-via-login Stage 1(payment):双身份归属 text 列。
-- 既有 bigint 列(created_by_admin_id 等)只能存 admin_tokens.id,区分不了
-- 令牌 admin 与登录 admin(users.id 与 token id 是两个 id 空间)。新列存
-- AuditActor() 串("admin_token:<id>" / "admin_user:<id>"),与 admin_audit_events.actor_id
-- 的 P2b-1 text 格式同源。纯加列非破坏:token 通道双写(旧 bigint + 新 text),
-- session 通道旧列落 NULL、新列落 admin_user:<id>。存量行不回填(Owner Q1 决策)。
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS created_by_actor text;
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS confirmed_by_actor text;
ALTER TABLE payment_audit_events ADD COLUMN IF NOT EXISTS actor_ref text;
