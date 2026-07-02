-- 回滚 voucher 归属 text 列。
ALTER TABLE voucher DROP COLUMN IF EXISTS created_by_actor;
ALTER TABLE voucher DROP COLUMN IF EXISTS revoked_by_actor;
ALTER TABLE voucher_batch DROP COLUMN IF EXISTS created_by_actor;
