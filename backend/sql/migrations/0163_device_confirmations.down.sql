-- 回滚新设备确认表 (additive only 迁移的逆操作: 仅删本切片新建的表 + 索引)。
BEGIN;

DROP INDEX IF EXISTS idx_device_confirmations_user_status;
DROP INDEX IF EXISTS uq_device_confirmations_token_hash;
DROP TABLE IF EXISTS device_confirmations;

COMMIT;
