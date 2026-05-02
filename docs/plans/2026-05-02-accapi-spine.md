# Account-to-API spine 0011 — Claude/Codex synthesis

Date: 2026-05-02
Lane: synthesis (post CLAUDE.md #10 parallel-draft).
Inputs:
- `docs/plans/2026-05-02-accapi-spine-claude.md` (commit `ced523f`, 396 lines)
- `docs/plans/2026-05-02-accapi-spine-codex.md` (commit `1b939df`, 670 lines)
- Source-of-truth audit: `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md` (commit `0a89450`)

This document is the cross-discussion required by CLAUDE.md #10 strict parallel rule. After Owner approval, the synthesized plan replaces both individual drafts as the implementation contract for migration 0011.

## 1. Where Claude and Codex agree (high-confidence shared baseline)

Both plans converge on:

| Topic | Shared decision |
| --- | --- |
| Migration shape | Single additive migration `0011_accapi_spine.up.sql` + `.down.sql`, BEGIN/COMMIT, additive only |
| `api_key_bindings` schema | Explicit per-target columns (pool_group_id / provider_account_id / tenant_default_token); CHECK enforces exactly-one-non-NULL per binding_kind; tenant_default uses literal `'default'` token (no NULL targets persisted) |
| Cross-tenant FKs | Composite `(tenant_id, target_id) → target(tenant_id, id)` for api_keys + pool_groups + provider_accounts; mirrors N+4b1 pattern |
| Per-kind partial unique indexes | 3 indexes, one per binding_kind, scoped `WHERE binding_kind = X AND deleted_at IS NULL` (Codex pass-12 P2 NULL-safe fix) |
| Composite uq prerequisites | Pre-create `uq_provider_accounts_tenant_id_id` (defensively); `uq_pool_groups_tenant_id_id` already exists from 0008 |
| `usage_records` additions | Add `pool_group_id`, `binding_id`, `credential_kind`, `credential_version` as NULL-tolerant columns + composite FKs; new traffic populates, historical rows stay NULL |
| `request_attempts` table | Append-only per-attempt audit; UNIQUE `(tenant_id, request_id, attempt_number)`; composite cross-tenant FKs to binding/account/pool |
| Out-of-scope for 0011 | Credential injector implementation, error classifier impl, retry/fallback executor, capability snapshot table, full trace endpoint, payment/billing/auth/secrets — all deferred to Slice 5+ |
| 9 F-ACCAPI rows in 03 matrix | CORE / BIND / LEASE / CAP-SNAP / CRED-INJECT / ERR-CLASSIFY / ATTEMPT / STATE / TRACE (PROTO uses existing F-PROTO-002, no new row) |
| Admin endpoint scope | GET + POST `/admin/v1/api-keys/{id}/bindings`. Recovery/disable route deferred unless Owner expands |
| Test discipline | Integration PG tests for SQL constraints + admin handler unit/integration; YAML parse for OpenAPI |
| Rollback posture | App-rollback first, schema-down only after data export + Owner confirmation |
| Codex review before commit | Mandatory `codex exec review --uncommitted --full-auto` per CLAUDE.md #8 |

## 2. Codex additions Claude did not specify (recommend ADOPT)

These are concrete improvements Codex's deeper specifier work surfaced. Claude's plan should absorb them.

### 2.1 Additional CHECK constraints
- `priority >= 0` on api_key_bindings (Codex)
- `request_id <> ''` on request_attempts (Codex)
- `attempt_number >= 0` on request_attempts (Codex)
- `credential_kind <> ''` + `credential_version >= 0` on request_attempts (Codex)
- `upstream_status_code BETWEEN 100 AND 599` on request_attempts (Codex)
- `retry_after_ms >= 0` on request_attempts (Codex)
- `finished_at IS NULL OR finished_at >= started_at` on request_attempts (Codex `request_attempts_time_check`)
- `usage_records_credential_pair_check`: kind + version both NULL or both NOT NULL together (Codex)

**Recommendation**: ADOPT all. Cheap correctness layer; don't make caller pass illegal values silently.

### 2.2 Additional indexes
- `idx_api_key_bindings_pool_target (tenant_id, pool_group_id, priority) WHERE binding_kind='pool_group' AND deleted_at IS NULL` — reverse lookup "which keys point at this pool" for admin
- `idx_api_key_bindings_account_target (tenant_id, provider_account_id, priority) WHERE binding_kind='provider_account' AND deleted_at IS NULL` — same for accounts
- `idx_request_attempts_unfinished (tenant_id, started_at) WHERE finished_at IS NULL` — stuck-attempt detection
- `idx_usage_records_account_credential_settled (tenant_id, provider_account_id, credential_kind, credential_version, settled_at DESC) WHERE credential_kind IS NOT NULL` — credential forensics

**Recommendation**: ADOPT all. Cheap; supports operator queries we will need.

### 2.3 NOT VALID + VALIDATE pattern for usage_records FKs
Codex applies the N+4b1 pattern to the new usage_records FKs (since this is an existing table with rows). Claude's plan didn't specify — implicitly let Postgres validate immediately, which is fine on dev DB but unsafe on production-sized data.

**Recommendation**: ADOPT. Use `ALTER TABLE ... ADD CONSTRAINT ... NOT VALID; ALTER TABLE ... VALIDATE CONSTRAINT ...` pattern.

### 2.4 credential_version source pinned to F-AUTH-005's `provider_accounts.token_version`
Codex correctly notes credential_version should NOT be a new field — it should be the existing `token_version` field from F-AUTH-005 spec. Avoids a parallel versioning scheme.

**Recommendation**: ADOPT. Verify F-AUTH-005 spec actually shipped this field; if not, raise to Owner.

### 2.5 request_attempts.pool_group_id derivation rule
Codex notes: when binding_kind = `provider_account` (no pool group in binding), derive pool_group_id at write time via `provider_accounts.channel_id → channels.pool_group_id`. Otherwise the column is unset.

**Recommendation**: ADOPT. Document this in audit §5.6 as the resolution rule.

### 2.6 Production-sized index creation note
Codex flags: BEGIN/COMMIT migration is fine for dev; production should split into `CREATE INDEX CONCURRENTLY` outside transactions.

**Recommendation**: ADOPT as comment in migration; not a code change for dev migration but operational guidance.

### 2.7 Production rollback warning
Codex adds: post-traffic rollback destroys binding contracts and request_attempts forensic evidence. App-rollback first.

**Recommendation**: ADOPT. Add to migration header comment.

## 3. Claude additions Codex did not specify (recommend ADOPT)

### 3.1 Three-piece adapter design (PROTO + CRED-INJECT + ERR-CLASSIFY)
Claude's plan spelled out the orthogonal interface design from audit §6, including the registry resolution `(provider, kind) → (*, kind)` fallback rule. Codex's plan calls these "deferred to Slice 5" without the design contract.

**Recommendation**: ADOPT. The interface shape needs to be in the synthesized plan even though impl is Slice 5, because the sqlc query design depends on knowing what fields the executor will need (e.g. credential_kind enum values).

### 3.2 LOC + time estimate
Claude estimates ~600 hand-written + ~6-8 hours focused work. Codex estimates 6-9 agent hours, 1-2 wall-clock days. Both reasonable; merge.

**Recommendation**: ADOPT Claude's LOC breakdown. Use Codex's wall-clock calibration.

## 4. Real conflicts (Owner must decide)

### 4.1 Priority uniqueness per active key
- **Codex**: `UNIQUE (tenant_id, api_key_id, priority) WHERE deleted_at IS NULL` — ensures deterministic fallback order; rejects duplicate priorities at INSERT.
- **Claude**: no priority uniqueness; rely on `ORDER BY priority, id` everywhere.

**Codex's argument**: deterministic order; admin can't accidentally create two bindings with priority=100 and have non-deterministic resolution.

**Claude's argument** (implicit): simplicity; lets admin "swap" priorities by editing without delete-then-insert dance.

**Recommendation**: ADOPT Codex's uniqueness. Reasoning: deterministic > flexible-but-confusing. Operator can update priority by `UPDATE ... SET priority = priority + 1` for a swap; admin handler validates and surfaces 409 Conflict on dup.

### 4.2 Disabled bindings blocking duplicate target
- **Codex Q3**: should `enabled=false` rows still occupy the unique slot, or release it?
- **Claude**: doesn't address.

**Recommendation**: KEEP CURRENT (disabled rows keep occupying slot until soft-delete). Reasoning: explicit "disabled but reserving slot" is clearer than "disabled = doesn't count" for audit trails. Operator should soft-delete to free.

### 4.3 Provider-account binding to disabled/unhealthy account
- **Codex Q5**: allow pre-staging (bind to account that's not yet schedulable)?
- **Claude**: doesn't explicitly address.

**Recommendation**: ALLOW. Reasoning: binding is a contract, scheduler is the gate. Admin should be able to set up bindings before activating accounts (e.g. during staged rollout). Document in audit §5.1.

### 4.4 request_attempts.provider_account_id nullability
- **Codex Q6**: allow NULL for local-failure attempts (validation rejected before account selection)?
- **Codex's recommendation**: NO — those go to admin/audit events instead.
- **Claude**: not explicitly addressed; schema marks provider_account_id NOT NULL.

**Recommendation**: ADOPT Codex's NOT NULL stance. Local rejects don't represent upstream traffic; they belong in `admin_audit_events` not `request_attempts`. Keeps the table semantically clean.

### 4.5 Admin disable/delete route in 0011
- **Codex Q4 + Claude both**: keep GET/POST only; defer disable/delete.

**Recommendation**: ADOPT (already aligned). Disable comes with Slice 5's binding-state-management workflow.

### 4.6 `account_state_view` in 0011
- **Codex Q1**: defer to next slice.
- **Claude**: explicitly defers (audit §9 step 7).

**Recommendation**: DEFER. STATE-001 lands with ERR-CLASSIFY-001 in Slice 5 because state transitions need classifier signals.

### 4.7 OpenAPI `x-huakai-spec-source` citation
- **Codex Q8**: should it cite a new spec doc, a DR, or both?
- **Claude**: Track 1 step 4 says open DR before migration; doesn't address spec citation.

**Recommendation**: BOTH. Open `DR-009-account-to-api-mainline` (sets the architectural decision) AND start `docs/specs/account-to-api-spine.md` (the implementer-facing released contract). OpenAPI cites the spec; spec cites the DR.

### 4.8 binding_id ever NOT NULL on usage_records
- **Codex Q9**: future backfill or stay nullable forever?

**Recommendation**: STAY NULLABLE in 0011. Decide after Slice 5 ships and we see whether all new traffic populates it cleanly. A future migration can `ALTER ... SET NOT NULL` after backfill if the no-NULL-on-new-traffic invariant holds.

### 4.9 CLIProxyAPI Phase 2 mining blocking 0011
- **Codex Q7**: blocker or parallel?

**Recommendation**: PARALLEL. Spine schema is independent of CLIProxyAPI feedback. CLIProxyAPI specifier work (see `reference_deep_dive/2026-05-02/cliproxy-api/account-to-api-deep-dive.md`) can refine Slice 5 executor design — that's where its insights matter, not 0011 schema.

## 5. Gaps both plans share (need Owner direction)

These are not in either plan but must be settled before implementation:

1. **DR number assignment**: `DR-009` proposed but not confirmed.
2. **Spec doc creation**: `docs/specs/account-to-api-spine.md` — write before or after migration? Recommend before, so spec citation in OpenAPI is grounded.
3. **Codex parallel-plan workflow validation**: this is the first time we're synthesizing two plans. Owner's review of the synthesis quality is itself a CLAUDE.md #10 evidence point.
4. **Migration test infrastructure**: currently smoke + integration_pg. Do we add a dedicated `-tags=migration_test` for schema-only migration tests, or fold into existing integration_pg? Codex implies fold-in; Claude doesn't specify.
5. **Smoke seed data update**: 0011 keeps usage_records additions nullable, so existing smoke seed should pass. Both plans agree no rewrite needed. Verify by running smoke after migration.

## 6. Synthesized scope decision matrix

| Concern | Decision (synthesized) |
| --- | --- |
| Migration file count | 1 (`0011_accapi_spine.up.sql` + down) |
| Tables created | 2 (`api_key_bindings`, `request_attempts`) |
| Tables altered | 1 (`usage_records` adds 4 columns + 2 FKs + 1 CHECK + 3 indexes) |
| Indexes added | 7 on `api_key_bindings` (3 partial-unique by kind + 1 priority unique + 3 lookup) + 2 on `usage_records` + 5 on `request_attempts` |
| CHECK constraints | 8+ (8 from Codex §2.1 + binding_target_consistency from both) |
| Composite FKs | 6 total (3 on api_key_bindings + 2 on usage_records + 3 on request_attempts) |
| Action enum extension on `admin_audit_events` | Yes — add `'bind_api_key'`, `'unbind_api_key'`, `'list_api_key_bindings'` (extend N+4b2 enum) |
| sqlc queries | ~10 across 2 files (`admin_api_key_bindings.sql` + `request_attempts.sql`) |
| Admin endpoints | 2 in 0011 (GET + POST `/admin/v1/api-keys/{id}/bindings`). Disable/delete deferred. |
| OpenAPI schemas added | 4-5 (APIKeyBinding, APIKeyBindingCreate, APIKeyBindingList, APIKeyBindingKind enum) |
| Test cases | 13 SQL constraint (Codex) + 7 admin handler (Claude) + 7 service (Claude) = ~27 cases |

## 7. Synthesized 16-step execution order

Refines Codex's 15-step list with Claude's pre-implementation discipline.

1. **Owner approves this synthesized plan** (CLAUDE.md #10 final gate)
2. **Confirm DR-009 number** is free; reserve it
3. **Open `DR-009-account-to-api-mainline.md`** with this audit + this plan as inputs
4. **Open `docs/specs/account-to-api-spine.md`** as the implementer-facing contract
5. **Update `docs/03_FEATURE_PARITY_MATRIX.md`** with 9 F-ACCAPI rows
6. **Update `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`** with spine section
7. **Write `0011_accapi_spine.up.sql` + `.down.sql`**
8. **Write `backend/sql/queries/admin_api_key_bindings.sql` + `request_attempts.sql`**
9. **Run `make generate`** from `backend/`
10. **Implement `internal/admin/binding.go`** binding service
11. **Implement `internal/adminhttp/api_key_bindings_handler.go`** + route wiring in `cmd/gateway/main.go`
12. **Update `docs/openapi/openapi.yaml`** with paths + schemas; cite the spec
13. **Add migration/SQL constraint tests** (~13 cases) under `-tags=integration_pg`
14. **Add admin handler/service tests** (~14 cases)
15. **Run focused tests** then `go test -tags=integration_pg ./...` then smoke
16. **Stage intended files only**; run `codex exec review --uncommitted --full-auto`; address HIGH findings; commit

## 8. Owner sign-off needed (8 decisions)

Before implementation starts, Owner confirms:

- [ ] DR number = 009
- [ ] Adopt Codex 8 CHECK constraints + 4 indexes (§2.1, §2.2)
- [ ] Adopt NOT VALID + VALIDATE pattern for usage_records FK adds (§2.3)
- [ ] credential_version source = `provider_accounts.token_version` (§2.4)
- [ ] **Priority uniqueness per active key** (§4.1 — Codex's stronger constraint)
- [ ] Provider-account binding allowed for disabled accounts (§4.3 — pre-staging)
- [ ] `request_attempts.provider_account_id` NOT NULL (§4.4 — Codex's stance)
- [ ] OpenAPI cites BOTH DR-009 + spec doc (§4.7)

After Owner sign-off: execute steps 3-16 above. Slice 5 begins after merge.

## 9. Cross-discussion summary for Owner

**Both plans are high-quality and converge on the core schema**. Codex's plan is more detailed (670 lines vs 396) because it included full rationale per index + 9 explicit Open Questions; Claude's plan is more architectural (3-piece adapter design contract) and includes LOC estimates.

**No fundamental disagreement on scope or schema shape.** All 9 of Codex's Open Questions resolved here either by adoption (1, 5, 8) or by recommendation (2, 3, 4, 6, 7, 9).

**4 Codex contributions ADOPT into final plan**: 8 CHECK constraints, 4 indexes, NOT VALID+VALIDATE pattern, token_version pinning.

**1 Claude contribution ADOPT**: 3-piece adapter interface design contract.

**1 real Owner-decision conflict**: Priority uniqueness per active key (§4.1). Recommend Codex's stronger constraint.

**No clean-room contamination**: both plans use HUAKAI domain terms only, no non-MIT reference source read in plan-writing sessions.

This synthesis IS the implementation contract once Owner signs off. Both individual plan files remain as audit trail of the parallel-draft process per CLAUDE.md #10.
