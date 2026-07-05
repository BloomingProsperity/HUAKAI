-- 补偿器清扫段的扫描索引: 按 lease 到期序取未终态预留。
-- 0070 的 idx_quota_reservations_tenant_status_lease 是 tenant 前导 + WHERE status='reserved',
-- 服务不了跨租户、含 reconciliation_needed 的清扫谓词; 无此索引时 knob 打开后
-- 每轮(分钟级)对无界增长的 quota_reservations 全表顺扫。
CREATE INDEX IF NOT EXISTS idx_quota_reservations_stale_sweep
    ON quota_reservations (lease_expires_at, id)
    WHERE status IN ('reserved', 'reconciliation_needed');
