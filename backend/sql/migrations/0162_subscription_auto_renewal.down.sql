-- 回滚订阅自动续费账本 (additive only 迁移的逆操作: 仅删本切片新建的表 + 索引)。
BEGIN;

DROP INDEX IF EXISTS idx_sub_auto_renewal_user;
DROP INDEX IF EXISTS uq_sub_auto_renewal_period;
DROP TABLE IF EXISTS subscription_auto_renewal_charges;

COMMIT;
