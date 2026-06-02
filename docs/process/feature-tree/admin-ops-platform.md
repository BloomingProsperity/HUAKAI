# Admin-Ops-Platform Feature Tree

**Domain summary:** Operator-facing control plane — user management, provider/channel CRUD, quota & billing administration, observability, routing config, credential lifecycle, and operational tooling. Backend: `backend/internal/adminhttp/`, `backend/internal/gatewayhttp/admin_*.go`, route registration `backend/cmd/gateway/routes.go:372-522`. Frontend: `frontend/` (only 2 of 6 sidebar pages fully wired; majority stubs/disabled).

---

## Feature Coverage Table

| # | Feature | Status | Evidence (file:line or grep terms tried) | Gap Note |
|---|---------|--------|------------------------------------------|----------|
| 1 | Admin authentication — token-based, role resolution | PRESENT | `backend/internal/admin/admin.go`, `backend/internal/admin/operator_auth.go` | Two roles: `platform_admin`, `tenant_operator`; per-actor rate-limit (30 issues/hr) |
| 2 | Admin API key issuance / listing / revoke | PRESENT | `backend/internal/adminhttp/api_keys_handler.go:62-66`; routes `backend/cmd/gateway/routes.go:380-386` | Expiry, env tagging, prefix-only display, audit trail |
| 3 | Dashboard — aggregate stats (requests/spend/users) | PARTIAL | `backend/internal/gatewayhttp/admin_observability_handler.go:58-68` (usage, claims, audit-events list endpoints) | List endpoints exist with filtering; **no aggregation/rollup endpoint** — totals by tenant/model/period not exposed |
| 4 | Usage records query (admin view) | PRESENT | `backend/cmd/gateway/routes.go:510`; `backend/internal/gatewayhttp/admin_observability_handler.go` | Cursor-paginated, tenant/time/model filters |
| 5 | Billing claims query (admin view) | PRESENT | `backend/cmd/gateway/routes.go:511`; `admin_observability_handler.go` | Admin-scoped billing claims |
| 6 | Audit event log (admin) | PRESENT | `backend/cmd/gateway/routes.go:512`; `backend/internal/db/admin/admin_audit.sql.go`; migration `0013_trust_chain_audit_ledger` | EventClass/Type/Severity/ActorID/time-range filters; cursor-paginated |
| 7 | Request log viewer (per-request search by user/model/status) | MISSING | Grep: `RequestLog`, `LogQuery`, `LogList`, `request_log` — no matches under `backend/internal/` HTTP handlers | new-api and sub2api both expose per-request log search; HUAKAI has usage records but not raw per-request searchable log entries |
| 8 | User management — admin list users | MISSING | Grep: `AdminListUser`, `ListUsers`, `UserManagementHandler`, `admin.*user.*list` — no matches | sub2api `~/refs/sub2api/controller/user.go` exposes full user CRUD; HUAKAI has no admin-facing user list endpoint |
| 9 | User management — admin view/edit user | MISSING | Grep: `AdminGetUser`, `AdminUpdateUser` — no matches | Needed for support workflows |
| 10 | User management — suspend / ban / unsuspend | MISSING | Grep: `BanUser`, `SuspendUser`, `EnableUser` — no matches | Critical for fraud/abuse ops; sub2api has `PUT /api/user/{id}` with status field |
| 11 | User management — admin reset password / force re-auth | MISSING | Grep: `AdminResetPassword`, `ForceLogout`, `InvalidateSession` — no matches | Present in sub2api and new-api |
| 12 | User management — admin view user balance | MISSING | Only `POST /admin/v1/balances/adjustments` (write); no read-only balance view per user-ID | Operator support requires "what is user X's balance?" without executing a transaction |
| 13 | Provider catalog listing | PRESENT | `backend/internal/adminhttp/provider_catalog_handler.go:57-61`; `backend/cmd/gateway/routes.go:391-394` | Read-only catalog |
| 14 | Channel catalog listing | PRESENT | `backend/internal/adminhttp/channel_catalog_handler.go:42-46`; `backend/cmd/gateway/routes.go:395-398` | Read-only |
| 15 | Provider account CRUD (create / read / update / soft-delete) | PRESENT | `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:166-173`; `backend/cmd/gateway/routes.go:400-443` | Full lifecycle |
| 16 | Provider account enable / disable | PRESENT | `PATCH /admin/v1/provider-accounts/{id}/enabled` — `backend/cmd/gateway/routes.go:413` | Toggle with audit event |
| 17 | Provider account test (live connectivity check) | PRESENT | `backend/internal/adminhttp/provider_account_test_handler.go`; `backend/cmd/gateway/routes.go:416` | Health-check call against real upstream |
| 18 | Provider account health view | PRESENT | `backend/internal/adminhttp/provider_account_health_handler.go:47-48`; `backend/cmd/gateway/routes.go:419` | Exposes state, last-error, latency |
| 19 | Channel health state control (pause / resume / force-active) | PRESENT | `backend/cmd/gateway/routes.go:430-435`; `backend/internal/gatewayhttp/channel_health_admin_handler.go:47-55` | State machine with audit |
| 20 | Credential management — create / list / rotate / state / delete | PRESENT | `backend/internal/gatewayhttp/admin_credentials_handler.go:66-76`; `backend/cmd/gateway/routes.go:444-458` | Full state machine: active/inactive/expired/rotated |
| 21 | Credential acquisition flows — paste / CLI / CSV / JSON / OAuth | PRESENT | `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:79-93`; `backend/cmd/gateway/routes.go:423-443` | 5 import modes, OAuth callback included |
| 22 | Credential renewal status | PRESENT | `GET /admin/v1/credentials/renew-status` — `backend/cmd/gateway/routes.go:444` | Proactive expiry monitoring |
| 23 | Pool management — CRUD | PRESENT | `backend/internal/gatewayhttp/admin_pools_handler.go:162-167`; `backend/cmd/gateway/routes.go:459-464` | TopK strategy, capability mode |
| 24 | Routing rules management (user-group → pool mapping) | PRESENT | `backend/internal/routeadminhttp/handler.go:84-88`; `backend/cmd/gateway/routes.go:504-509` | Model-pattern match, priority, soft-delete |
| 25 | Quota policy CRUD — admin set/view/override per-user quota | MISSING | Grep: `SetQuota`, `QuotaOverride`, `AdminQuota`, `quota.*http` — no matches; quota package exists at `backend/internal/quota/` with full service, but **zero HTTP admin endpoints** | Quota subsystem (migration 0070) is fully implemented but has no admin API surface; operators cannot view or override a user's quota |
| 26 | Quota usage view per user (how much consumed this window) | MISSING | Grep: `quota.*admin`, `admin.*quota` — no HTTP files; only `backend/internal/db/billing/observability.sql.go` internal | Needed alongside billing claims for support |
| 27 | Manual balance credit / debit (admin) | PRESENT | `backend/internal/adminhttp/balance_credit_handler.go:33-35`; `POST /admin/v1/balances/adjustments` | Idempotency key, actor attribution, net-balance return |
| 28 | Payment order management — list / get / confirm (admin) | PRESENT | `backend/internal/paymenthttp/handler.go:195-198`; `backend/cmd/gateway/routes.go:491-496` | Manual confirmation of payment orders |
| 29 | Refund / order reversal workflow | PARTIAL | `POST /admin/v1/balances/adjustments` with negative amount can credit back; but **no dedicated refund endpoint** with reason/status tracking | sub2api has a `refund` state on orders; HUAKAI relies on raw balance adjustment — no refund status, no idempotent refund ledger row |
| 30 | Subscription plan CRUD — create / list / get / disable | PRESENT | `backend/internal/subscriptionhttp/handler.go:234-237`; `backend/cmd/gateway/routes.go:497-500` | Daily/weekly/monthly USD caps; for-sale flag |
| 31 | Subscription assignment — assign / list / get / cancel | PRESENT | `backend/internal/subscriptionhttp/handler.go:239-242`; `backend/cmd/gateway/routes.go:501-503` | Direct activation, cancellation with audit trail |
| 32 | Subscription voucher creation (admin) | PRESENT | `POST /v1/admin/subscriptions/vouchers` — `backend/internal/subscriptionhttp/handler.go:243` | Bridges voucher + subscription systems |
| 33 | Voucher / redemption code management — create / batch / revoke | PRESENT | `backend/internal/gatewayhttp/voucher_handler.go:76-85`; `backend/cmd/gateway/routes.go:485-490` | Batch up to 1000, eligibility targeting, grant type |
| 34 | Billing settings per-tenant (stream-interrupt policy, etc.) | PRESENT | `backend/internal/gatewayhttp/admin_billing_settings_handler.go:67-69`; `backend/cmd/gateway/routes.go:465-472` | Transactional with previous-value audit trail |
| 35 | Email settings — get / update / test | PRESENT | `backend/internal/gatewayhttp/admin_email_settings_handler.go:45-47`; `backend/cmd/gateway/routes.go:373-378` | SMTP config, test send |
| 36 | Model catalog sync (trigger) | PRESENT | `backend/internal/adminhttp/model_sync_handler.go:54-56`; `POST /admin/v1/model-sync` | Reason field, platform_admin only |
| 37 | Model enable / disable per channel | MISSING | Grep: `ModelEnable`, `ModelDisable`, `EnableModel`, `DisableModel` — found only in registry error types / config, no admin HTTP handlers | new-api has per-channel model enable flags; HUAKAI has no API to enable/disable individual models available through a channel |
| 38 | Model pricing configuration (admin-set per-model rates) | MISSING | Grep: `ModelPricing`, `model.*price`, `price.*model` — only in `registry/postgres_registry.go` internal (read-path); no write admin endpoint | Operators cannot override pricing for a specific model via API; rates come from static config |
| 39 | Rate limit configuration — global / per-user / per-tenant | MISSING | Grep: `SetRateLimit`, `RateLimitConfig`, `RateLimitPolicy`, `GlobalRateLimit` — no admin HTTP handlers; only `clear-rate-limit` on provider accounts | Provider-level clear exists; but no endpoint to configure or inspect rate-limit policies for API consumers |
| 40 | Provider account rate-limit clear | PRESENT | `POST /admin/v1/provider-accounts/{id}/clear-rate-limit` — `backend/cmd/gateway/routes.go:418` | Clears upstream RL backoff state |
| 41 | Dead-letter queue — list / replay | PRESENT | `backend/internal/gatewayhttp/admin_dlq_handler.go`; `backend/cmd/gateway/routes.go:513-515` | Handler-scoped views, usage-record DLQ separate replay |
| 42 | L2 cache — stats / get entry / delete entry | PRESENT | `backend/internal/gatewayhttp/admin_cache_l2_handler.go:33-35`; `backend/cmd/gateway/routes.go:516-521` | Tenant-scoped visibility |
| 43 | Account modes (feature flags) — list | PARTIAL | `backend/internal/adminhttp/account_modes_handler.go:25`; `GET /admin/v1/account-modes` | **Read-only catalog only**; no mutation endpoint to enable/disable modes per tenant |
| 44 | Feature flag / account-mode mutation (per-tenant toggle) | MISSING | Grep: `FeatureFlag`, `FeatureToggle`, `AccountModeUpdate`, `PatchAccountMode` — no matches | Operators cannot enable experimental features for specific tenants via API |
| 45 | System-level health aggregate endpoint | MISSING | Grep: `SystemHealth`, `MetricsAdmin`, `HealthAggregate` — no matches | No single endpoint reporting overall system health (pool availability, DLQ depth, worker lag) |
| 46 | Notification / alerting system (threshold-based admin alerts) | MISSING | Grep: `Alert`, `Notification`, `AlertEmail`, `NotifyAdmin`, `ThresholdAlert` — matches only in `voucher/audit.go` (unrelated) | No alerting on low balance, channel failure rate, quota exhaustion; common in sub2api and new-api |
| 47 | Data export (CSV / JSON) for billing / usage | MISSING | Grep: `Export`, `CsvExport`, `ReportGenerate`, `DataExport` — only `backend/internal/proto/passthrough.go` (unrelated) | Needed for accounting, compliance, and self-serve operator reports |
| 48 | Webhook management — admin configure / list / test outbound webhooks | MISSING | Grep: `WebhookAdmin`, `AdminWebhook`, `WebhookConfig` — no matches; inbound payment webhook exists but no admin-configurable outbound hooks | Cannot wire automation (e.g., notify external system on payment, quota breach) |
| 49 | System announcements / banners (broadcast to users) | MISSING | Grep: `Announcement`, `SystemBanner`, `AdminBroadcast` — no matches | new-api has `announcement` CRUD; sub2api has notice system |
| 50 | Tenant management — create / list / configure tenants | MISSING | Grep: `TenantCreate`, `AdminListTenants`, `TenantAdmin` — no matches; tenant IDs exist in auth but no admin CRUD surface | Platform-admin cannot provision new tenants via API; must be done at DB level |
| 51 | Panel auth — user role resolution (whoami) | PRESENT | `backend/internal/panelauthhttp/handler.go:38-40`; `GET /v1/auth/me` | Session-based, role from `users.role` DB column, deny-by-default on soft-deleted accounts |
| 52 | Frontend admin UI — dashboard page | PARTIAL | `frontend/app/dashboard/page.tsx`; `frontend/components/dashboard/` (StatCard, TrendChart) | Dashboard page exists with stat cards and trend charts but consumes **mock/stub data** (confirmed by earlier audit) — violates "不做假" principle |
| 53 | Frontend admin UI — audit trail page | PRESENT | `frontend/app/audit/page.tsx`; `frontend/components/audit/` (HopChainTimeline, MerkleProofPanel) | Functional trust-chain audit UI |
| 54 | Frontend admin UI — accounts pool page | PARTIAL | `frontend/app/accounts/page.tsx`; sidebar link `disabled: true` | Page exists, sidebar navigation disabled |
| 55 | Frontend admin UI — API key management page | MISSING | Sidebar `href: '/api-keys'` with `disabled: true`; no `app/api-keys/` directory | Route stub only |
| 56 | Frontend admin UI — usage analytics page | MISSING | Sidebar `href: '/usage'` with `disabled: true`; no `app/usage/` directory | Route stub only |
| 57 | Frontend admin UI — settings page | MISSING | Sidebar `href: '/settings'` with `disabled: true`; no `app/settings/` directory | Route stub only |
| 58 | Frontend admin UI — user management page | MISSING | No sidebar entry; no `app/users/` directory | Not even a stub |
| 59 | Frontend admin UI — provider/channel management page | MISSING | No sidebar entry or directory | No UI for the richest backend feature cluster |
| 60 | Frontend admin UI — subscription/plan management page | MISSING | No sidebar entry or directory | Full backend implemented, zero UI |
| 61 | Frontend admin UI — voucher management page | MISSING | No sidebar entry or directory | Full backend implemented, zero UI |
| 62 | Admin impersonation / act-as-user (support debugging) | MISSING | Grep: `Impersonate`, `ActAs`, `SudoUser`, `SwitchUser` — no matches | Useful for operator support; sub2api has this pattern |
| 63 | Bulk user operations (batch quota reset, bulk ban, mass voucher) | PARTIAL | `POST /v1/admin/vouchers/batch` exists (`backend/internal/gatewayhttp/voucher_handler.go:79`); no bulk user or quota ops | Voucher batch only; no bulk user status change or quota reset |

---

## Top Missing Features, Ranked by Commercial Value

1. **User Management Admin API (list / view / suspend / ban / balance-view)** — Platform operators have no way to look up a user by ID, view their balance, or suspend a fraudulent account without direct DB access. This is table-stakes for any B2C platform. sub2api `~/refs/sub2api/controller/user.go` covers all these. Affects: fraud prevention, support workflows, incident response.

2. **Quota Override / View API (admin)** — The quota subsystem (`backend/internal/quota/`) is fully built with DB, service, and reconciler — but zero HTTP admin endpoints exist. Operators cannot inspect or override a user's quota window (e.g., lift a limit for a paying enterprise customer). Affects: B2B upsell, support escalations.

3. **Rate Limit Configuration API (per-user / per-tenant / global)** — Only `clear-rate-limit` on provider accounts exists. No endpoint to set, inspect, or override request-rate policies for API consumers. Affects: abuse prevention, enterprise SLA tiers.

4. **Model Enable/Disable + Per-channel Pricing** — Only a sync-trigger exists. Operators cannot expose a subset of models per tenant/channel or apply custom pricing. new-api `~/refs/new-api/controller/model.go` and `channel.go` both expose this. Affects: model governance, B2B custom pricing, cost control.

5. **Request Log Search (per user / model / status / time range)** — Usage records aggregate billing data; there is no raw per-request log search. Operators cannot diagnose "why did request X fail?" or investigate abuse patterns. new-api has a log-search endpoint. Affects: support resolution time, abuse detection.

6. **Aggregated Dashboard Metrics (rollup by tenant / model / period)** — Individual records can be queried with filters but there is no rollup/aggregate endpoint. The frontend dashboard currently shows stub/mock data. Affects: business intelligence, operator situational awareness.

7. **Notification / Alerting System (threshold-based)** — No alerting on low account balance, channel error rate spike, quota nearing exhaustion, or DLQ depth. Both sub2api and new-api have notification systems. Affects: SLA assurance, proactive ops.

8. **Data Export (CSV / JSON) for Billing and Usage** — No export endpoint. Operators and customers cannot extract billing data for reconciliation or compliance. Affects: accounting, audits, customer self-service.

9. **Tenant Management (create / list / configure)** — Tenants exist as DB rows but cannot be provisioned or configured via admin API. Blocks multi-tenant SaaS onboarding automation. Affects: sales ops, self-service signup flow.

10. **Feature Flag / Account-Mode Mutation (per-tenant toggle)** — `GET /admin/v1/account-modes` is read-only. No endpoint to enable or disable capability sets per tenant. Affects: gradual feature rollout, enterprise-specific opt-ins.

11. **Webhook Management (admin-configurable outbound hooks)** — No way to wire automation on payment events, quota breach, or channel failure. Affects: enterprise integrations, ops automation pipelines.

12. **Frontend Admin UI — Provider/Channel, Users, Subscriptions, Vouchers pages** — The backend for these four areas is production-grade, but there is zero operator UI. All four sidebar entries are missing or disabled. Affects: operator adoption, SaaS product readiness (ops team cannot work without CLI/direct API access).

13. **Refund Workflow (dedicated refund endpoint with status tracking)** — Current workaround is a negative balance adjustment. Missing: refund reason, refund-status lifecycle, idempotent refund ledger. Affects: customer trust, accounting reconciliation.

14. **System Health Aggregate Endpoint** — No single API returning overall pool availability, DLQ depth, worker lag, and channel error rates. Affects: on-call response, status-page integration.

15. **Admin Impersonation / Act-as-User** — Operators cannot test the user experience of a specific account without sharing credentials. Affects: premium support, debugging escalations.
