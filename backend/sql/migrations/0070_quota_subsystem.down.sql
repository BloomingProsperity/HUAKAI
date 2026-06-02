-- 0070_quota_subsystem 的 down migration。
-- 只移除 quota 自有表和索引, 不回滚或修改 billing core。

BEGIN;

DROP FUNCTION IF EXISTS quota_acquire_concurrency_slot(
    bigint, bigint, bigint, text, text, timestamptz, timestamptz, bigint
);

DROP INDEX IF EXISTS idx_quota_reconciliation_tenant_reservation;
DROP INDEX IF EXISTS idx_quota_reconciliation_tenant_stale_running;
DROP INDEX IF EXISTS idx_quota_reconciliation_tenant_due;
DROP INDEX IF EXISTS uq_quota_reconciliation_active_claim_kind;
DROP TABLE IF EXISTS quota_reconciliation_jobs;

DROP INDEX IF EXISTS idx_quota_audit_tenant_claim_time;
DROP INDEX IF EXISTS idx_quota_audit_tenant_decision_time;
DROP INDEX IF EXISTS idx_quota_audit_tenant_time;
DROP TABLE IF EXISTS quota_audit_events;

DROP INDEX IF EXISTS idx_quota_slots_tenant_reservation_status;
DROP INDEX IF EXISTS idx_quota_slots_tenant_active_scope;
DROP TABLE IF EXISTS quota_concurrency_slots;
DROP TABLE IF EXISTS quota_concurrency_scope_locks;

DROP INDEX IF EXISTS idx_quota_reservations_tenant_created;
DROP INDEX IF EXISTS idx_quota_reservations_tenant_status_lease;
DROP TABLE IF EXISTS quota_reservations;

DROP INDEX IF EXISTS idx_quota_windows_tenant_open;
DROP INDEX IF EXISTS idx_quota_windows_tenant_policy_end;
DROP TABLE IF EXISTS quota_windows;

DROP INDEX IF EXISTS idx_quota_policies_tenant_validity;
DROP INDEX IF EXISTS idx_quota_policies_tenant_scope_metric;
DROP INDEX IF EXISTS uq_quota_policies_live_scope_metric;
DROP TABLE IF EXISTS quota_policies;

COMMIT;
