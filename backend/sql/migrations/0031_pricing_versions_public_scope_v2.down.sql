BEGIN;

-- 撤回 0031 的 public-only 唯一约束，避免把全部历史行设回 true 时发生版本冲突。
DROP INDEX IF EXISTS idx_billing_pricing_versions_public;

ALTER TABLE billing_pricing_versions
    ADD COLUMN IF NOT EXISTS is_public boolean NOT NULL DEFAULT true;

ALTER TABLE billing_pricing_versions
    ALTER COLUMN is_public SET DEFAULT true;

-- 恢复 0030 老 backfill 语义：所有既有 pricing version 都视为 public。
UPDATE billing_pricing_versions
SET is_public = true;

COMMIT;
