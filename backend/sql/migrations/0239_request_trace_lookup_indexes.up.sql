-- 0239:按请求标识点查的索引。链路时间线投影按 billing_ledger_claims.logical_request_id 与四类账号级
-- 审计事件的请求标识精确查找;此前只有幂等键 / 租户+时间索引,部署者跨租户点查会在热表上顺序扫描。
-- 只加索引,不改数据与约束。

BEGIN;

CREATE INDEX IF NOT EXISTS idx_billing_ledger_claims_logical_request
    ON billing_ledger_claims (logical_request_id);

-- 部署者省略 tenant_id 时按网关 HTTP 请求标识找 claim:既有 0029 索引以 tenant_id 为前导列,跨租户点查用不上。
CREATE INDEX IF NOT EXISTS idx_billing_events_audit_request
    ON billing_events (audit_request_id)
    WHERE audit_request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_channel_health_audit_request
    ON channel_health_audit_events (request_id)
    WHERE request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_pool_routing_audit_request
    ON pool_routing_audit_events (request_id)
    WHERE request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_rate_limit_audit_upstream_request
    ON rate_limit_audit_events (upstream_request_id)
    WHERE upstream_request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_oauth_refresh_audit_request
    ON oauth_refresh_audit_events (request_id)
    WHERE request_id IS NOT NULL;

COMMIT;
