-- 回滚 S1a: 移除 users.role 列。
BEGIN;

ALTER TABLE users DROP COLUMN IF EXISTS role;

COMMIT;
