# 2026-05-16 F-CH-002 Channel Health Implementation - Codex

| Owner directive | "你是 HUAKAI 项目 codex executor lane, 任务 = F-CH-002 channel health auto-disable 真代码实施 (按 commit 06f0ff2 spec)." |
| --- | --- |
| Lane | implementer; HUAKAI-owned docs and local code only; no reference-project source read |
| Scope | In: migration `0022_channel_health_state`, new `backend/internal/channelhealth/` package, sqlc queries/codegen if needed, pool/PASR eligibility predicate integration, admin override handler, channel-health audit/alert hooks, AT-CH-002-001..013 tests. Out: `LICENSE`, billing ledger, quota enforcement, auth core secret mutation, Rust `core_gateway`, anti-detection/TLS/device/browser fingerprint work, reference-project source reading. |
| Success criteria | `channel_health_state` is tenant-scoped and credential-version scoped; policy/state/ramp/classifier logic covers the five required dimensions; selectors skip `cooling_down`, `disabled`, and `manual_paused`; manual pause/resume/force-active are audited; audit payloads never include raw upstream text or secrets; AT-CH-002-001..013 have executable coverage; requested Go tests pass or blockers are recorded truthfully. |
| Time estimate | 3-5 engineering days. Day 1 schema/store/core types; Day 2 policy/cooldown/ramp/classifier; Day 3 pool/admin/audit integration; Day 4 AT integration tests and race runs; Day 5 hardening/review fixes if needed. |
| Blast radius | Backend database schema, generated sqlc DB API, pool selection hot path, admin HTTP surface, audit/alert event surfaces, and test fixtures. A bad implementation could misroute tenant traffic, silently route to unhealthy channels, leak upstream error text into audit/admin output, or break migrations/codegen. |
| Failure modes | Over-disabling scarce accounts; under-disabling banned/revoked channels; cross-tenant or cross-version health contamination; flapping during ramp; hidden all-unhealthy pool state; leaking raw upstream body/token-shaped strings; touching forbidden billing/quota/auth/Rust areas; migration numbering conflict because current tree only contains migrations through `0019`; implementing from Draft spec without synthesized plan approval. Mitigation: add tenant/version constraints, sample floors, safe reason-class enums, explicit all-unhealthy errors, redaction tests, minimal predicate-only pool changes, and stop for Owner decision on high-risk conflicts. |
| Decision points | Owner/PM must reconcile this Codex plan with an independent Claude plan before implementation per AGENTS.md parallel-plan rule; Owner must confirm whether to create `0022` with gaps `0020/0021` absent in this branch or use next contiguous migration; Owner must confirm whether commit `06f0ff2` Draft F-CH-002 is approved as implementation input despite `Status=Draft`; Owner must confirm admin route prefix if `/v1/admin/channels/{id}/...` should coexist with existing `/admin/v1/...` handlers. |
| Clean-room guard | Use only `docs/specs/channel-health-auto-disable.md`, `docs/decompositions/_cross-cutting/channel-health.md`, local HUAKAI code, and acceptance matrix. Do not read Sub2API/New-API/One-API/LiteLLM/Portkey/Helicone/All-API-Hub/Envoy source. Preserve feature outcome with independently designed Go code and schema. |

## Pre-Execution Checklist

1. Confirm synthesized plan exists after Claude/Codex cross-discussion, or receive explicit Owner override to proceed from this plan.
2. Re-check `git status --short`; preserve existing unrelated modified file `docs/plans/2026-05-15-f-cred-001-acquisition-claude.md`.
3. Verify migration numbering decision (`0022` as requested versus contiguous next number).
4. Read current `account_credentials`, `provider_accounts`, audit ledger/admin audit, and pool selector code before editing.
5. Confirm sqlc availability; if unavailable, either install is not allowed under current no-approval policy or hand-written package store must avoid generated DB code churn.
6. Define safe local enums for health state, signal class, confidence, and event type; never store raw upstream bodies.
7. Keep all selectors tenant-first: every lookup key includes `tenant_id` and either `account_credential_id + credential_version` or provider account/channel mapping.
8. Prepare tests before broad integration: pure unit tests for policy/ramp/classifier/store JSON aggregation, then pool/admin integration tests.

## Concrete Execution Order

1. Add migration:
   - Create `channel_health_state_enum` or equivalent CHECK-constrained text state.
   - Create `channel_health_state` with requested columns: `id`, `tenant_id`, `vendor`, `account_credential_id`, `credential_version`, `state`, `state_entered_at`, `cooldown_until`, `ramp_stage_pct`, `sample_window`, `policy_version`, `last_signal_class`, `last_signal_at`, `manual_pause_reason`, and `ramp_failure_count`.
   - Add `(tenant_id, vendor, account_credential_id, credential_version)` index and `UNIQUE (account_credential_id, credential_version)`.
   - Prefer additional tenant-safe FK/index checks only if they do not require high-risk schema rewrites outside this migration.
2. Add `backend/internal/channelhealth/types.go`:
   - `HealthState`, `Policy`, `Signal`, `SignalClass`, `WindowSummary`, `Transition`, `AuditEvent`, `Alert`.
   - Include deterministic clock injection for tests.
3. Add `store.go`:
   - Postgres CRUD interface and implementation using pgx/sqlc-compatible narrow interfaces.
   - Sliding-window aggregation helper over JSONB `sample_window`, with tenant/vendor/credential-version keys and sample-floor metadata.
   - In-memory test store if it keeps tests fast and avoids DB-only coupling.
4. Add `policy.go`, `cooldown.go`, and `ramp.go`:
   - Evaluate `error_rate`, `latency_p99`, `rate_limit_hit_rate`, `upstream_5xx_rate`, and `ban_signal`.
   - Enforce `active -> degraded -> cooling_down -> ramping -> active`, plus `disabled` and `manual_paused` restrictions.
   - Implement 1/10/50/100 ramp stages and rollback/backoff.
5. Add `signal_classifier.go`:
   - Accept structured response metadata/status/error-code inputs.
   - Return safe classes such as `account_suspended`, `token_revoked`, `credential_revoked`, `account_disabled`, `rate_limit_429`, `latency_p99_spike`, `upstream_5xx`, and `unknown`.
   - Drop raw upstream text after classification; tests inject token-shaped strings and assert they do not persist.
6. Add `failover.go` and pool integration:
   - Expose an `EligibilityChecker` and `Gate` adapter usable from `pool.GateChain.Health`.
   - For PASR, add the same predicate in candidate filtering so PASR does not bypass channel health.
   - Do not rewrite PASR scoring or routing logic; only add eligibility.
7. Add audit and alert integration:
   - Emit allowlisted events: `channel_health_degraded`, `channel_disabled`, `channel_recovered`, `channel_ramp_started`, `channel_ramp_rolled_back`, `channel_manual_override`.
   - Use existing audit ledger/admin audit patterns where possible; if a durable alert table does not exist, introduce a narrow in-package alert sink interface and test it without adding a new alert schema unless Owner confirms.
8. Add admin override handler:
   - `backend/internal/gatewayhttp/channel_health_admin_handler.go`.
   - Implement POST pause/resume/force-active with tenant_id and reason validation, admin auth, store transition call, audit emission, and safe JSON responses.
   - Confirm route mounting location before wiring into server bootstrap if existing route tree is not obvious.
9. Add tests:
   - Pure `go test ./internal/channelhealth -race -count=1` coverage for policy, cooldown, ramp, classifier, store aggregation, audit payload allowlist.
   - Pool tests for AT-CH-002-006 and AT-CH-002-012 predicate/failover behavior.
   - Admin handler tests for AT-CH-002-010 and audit failures.
   - Integration PG tests for migration/store if `HUAKAI_DATABASE_URL` is available; otherwise they must skip honestly.
10. Run verification:
    - `go test ./internal/channelhealth -race -count=1`
    - Targeted pool/admin tests.
    - Integration tests for AT-CH-002-001..013 where environment permits.
    - `git status --short` and `git diff --stat`.
    - Stage and run `codex exec review --uncommitted --full-auto` before any commit, per per-commit review discipline.

## AT-CH-002 Mapping

| AT ID | Planned coverage |
| --- | --- |
| AT-CH-002-001 | Store create/default active test plus credential-version key test. |
| AT-CH-002-002 | Error-rate policy transition to cooldown with audit events. |
| AT-CH-002-003 | Upstream 5xx degrade-first then repeated breach cooldown; local 5xx ignored. |
| AT-CH-002-004 | Rate-limit hit-rate cooldown uses reset timestamp fallback/default policy. |
| AT-CH-002-005 | Ban signal disables 24-72h, emits alert, stores safe reason only. |
| AT-CH-002-006 | Pool/PASR health gate skips cooled channel and picks healthy alternate. |
| AT-CH-002-007 | Cooldown expiry starts 1/10/50/100 ramp and recovers. |
| AT-CH-002-008 | Ramp failure rolls back to cooldown and increments failure count. |
| AT-CH-002-009 | Vendor and credential-version isolation. |
| AT-CH-002-010 | Admin pause/resume/force-active authorization and audit. |
| AT-CH-002-011 | Audit payload allowlist and secret/raw-text negative assertions. |
| AT-CH-002-012 | Sample floor prevents single-failure cooldown; all-unhealthy pool surfaces explicit error. |
| AT-CH-002-013 | Latency P99 degradation/cooldown/recovery without permanent disable. |

## Assumptions

- The Owner message is a start signal for planning, but implementation still waits on the project-mandated synthesized plan unless explicitly overridden.
- `backend/internal/auditledger` is F-TRUST-facing, while admin override operations may also need `admin_audit_events`; final code should avoid inventing a second audit truth unless existing abstractions are insufficient.
- Existing `provider_accounts.health_state` remains a legacy pool gate; F-CH-002 should add a predicate layer rather than replacing that column in this slice.
- If sqlc is unavailable in the sandbox, generated files cannot be refreshed safely; implementation should either avoid sqlc changes or record the blocker.

## Risk Register For This Work Unit

- Medium: Adding `channel_health_state` schema is a DB structural change. Mitigation: additive migration, down migration, no destructive changes to existing tables.
- Medium: Pool hot path now depends on channel-health reads. Mitigation: predicate interface, cache or store adapter with fail-closed/fail-open policy explicit in tests.
- Medium: Admin force-active can route risky channels. Mitigation: platform-admin role, required reason, audit plus alert.
- Low: New package APIs may evolve after spec release. Mitigation: keep package internal and use small interfaces.
- Clean-room: Low if implementation consumes only HUAKAI docs and local code; high if any reference source is read during implementation. Mitigation: do not clone/open reference projects in this lane.

Source files read: docs/RULES.md; docs/specs/channel-health-auto-disable.md; docs/decompositions/_cross-cutting/channel-health.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; backend/sqlc.yaml; backend/go.mod; backend/Makefile; backend/sql/migrations/* listing and targeted migration snippets; backend/sql/queries/pool_accounts.sql; backend/internal/pool/{pool.go,selector.go,gates.go,pasr_selector.go,db_account_source.go}; backend/internal/gatewayhttp/admin_pool_accounts_handler.go; backend/internal/auditledger/{types.go,ledger.go,postgres.go}; .agents/skills/clean-room-license-guard/SKILL.md
Lane: implementer
Agent: Codex GPT-5 executor lane
UTC timestamp: 2026-05-16T07:44:35Z
