-- money-via-login voucher 片(Owner 解禁后接入):双身份归属 text 列(同 0165-0167 pattern)。
-- 纯加列非破坏,存量不回填;旧 bigint 列(created_by_admin_id/revoked_by_admin_id)语义不变。
ALTER TABLE voucher ADD COLUMN IF NOT EXISTS created_by_actor text;
ALTER TABLE voucher ADD COLUMN IF NOT EXISTS revoked_by_actor text;
ALTER TABLE voucher_batch ADD COLUMN IF NOT EXISTS created_by_actor text;
