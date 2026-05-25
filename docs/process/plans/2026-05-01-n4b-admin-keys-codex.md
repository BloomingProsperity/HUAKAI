# 2026-05-01 N+4b Admin API-key Issuance + Ledger FK Backfill — Codex Independent Plan

| Field | Value |
| --- | --- |
| Owner directive | "Codex Independent Plan Draft — N+4b (Admin API-key Issuance + Ledger FK Backfill)" |
| Lane | specifier (independent planner) |
| Counterpart | Claude drafts `docs/process/plans/2026-05-01-n4b-admin-keys-claude.md` in parallel; this file was written without reading it. |
| Scope | Plan only; no implementation, no commit. |
| Clean-room posture | Internal HUAKAI code/docs only; no non-MIT reference source read. |
| Observed regions | 19 repo regions |
| Inferences | 11, each marked `(inferred)` |
| Open questions | D1-D8 + DB1-DB5 |

## 1. Evidence Read

- N+4a agreed on `users` + `api_keys`, bcrypt, table-backed resolver, real smoke seeding, uniform `401`, `hk_live_` / `hk_test_`, 16-char prefix, composite tenant/user protection, and no sync `last_used_at` write. Evidence: `docs/process/plans/2026-04-30-n4-l0-minimum.md:15-33`.
- N+4a explicitly split auth replacement from ledger cleanup. Evidence: `docs/process/plans/2026-04-30-n4-l0-minimum.md:27-32`.
- Actual N+4a schema is `0007_l0_inbound_auth.up.sql`: `users` has no password/admin role column, while `api_keys` stores `key_hash`, `key_prefix`, status, expiry/revocation fields, and composite `(tenant_id,user_id)` FK. Evidence: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:13-42`, `backend/sql/migrations/0007_l0_inbound_auth.up.sql:45-93`.
- Resolver accepts only HUAKAI bearer namespaces, uses prefix length 16, caps bcrypt fanout at 5, and returns `Identity{TenantID,APIKeyID,UserID}` after status + bcrypt checks. Evidence: `backend/internal/auth/api_key_resolver.go:48-58`, `backend/internal/auth/api_key_resolver.go:96-145`, `backend/internal/auth/api_key_resolver.go:161-166`.
- Resolver/sql comments bind CMB-1/CMB-5/CMB-7 and source query reads `key_hash` only for resolver comparison with deleted parent filtering and `LIMIT 5`. Evidence: `backend/internal/auth/api_key_resolver.go:10-21`, `backend/sql/queries/auth_inbound.sql:1-48`.
- Gateway has admin shells but no `/admin/v1/api-keys`; current admin routes cover pools, provider accounts, usage, billing claims, audit events, and DLQ. Evidence: `backend/cmd/gateway/main.go:163-205`.
- API contract defines actor split: client developer uses HUAKAI API key; tenant operator manages own-tenant admin endpoints; platform admin handles elevated/cross-tenant actions. Evidence: `docs/specs/api-contract.md:45-66`.
- Every admin mutation must produce an audit event; OpenAPI `AuditEvent` includes actor fields, reason, payload, and role enum. Evidence: `docs/specs/api-contract.md:96-107`, `docs/openapi/openapi.yaml:1148-1158`.
- `billing_ledger_claims` and `usage_records` carry `api_key_id`/`user_id` without FKs; `billing_ledger_archive` carries `api_key_id` without FK. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:19-31`, `backend/sql/migrations/0002_observability_billing.up.sql:121-127`, `backend/sql/migrations/0002_observability_billing.up.sql:75-85`.
- `pool_slot_acquisitions` has no `api_key_id`; its `claim_id` comment says FK was deferred. Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:168-196`.
- Fixture debt exists in billing and pool integration tests via `tenantID*100` synthetic IDs; auth resolver tests and gateway smoke already seed real `users`/`api_keys`. Evidence: `backend/internal/billing/claim_gate_integration_test.go:35-53`, `backend/internal/pool/db_adapters_integration_test.go:48-63`, `backend/internal/auth/api_key_resolver_integration_test.go:55-110`, `backend/cmd/gateway/smoke_test.go:140-164`.
- CMB constraints: Router must not read credentials; plaintext credentials/API keys must not enter logs/spans/traces; Auth is read-only in hot path while Pool/Ledger have bounded writes. Evidence: `docs/specs/_invariants/cross-module-boundaries.md:101-105`, `docs/specs/_invariants/cross-module-boundaries.md:131-138`, `docs/specs/_invariants/cross-module-boundaries.md:147-158`.
- Tooling/rules: Go + stdlib/chi, PostgreSQL + sqlc, tenant_id on primary tables; sqlc reads `sql/migrations` and `sql/queries`. Evidence: `docs/RULES.md:90-100`, `backend/sqlc.yaml:1-15`.

## 2. Scope A — Admin API-key Issuance

Goal: replace manual DB inserts with an operator endpoint that creates an API key safely, returns plaintext exactly once, and records an auditable admin mutation.

Minimum N+4b:

1. Implement `POST /admin/v1/api-keys`; defer list/revoke/patch unless Owner explicitly expands scope.
2. Request: `tenant_id`, `user_id`, `name`, optional `display_name`, optional `expires_at`, `environment` (`live`/`test`), and `reason`.
3. Authenticate operator before generation/write.
4. Generate `hk_live_<random>` or `hk_test_<random>`, derive 16-char `key_prefix`, bcrypt hash full bearer, insert `api_keys`, insert audit event in one transaction, return plaintext only in `201` response.
5. Response metadata may include `id`, `tenant_id`, `user_id`, `name`, `display_name`, `key_prefix`, `status`, `expires_at`, `created_at`, plus one-time `plaintext_bearer`. Never return `key_hash`.

(inferred) Issuance should live outside hot-path `internal/auth` but reuse a shared prefix/format helper to avoid drift with resolver constants. Evidence: `backend/internal/auth/api_key_resolver.go:48-58`, `backend/internal/auth/api_key_resolver.go:161-166`.

## 3. Scope B — Ledger FK Backfill

Goal: make ledger identity references real so claim/usage rows cannot point to nonexistent or cross-tenant keys/users.

Minimum N+4b:

1. Rewrite synthetic fixture helpers to seed real `users` + `api_keys` before billing claims.
2. Add composite FKs from `billing_ledger_claims(tenant_id,api_key_id)` to `api_keys(tenant_id,id)` and `billing_ledger_claims(tenant_id,user_id)` to `users(tenant_id,id)`.
3. Add equivalent composite FKs to `usage_records`.
4. Add `pool_slot_acquisitions.claim_id -> billing_ledger_claims(id)` because the original migration explicitly deferred it.
5. Add `billing_ledger_archive(tenant_id,api_key_id) -> api_keys(tenant_id,id)` unless Owner declares archives must outlive hard-deleted keys without FK.
6. Do not add `pool_slot_acquisitions -> api_keys`; no `api_key_id` column exists.

(inferred) Composite FKs preserve N+4a's tenant-bound identity model better than single-column FKs. Evidence: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:67-70`, `docs/process/plans/2026-04-30-n4-l0-minimum.md:29-31`.

## 4. Touch List

- `backend/sql/migrations/0009_admin_api_keys_and_ledger_fks.up.sql` — admin audit/admin token tables if approved, supporting indexes, composite ledger FKs, pool claim FK.
- `backend/sql/migrations/0009_admin_api_keys_and_ledger_fks.down.sql` — reverse constraints/tables/indexes for local rollback.
- `backend/sql/queries/admin_api_keys.sql` — create key, validate target user, insert audit, optional list/revoke only if scoped in.
- `backend/sql/queries/admin_auth.sql` — only if D1 chooses DB-backed admin tokens.
- `backend/internal/db/*.sql.go`, `backend/internal/db/models.go`, `backend/internal/db/querier.go` — regenerated sqlc output.
- `backend/internal/admin/api_keys.go` — service: RBAC, key generation, bcrypt, transaction, audit.
- `backend/internal/admin/operator_auth.go` — operator identity/RBAC interface and D1 implementation.
- `backend/internal/admin/keygen.go` — testable `hk_live_`/`hk_test_` generator and prefix derivation.
- `backend/internal/adminhttp/api_keys_handler.go` — strict JSON handler, status mapping, redaction discipline.
- `backend/cmd/gateway/main.go` — mount `/admin/v1/api-keys` and wire deps.
- `docs/openapi/openapi.yaml` and `docs/specs/api-contract.md` — add contract/path/schemas/role extension/audit note.
- Tests: `backend/internal/admin/*_test.go`, `backend/internal/adminhttp/*_test.go`, `backend/internal/billing/claim_gate_integration_test.go`, `backend/internal/billing/settler_integration_test.go`, `backend/internal/pool/db_adapters_integration_test.go`, optional `backend/cmd/gateway/smoke_test.go`.

## 5. Schema Migration Shape

Use `0009_admin_api_keys_and_ledger_fks` because current migrations run through `0008_model_registry` and sqlc reads `sql/migrations`. Evidence: `backend/sqlc.yaml:3-5`.

Up shape:

```sql
BEGIN;
CREATE TABLE IF NOT EXISTS admin_audit_events (
  id bigserial PRIMARY KEY,
  tenant_id bigint REFERENCES tenants(id),
  actor_id text NOT NULL,
  actor_role text NOT NULL CHECK (actor_role IN ('platform_admin','tenant_operator')),
  action text NOT NULL,
  target_type text NOT NULL,
  target_id bigint,
  request_id text,
  reason text,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_admin_audit_events_tenant_time ON admin_audit_events (tenant_id, occurred_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_tenant_id_id ON api_keys (tenant_id, id);
ALTER TABLE billing_ledger_claims ADD CONSTRAINT fk_claims_api_key FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE billing_ledger_claims ADD CONSTRAINT fk_claims_user FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE usage_records ADD CONSTRAINT fk_usage_records_api_key FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE usage_records ADD CONSTRAINT fk_usage_records_user FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE billing_ledger_archive ADD CONSTRAINT fk_archive_api_key FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE pool_slot_acquisitions ADD CONSTRAINT fk_pool_slot_acquisitions_claim FOREIGN KEY (claim_id) REFERENCES billing_ledger_claims (id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE billing_ledger_claims VALIDATE CONSTRAINT fk_claims_api_key;
ALTER TABLE billing_ledger_claims VALIDATE CONSTRAINT fk_claims_user;
ALTER TABLE usage_records VALIDATE CONSTRAINT fk_usage_records_api_key;
ALTER TABLE usage_records VALIDATE CONSTRAINT fk_usage_records_user;
ALTER TABLE billing_ledger_archive VALIDATE CONSTRAINT fk_archive_api_key;
ALTER TABLE pool_slot_acquisitions VALIDATE CONSTRAINT fk_pool_slot_acquisitions_claim;
COMMIT;
```

Down shape: drop the six FK constraints, drop `uq_api_keys_tenant_id_id`, then drop `admin_audit_events`. If D1 adds `admin_tokens`, include it here.

(inferred) Use `ON DELETE RESTRICT`/no-action, not cascade, because billing claims are money-grade/audit source of truth. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:15-18`, `backend/sql/migrations/0002_observability_billing.up.sql:65-65`.

## 6. Admin Endpoint Design

- **Auth/RBAC**: no implemented admin auth was observed. Recommended D1: separate `admin_tokens` bootstrap table because current `users` are end-user identities with no password/role. Evidence: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:13-17`, `backend/internal/db/models.go:628-638`.
- **Tenant scope**: `tenant_operator` can issue only within own tenant; `platform_admin` can issue cross-tenant. Evidence: `docs/specs/api-contract.md:47-50`.
- **Generation**: `crypto/rand` at least 32 random bytes (inferred), header-safe encoding, namespace `hk_live_` or `hk_test_`, prefix `bearer[:16]`, bcrypt default cost. Evidence: `backend/internal/auth/api_key_resolver.go:48-53`, `backend/internal/auth/api_key_resolver_integration_test.go:87-100`.
- **Audit**: dedicated `admin_audit_events`, not `billing_events`; include actor/action/target/reason/redacted payload with prefix only. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:87-90`, `docs/specs/api-contract.md:96-107`.
- **Transaction**: authenticate → strict decode → authorize tenant → validate active target user → generate/hash → insert key + audit in one tx → commit → return plaintext once. Existing `GetUserByID` can seed target-user validation. Evidence: `backend/sql/queries/auth_inbound.sql:41-48`.

## 7. Decision Points

- D1 admin auth: separate `admin_tokens` (Codex recommendation), extend `api_keys`, extend `users`, or dev-only env token.
- D2 prefix/entropy: keep 16-char prefix; use at least 32 random bytes (inferred).
- D3 plaintext display: response body only; defer one-time link.
- D4 idempotency/rate-limit: DB-backed only; otherwise mandatory roadmap, not fake name-based idempotency.
- D5 audit event shape: dedicated admin audit table aligned with OpenAPI audit shape.
- D6 PATCH/DELETE: POST only for N+4b; if revocation required, soft revoke, never hard delete.
- D7 LIST: optional; if included, show prefix/status/timestamps only.
- D8 RBAC: tenant operator same-tenant; platform admin global/cross-tenant.
- DB1 migration: single `0009` with `NOT VALID` + validate if pre-L0 confirmed; split if real data may exist.
- DB2 delete behavior: `RESTRICT`/no-action.
- DB3 composite uniqueness: add `UNIQUE (tenant_id,id)` on `api_keys` if needed for composite FK target.
- DB4 order: fix fixtures first, then add/validate FKs.
- DB5 production status: prompt says pre-L0/no customers; I did not read blueprint, so confirm before validation.

## 8. Test Plan

- Unit: key generator namespace/length/prefix; bcrypt success/failure; response omits hash; redaction; RBAC allow/deny.
- Handler: `401` missing admin credential, `403` wrong tenant, `400` malformed request, `201` success, no plaintext in errors.
- Integration PG: create key through service; authenticate returned plaintext through existing resolver; assert DB stores hash/prefix not plaintext; assert audit row has prefix/actor but no secret; reject disabled/deleted users; reject cross-tenant tenant-operator issuance.
- FK tests: nonexistent `api_key_id`/`user_id` rejected in `billing_ledger_claims` and `usage_records`; nonexistent `claim_id` rejected in `pool_slot_acquisitions`.
- Existing fixture updates: billing claim helper, settler helper cleanup, pool adapter helper; auth resolver tests and gateway smoke are reference patterns for real key seeds.
- Commands: `go test ./internal/admin ./internal/adminhttp ./internal/auth ./cmd/gateway`; `sqlc generate`; migrate DB; `go test -tags integration_pg ./internal/billing ./internal/pool ./internal/auth ./internal/admin`; smoke after route wiring.

## 9. Risk Matrix

| Risk | Severity | Evidence | Mitigation |
| --- | --- | --- | --- |
| Plaintext key leaks into logs/audit | HIGH | CMB-5: `docs/specs/_invariants/cross-module-boundaries.md:131-138` | Never log bodies/secrets; audit prefix only; tests search plaintext. |
| Customer key becomes admin key | HIGH | Users no role/password: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:13-17` | Separate admin credential unless Owner approves extension. |
| FK migration breaks tests | HIGH | Synthetic IDs: `backend/internal/billing/claim_gate_integration_test.go:35-53` | Rewrite fixtures before validation. |
| Cascading deletes erase billing evidence | HIGH | Claims money-grade: `backend/sql/migrations/0002_observability_billing.up.sql:15-18` | RESTRICT/no-action; revoke/soft-delete. |
| Audit table mismatch | MED | Admin audit required: `docs/specs/api-contract.md:96-107` | Dedicated table matching OpenAPI shape. |
| sqlc drift | MED | Generation paths: `backend/sqlc.yaml:1-15` | Regenerate and compile. |
| Prefix/fanout regression | MED | Prefix/fanout constants: `backend/internal/auth/api_key_resolver.go:48-58` | Share helper/constant; keep LIMIT 5. |
| Scope creep | MED | No key route today: `backend/cmd/gateway/main.go:182-203` | POST first; defer list/revoke unless approved. |

## 10. CMB Compliance

- CMB-1: no Router credential reads; admin code must not enter `internal/router`. Evidence: `docs/specs/_invariants/cross-module-boundaries.md:101-105`.
- CMB-5: plaintext only in one issuance response; never logs/audit/DB/errors/list. Evidence: `docs/specs/_invariants/cross-module-boundaries.md:131-138`.
- CMB-7: hot-path Auth remains read-only; admin issuance is a separate admin write path; Ledger/Pool write ownership unchanged. Evidence: `docs/specs/_invariants/cross-module-boundaries.md:147-158`, `backend/internal/auth/api_key_resolver.go:16-17`.

## 11. Sequencing, Rollback, Effort

Sequencing recommendation: split into two PRs if possible. PR1: fixture cleanup + FK migration + FK tests. PR2: admin auth + issuance + audit + OpenAPI/docs. If one PR is required, use two commits in that order. Do not implement route before D1 admin auth is approved.

Rollback: disable route via mount guard/feature flag; revoke bad keys with `status='revoked'`, `revoked_at`, `revoked_reason`; do not hard delete referenced keys. Drop N+4b constraints in down migration; export audit rows before dropping audit table in real environments. Evidence for revocation fields: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:58-66`.

Estimated effort: FK cleanup/migration 0.5–1 day; D1 admin auth 0.5 day after Owner decision; issuance service/handler/tests/sqlc/OpenAPI 1–1.5 days; integration/smoke stabilization 0.5–1 day. Total 2.5–4 engineer-days.

## 12. Pre-execution Checklist

1. Reconcile Claude/Codex plans and approve synthesized plan.
2. Decide D1 admin auth and D5 audit shape.
3. Decide POST-only vs LIST/PATCH/DELETE inclusion.
4. Confirm production data status; run orphan preflight if any real data exists.
5. Patch fixtures to real auth rows first.
6. Add migration/queries and regenerate sqlc.
7. Implement admin service/handler behind authenticated route.
8. Run unit, integration_pg, and smoke checks.
9. Stage changes and run required Codex review before commit.

## 13. Open Questions

1. Accept separate `admin_tokens`, or wait for Account Hub RBAC?
2. Need minimal `/admin/v1/users` before key issuance?
3. Add `display_name` to `api_keys`, or use existing `name` only? Evidence: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:51-66`.
4. FK `billing_ledger_archive` to `api_keys`, or allow archive to outlive hard-deleted keys? Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:75-85`.
5. Include `pool_slot_acquisitions.claim_id` FK now? Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:177-178`.
6. Support issuance idempotency in N+4b? If yes, where is state stored?
7. New OpenAPI `admin-api-keys` tag or fold into `admin-accounts`?
8. Keep `last_used_at` unset? This plan keeps it unchanged.

## 14. Chinese Owner Summary

本计划只做 N+4b 独立规划，没有执行实现，也没有读取 Claude 并行草稿。真实观察：N+4a 已落地 `users`/`api_keys`、bcrypt、16 位 `key_prefix`、`hk_live_`/`hk_test_` 和只读解析器；当前没有 `/admin/v1/api-keys`；billing/pool 测试仍有 `tenantID*100` 合成 ID；`billing_ledger_claims`、`usage_records`、`billing_ledger_archive` 缺少到 `api_keys/users` 的 FK，`pool_slot_acquisitions.claim_id` 也仍是延迟 FK。合理推断：先修 fixture+FK，再做 admin issuance；admin 写路径放 `internal/admin`/`internal/adminhttp`，不要污染 `internal/auth` 热路径；审计用专用 admin audit 表。无功能缩水；clean-room 风险低（未读外部 reference source）；主要安全风险是 plaintext key 泄漏和 RBAC 错配，D1/D5 需要 Owner 先确认。

Source files read: docs/process/plans/2026-04-30-n4-l0-minimum.md; backend/sql/migrations/0007_l0_inbound_auth.up.sql; backend/internal/auth/api_key_resolver.go; backend/internal/auth/api_key_resolver_integration_test.go; backend/sql/migrations/0002_observability_billing.up.sql; backend/sql/migrations/0001_pool_routing.up.sql; backend/internal/billing/claim_gate_integration_test.go; backend/internal/billing/settler_integration_test.go; backend/internal/pool/db_adapters_integration_test.go; backend/cmd/gateway/main.go; backend/cmd/gateway/smoke_test.go; docs/specs/_invariants/cross-module-boundaries.md; docs/RULES.md; backend/sql/queries/auth_inbound.sql; backend/sql/queries/auth_audit.sql; backend/sql/queries/auth_credentials.sql; backend/sql/queries/auth_storm.sql; backend/sqlc.yaml; docs/specs/api-contract.md; docs/openapi/openapi.yaml
Lane: specifier
Agent: Codex
UTC timestamp: 2026-05-01T03:17:49Z

