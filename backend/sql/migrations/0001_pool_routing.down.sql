-- Down migration for 0001_pool_routing.

BEGIN;

DROP INDEX IF EXISTS idx_scheduler_outbox_lag_alert;
DROP INDEX IF EXISTS idx_scheduler_outbox_unconsumed;
DROP TABLE IF EXISTS scheduler_outbox;

DROP INDEX IF EXISTS idx_pool_audit_actor_time;
DROP INDEX IF EXISTS idx_pool_audit_tenant_type_time;
DROP TABLE IF EXISTS pool_routing_audit_events;

DROP INDEX IF EXISTS uq_model_routing_pool_model;
DROP TABLE IF EXISTS model_routing_overrides;

DROP INDEX IF EXISTS idx_routes_match_order;
DROP INDEX IF EXISTS uq_routes_tenant_name;
DROP TABLE IF EXISTS routes;

DROP INDEX IF EXISTS idx_sticky_expires_at;
DROP INDEX IF EXISTS uq_sticky_tenant_session_model;
DROP TABLE IF EXISTS sticky_bindings;

DROP INDEX IF EXISTS idx_slot_acq_orphan_sweep;
DROP INDEX IF EXISTS idx_slot_acq_account_status;
DROP INDEX IF EXISTS uq_slot_acq_token;
DROP TABLE IF EXISTS pool_slot_acquisitions;

DROP INDEX IF EXISTS idx_provider_accounts_health_until;
DROP INDEX IF EXISTS idx_provider_accounts_pool_dispatch;
DROP INDEX IF EXISTS uq_provider_accounts_tenant_name;
DROP TABLE IF EXISTS provider_accounts;

DROP INDEX IF EXISTS uq_channels_tenant_pool_name;
DROP TABLE IF EXISTS channels;

DROP INDEX IF EXISTS uq_pool_groups_tenant_name;
DROP TABLE IF EXISTS pool_groups;

DROP INDEX IF EXISTS uq_providers_tenant_code;
DROP TABLE IF EXISTS providers;

DROP INDEX IF EXISTS uq_tenants_name;
DROP TABLE IF EXISTS tenants;

COMMIT;
