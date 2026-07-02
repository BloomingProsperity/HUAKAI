-- money-via-login Stage 3(refund):双身份归属 text 列(同 0165/0166 pattern)。
-- 纯加列非破坏,存量不回填。decided_by/actor_id 旧 bigint 列语义不变。
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS actor_ref text;
ALTER TABLE payment_refund_requests ADD COLUMN IF NOT EXISTS decided_by_actor text;
