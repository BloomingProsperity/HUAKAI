BEGIN;

-- 0030 已在部分环境执行，0031 负责补齐代码依赖的公开标记列。
DROP INDEX IF EXISTS idx_billing_pricing_versions_public;

ALTER TABLE billing_pricing_versions
    ADD COLUMN IF NOT EXISTS is_public boolean NOT NULL DEFAULT true;

-- 保留 tenant_id=0 (公开约定) 所有行 is_public=true。
-- 这是 a6262be 0030 默认 backfill (DEFAULT TRUE) 在 tenant_id=0 上的语义保留。
UPDATE billing_pricing_versions
SET is_public = true
WHERE tenant_id = 0;

-- 把 tenant_id!=0 (真 tenant-scoped) 全设 false，撤回 a6262be 0030 默认值错误。
UPDATE billing_pricing_versions
SET is_public = false
WHERE tenant_id != 0;

-- 改默认值：新行 default false，新 tenant 行不自动公开。
ALTER TABLE billing_pricing_versions
    ALTER COLUMN is_public SET DEFAULT false;

-- 公开 pricing version 必须全局唯一；tenant 私有 version 仍由 (tenant_id, version) 约束隔离。
CREATE UNIQUE INDEX IF NOT EXISTS idx_billing_pricing_versions_public
    ON billing_pricing_versions (version)
    WHERE is_public = true;

COMMIT;
