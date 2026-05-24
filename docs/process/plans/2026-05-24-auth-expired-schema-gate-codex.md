# 2026-05-24 auth_expired outcome schema gate — Codex lane plan

> **For agentic workers:** this is an implementation plan only. Do not execute it until Owner approves the synthesized plan after Claude/Codex cross-discussion.
> **Hard stop:** do not `git add`, `git commit`, or `git push` while executing this plan unless a later Owner directive explicitly says so.

| Field | Value |
|---|---|
| Owner directive | `[OWNER AUTHORIZED 2026-05-24T11:10Z workspace-write — auth_expired outcome schema gate plan lane]` |
| Lane | Codex independent plan lane |
| Clean-room lane | specifier |
| Output path | `docs/process/plans/2026-05-24-auth-expired-schema-gate-codex.md` |
| Scope class | HIGH ceremony: DB schema gate + credential refresh audit + trust ledger + dispatch health gate |
| Execution status | Plan only. No implementation performed. |
| Commit policy | Absolutely no `git add` / `git commit` / `git push` in this lane. |

---

## §0 Planning Gate

| Required AGENTS.md field | Plan answer |
|---|---|
| Scope | In: schema gate for refresh audit outcomes; optional TR-D1 provider account cooldown schema reconciliation; sqlc regeneration; credentialworker outcome propagation; Copilot and Anthropic refresh failure classification; true PG audit+ledger integration tests; dispatcher health-state validation. Out: auth core rewrite, billing ledger changes, quota enforcement rewrite, production deploy, destructive migration, dependency addition, LICENSE. |
| Success criteria | The selected schema option accepts the selected logical refresh outcomes; missing schema makes a real PG test fail red; Copilot 401 and Anthropic invalid-grant paths write the selected auth-expired outcome through `oauth_refresh_audit_events` and `audit_ledger_entries` in the same transaction; dispatcher does not select unhealthy/cooling accounts; all tests include mutation-discriminating fixtures. |
| Time estimate | Plan: 1-1.5h. Implementation after Owner approval: 0.5d for migration/sqlc; 0.5d for outcome propagation; 0.5d for true PG integration; 0.25d for dispatcher health validation; 0.25d for review/fixes. |
| Blast radius | `oauth_refresh_audit_events` inserts; `provider_accounts.last_refresh_outcome` if selected; `provider_accounts.health_state` / cooldown columns if selected; credentialworker refresh audit path; Copilot refresh; Anthropic refresh; sqlc generated DB packages; pool account selection query. |
| Failure modes | CHECK constraint still rejects new outcome; down migration silently loses outcome evidence; provider error classification collapses to `permanent_disable`; audit insert commits without ledger row; ledger row commits without audit row; cooldown column drift splits truth between `health_state_until`, `cooldown_until`, and `channel_health_state.cooldown_until`; tests pass with non-discriminating fixtures. |
| Mitigations | Named/explicit constraint updates; down migration refuses downgrade while new outcome rows exist; shared outcome carrier contract; true PG transaction tests with rejection trigger; mutation self-check per outcome; explicit D decision before physical enum/type naming; dispatcher test with expired and active cooldown rows. |
| Decision points | D-A physical outcome schema shape; D-B logical outcome set and physical spelling; D-C provider account cooldown storage; D-D whether health-state schema joins this commit or splits; D-E default cooldown policy values; D-F whether `last_refresh_outcome` mirrors audit outcomes or remains coarse. |
| Pre-execution checklist | Confirm synthesized plan exists; confirm no newer migration than 0054 landed; confirm `HUAKAI_DATABASE_URL` target is disposable/dev; confirm sqlc installed; confirm Owner selected D-A..D-F; confirm no forbidden Claude lane plan is used as implementation evidence. |

Protocol note:

- A broad `rg` command accidentally matched the forbidden Claude lane file path.
- This Codex plan does not use that file as evidence, input, or source of truth.
- Evidence below is anchored to HUAKAI code/spec files, the 2026-05-24 ref-anchor ledger, and reference source regions read from `~/refs-latest`.

---

## §1 Goal

1. Close the P-A R-A3 schema gate left by Copilot refresh 401 classification.
2. Ensure the refresh audit DB schema can accept the selected auth-expired outcome.
3. Plan for the logical outcomes requested by Owner:
   - `auth_expired`
   - `revoked`
   - `quota_exhausted`
   - `risk_control_triggered`
4. Do not assume those exact strings are the final physical DB spellings.
5. Surface the physical spelling choice in §7 D-A/D-B.
6. Keep current outcomes valid:
   - `cache_hit`
   - `refresh_lock_held`
   - `refresh_succeeded`
   - `refresh_token_rotated`
   - `db_version_conflict`
   - `invalid_grant_race_recovered`
   - `storm_budget_exhausted`
   - `cas_lost`
   - `token_malformed`
   - `oauth_401_force_refresh`
   - `permanent_disable`
   - `mimicry_applied`
7. Add or reconcile TR-D1 provider-account health persistence.
8. Current reality: `provider_accounts.health_state` already exists in `0001_pool_routing.up.sql`.
9. Current reality: `provider_accounts.health_state_until` already exists and is indexed.
10. Current reality: `channel_health_state.cooldown_until` already exists in migration `0022`.
11. Therefore this plan must not blindly add another `health_state` column.
12. The TR-D1 schema choice is specifically about cooldown naming/storage and dispatcher semantics:
    - reuse `health_state_until`;
    - add `provider_accounts.cooldown_until`;
    - or keep cooldown on `channel_health_state` and derive/propagate to provider accounts.
13. Upgrade P-A R-A3 from sidecar safe-equivalent evidence to true same-transaction audit ledger evidence.
14. Make Copilot 401 write the selected auth-expired audit outcome through `credentialworker.recordAudit`.
15. Make Anthropic invalid-grant / auth-expired refresh errors write the selected auth-expired audit outcome through the same scheduler audit path.
16. Keep all secrets out of DB audit rows, ledger rows, logs, errors, and test snapshots.
17. Preserve functionality: no feature is removed if a schema option is rejected.
18. If Owner rejects a physical enum/type option, use the chosen safe equivalent rather than dropping the auth-expired classification.

---

## §2 Current Gaps

### §2.1 Schema Facts

1. `oauth_refresh_audit_events.outcome` is a `text` column with an inline CHECK constraint in `backend/sql/migrations/0006_upstream_credential_management.up.sql:48-58`.
2. That CHECK does not include any auth-expired-specific logical outcome.
3. That CHECK does not include `revoked`.
4. That CHECK does not include `quota_exhausted`.
5. That CHECK does not include `risk_control_triggered`.
6. `provider_accounts.last_refresh_outcome` also has a CHECK in `backend/sql/migrations/0006_upstream_credential_management.up.sql:23-29`.
7. `provider_accounts.last_refresh_outcome` currently does not include the new logical outcomes either.
8. `account_credentials.last_refresh_outcome` exists in `backend/sql/migrations/0016_account_credentials.up.sql:39-41`.
9. `account_credentials.last_refresh_outcome` has no CHECK at its table definition.
10. `auth_audit.sql` inserts the outcome via `sqlc.arg(outcome)` and does not cast to a DB enum type.
11. Generated `InsertOAuthRefreshAuditEventParams.Outcome` is currently `string`.
12. There is no observed DB type named `audit_outcome` in the inspected migrations.
13. The phrase "outcome enum" currently means "text CHECK allow-list" in the live schema unless Owner chooses a new enum/domain representation.

### §2.2 Health-State Facts

1. `provider_accounts.health_state` exists in `backend/sql/migrations/0001_pool_routing.up.sql:119-122`.
2. Existing allowed provider account health states are:
   - `operational`
   - `degraded`
   - `failed`
   - `cooling_down`
   - `error`
3. `provider_accounts.health_state_until` exists in `backend/sql/migrations/0001_pool_routing.up.sql:122`.
4. `idx_provider_accounts_health_until` indexes `(health_state, health_state_until)` where the timestamp is present.
5. `channel_health_state.cooldown_until` exists in `backend/sql/migrations/0022_channel_health_state.up.sql:24`.
6. The dispatcher SQL already filters eligible accounts to `health_state IN ('operational', 'degraded')` in `backend/sql/queries/pool_accounts.sql:103-118`.
7. That means `failed`, `cooling_down`, and `error` provider accounts are already skipped by the pool-account SQL path.
8. The missing TR-D1 piece is not the existence of a health column.
9. The missing TR-D1 piece is a coherent write path and cooldown-clear/dispatch policy.
10. If a refresh failure sets `health_state='cooling_down'` but nothing clears it after the deadline, the dispatcher will skip the account forever.
11. S5 must therefore test both active cooldown and expired cooldown.

### §2.3 Code Facts

1. `backend/internal/auth/auth.go:37-52` defines refresh audit outcomes as Go string constants.
2. That list does not include the new logical outcomes.
3. `backend/internal/credentialworker/audit.go:21-64` now supports the same-transaction path:
   - insert `oauth_refresh_audit_events`;
   - append `audit_ledger_entries`;
   - use one `pgx.Tx`.
4. Commit `f49867f` landed that same-transaction path.
5. `backend/internal/credentialworker/scheduler.go:197-200` currently records `OutcomePermanentDisable` for all refresh failures after retry/backoff.
6. `backend/internal/provider/copilot/copilot_refresher.go:122-125` maps HTTP 401 from the Copilot service-token exchange to `ErrCopilotAuthExpired`.
7. `backend/internal/provider/copilot/copilot_refresher.go:216-220` maps that error to the string `auth_expired` only for the sidecar `RecordCopilotRefreshFailure` hook.
8. `backend/internal/credentialworker/scheduler_test.go:177-218` currently proves the Copilot sidecar sees `auth_expired`, but scheduler audit still records `permanent_disable`.
9. That is the P-A R-A3 safe-equivalent gap.
10. `backend/internal/anthropicoauth/refresher.go:275-292` classifies HTTP 401/400 `invalid_grant` as `failureAuthExpired`.
11. `backend/internal/anthropicoauth/refresher_test.go:23-55` proves the credential-store failure class is `auth_expired`.
12. That Anthropic path does not by itself prove `oauth_refresh_audit_events.outcome='auth_expired'`.
13. `backend/internal/credentialstore/postgres_store.go:757-799` maps `invalid_grant` and `auth_expired` failure classes to credential state `revoked`.
14. Credential state mutation and refresh audit ledger are related but distinct evidence streams.

### §2.4 P-A R-A3 Anchor

1. P-A R-A3 asked for refresh failure to fail closed into audit.
2. The P-A plan specified a 401 fixture and same-transaction audit/ledger assertion in `docs/process/plans/2026-05-24-placeholder-session-adapters-claude.md:201-205`.
3. Commit `9165551` implemented Copilot device-code OAuth and service-token refresh.
4. Commit `9165551` included Copilot 401 sidecar classification.
5. Commit `9165551` did not close the DB schema gate for a real `oauth_refresh_audit_events.outcome='auth_expired'` row.
6. This plan upgrades R-A3 from sidecar evidence to DB + ledger evidence.

---

## §3 Reference Project Comparison

Clean-room rules for this section:

1. Reference projects are evidence, not source providers.
2. LGPL Sub2API content is paraphrased only.
3. No source code, comments, schemas, or distinctive structure is copied.
4. The 2026-05-24 ref-anchor ledger says latest Sub2API anchor is `Wei-Shaw/sub2api@63b0631a5827` and latest LiteLLM anchor is `BerriAI/litellm@414866767176`.
5. The local source read used `~/refs-latest/...` per `docs/process/2026-05-24-ref-anchor.md:40-43`.

| Topic | Sub2API comparison | LiteLLM comparison | HUAKAI implication |
|---|---|---|---|
| Health result persistence | Sub2API persists each channel-monitor check result into history rows and updates the monitor's checked timestamp; failures to persist history are logged rather than blocking the check result. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290` | LiteLLM's health check flow partitions deployments into healthy and unhealthy endpoint lists and returns exception/status metadata for later handling. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:225-307` | HUAKAI should not hide health outcomes in logs only; at least one structured audit/state path must survive refresh failures. |
| Health rollup / maintenance | Sub2API has daily maintenance that aggregates health history, prunes old detail, and prunes rollups. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:348-449` | LiteLLM has a shared health-check manager that caches health results with TTL and coordinates checks with a lock. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:16-90`; `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:190-325` | HUAKAI should keep cooldown policy configurable and durable enough for ops; it can choose direct provider-account columns or reuse existing channel-health state, but the dispatcher must see the result. |
| Failure classification granularity | The inspected Sub2API health-monitor path records status, latency, ping latency, message, and checked time per result. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:271-282` | LiteLLM builds deployment health state as healthy/unhealthy with timestamp and reason for background health routing. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:310-345` | HUAKAI's refresh audit outcome should distinguish auth expiration from generic permanent disable; otherwise operator recovery and financial audit lose causality. |
| Cross-pod / storm control | Sub2API's observed monitor maintenance is scheduled and bounded by a max days-per-run cap. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:391-424` | LiteLLM coordinates health checks across pods with Redis locks and cache fallback. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:216-325` | HUAKAI refresh outcome writing must remain storm-safe; schema work must not invite every worker to hammer a failing vendor. |
| Exact outcome enum shape | The inspected reference health anchors do not decide HUAKAI's OAuth refresh outcome DB type. | The inspected LiteLLM health anchors do not decide HUAKAI's OAuth refresh outcome DB type. | Owner must choose HUAKAI's physical schema shape in §7; reference projects inform durability and cooldown policy, not string spelling. |

Open reference note:

- The prompt mentioned `BerriAI/litellm@414866767176:litellm/proxy/_experimental/health_check`.
- In the local latest tarball path recorded by `docs/process/2026-05-24-ref-anchor.md`, the health framework is under `litellm/proxy/health_check.py`, `litellm/proxy/health_endpoints/`, and `litellm/proxy/health_check_utils/`.
- This plan cites the observed latest paths rather than inventing a non-existent path.

---

## §4 File-Level Scope

### §4.1 Migration Scope

| File | Action | Risk |
|---|---|---|
| `backend/sql/migrations/0055_oauth_refresh_outcome_schema_gate.up.sql` | Create only after Owner selects D-A/D-B/D-C/D-D. Extend outcome schema and optional health cooldown schema. | HIGH: DB schema. |
| `backend/sql/migrations/0055_oauth_refresh_outcome_schema_gate.down.sql` | Fail-safe rollback; refuse down migration if rows contain new outcomes unless Owner selects explicit mapping. | HIGH: rollback can destroy audit evidence if careless. |

Migration planning facts:

1. Existing highest migration is `0054_oauth_acquisition_session_devicecode`.
2. This slice should use `0055`.
3. If another migration lands first, renumber before implementation.
4. Do not edit `LICENSE`.
5. Do not create destructive migrations.
6. Do not drop audit rows.
7. Do not rewrite existing outcome rows.

### §4.2 SQL / sqlc Scope

| File | Action | Risk |
|---|---|---|
| `backend/sql/queries/auth_audit.sql` | Usually unchanged if D-A uses text CHECK/domain; may need cast if Owner selects enum type. | Medium. |
| `backend/internal/db/auth/auth_audit.sql.go` | Regenerate with sqlc. | Generated. |
| `backend/internal/db/auth/models.go` | Regenerate if enum/domain changes sqlc model output. | Generated. |
| `backend/internal/db/auth/querier.go` | Regenerate if signatures change. | Generated. |
| `backend/sql/queries/pool_accounts.sql` | Validate or adjust dispatcher health/cooldown predicate in S5. | Medium: dispatch selection. |
| `backend/internal/db/billing/pool_accounts.sql.go` | Regenerate if `pool_accounts.sql` changes. | Generated. |

sqlc command:

```bash
cd backend
make generate
```

Expected:

1. If D-A is text CHECK only, `InsertOAuthRefreshAuditEventParams.Outcome` should remain `string`.
2. If D-A is enum type, sqlc may emit an enum-like Go type or still emit `string` depending on configuration.
3. The implementation must inspect generated diffs and update call sites accordingly.

### §4.3 Go Outcome Scope

| File | Action | Risk |
|---|---|---|
| `backend/internal/auth/auth.go` or new `backend/internal/auth/refresh_outcome.go` | Add logical outcome constants selected by Owner; add outcome-carrier helper if chosen. | Medium. |
| `backend/internal/credentialworker/outcome.go` | New helper file allowed; package is under file/LOC budget. Maps provider errors to audit outcomes without importing provider packages into scheduler. | Medium. |
| `backend/internal/credentialworker/scheduler.go` | Replace all-refresh-failure `permanent_disable` collapse with selected classifier. | Medium. |
| `backend/internal/credentialworker/audit.go` | Usually unchanged; may include outcome validation if selected. | Medium. |
| `backend/internal/provider/copilot/copilot_refresher.go` | Wrap 401 auth-expired error with a scheduler-readable refresh audit outcome. | Medium. |
| `backend/internal/anthropicoauth/refresher.go` | Expose auth-expired / quota / non-retryable outcome from `RefreshError`. | Medium. |
| `backend/internal/credentialstore/postgres_store.go` | Usually unchanged for state mapping; update only if D-F chooses to mirror new outcomes into `account_credentials.last_refresh_outcome`. | Medium. |

Package-structure check:

1. `backend/internal/credentialworker` has 12 non-test Go files and about 1.9k non-test LOC.
2. Adding one small `outcome.go` does not violate the package budget.
3. `backend/internal/auth` has 7 non-test Go files and about 1.1k non-test LOC.
4. Adding one small outcome-helper file does not violate the package budget.
5. `backend/internal/provider/copilot` has 4 non-test Go files and about 856 non-test LOC.
6. Prefer editing existing `copilot_refresher.go`; do not split unless the implementation becomes unclear.
7. No new files may be added under frozen packages:
   - `backend/internal/gatewayhttp`
   - `backend/internal/gateway`
   - `backend/internal/proto`

### §4.4 Test Scope

| File | Action | Risk |
|---|---|---|
| `backend/internal/credentialworker/audit_tx_pg_test.go` | Extend true PG tests for selected auth-expired outcome and rollback behavior. | High-value. |
| `backend/internal/credentialworker/scheduler_test.go` | Update Copilot test to expect scheduler audit outcome selected by D-B, not `permanent_disable`. | Medium. |
| `backend/internal/provider/copilot/copilot_refresher_test.go` | Keep sidecar classification test; add outcome-carrier assertion if helper is visible. | Medium. |
| `backend/internal/anthropicoauth/refresher_test.go` | Add outcome-carrier assertion for invalid grant and rate-limit classes. | Medium. |
| `backend/internal/pool/db_adapters_integration_test.go` or targeted pool test | Add dispatcher health/cooldown selection test if S5 changes SQL. | Medium. |
| `backend/internal/db/pgconn_integration_test.go` | Optional schema sanity test for new migration version and columns. | Low. |

### §4.5 Docs Scope

| File | Action | Risk |
|---|---|---|
| `docs/10_RISK_REGISTER.md` | Update only if Owner rejects required schema or if implementation defers a mandatory outcome. | Low docs. |
| `docs/process/plans/2026-05-24-auth-expired-schema-gate-synthesis.md` | Written later by orchestrator after cross-discussion. | N/A in this lane. |
| `docs/process/reviews/PENDING-auth-expired-schema-gate.md` | Optional review context before `codex exec review --uncommitted`. | Low docs. |

---

## §5 Slice Plan

### S1 — Migration 0055 schema gate

Goal:

1. Make the DB accept the selected logical refresh outcomes.
2. Keep old outcomes valid.
3. Avoid destructive rollback.
4. Reconcile provider-account health cooldown storage if Owner chooses to include it in this commit.

Execution steps:

1. Confirm no migration newer than `0054` exists.
2. Confirm Owner selected D-A physical schema shape.
3. Confirm Owner selected D-B logical outcome set and physical spellings.
4. Confirm Owner selected D-C health cooldown storage.
5. If D-A = text CHECK:
   - update `oauth_refresh_audit_events.outcome` CHECK;
   - update `provider_accounts.last_refresh_outcome` CHECK only if D-F says it mirrors new outcomes.
6. If D-A = enum/domain:
   - add the selected type/domain in 0055;
   - migrate/cast the affected columns;
   - document lock/rewrite risk before execution.
7. If D-C = reuse `health_state_until`:
   - do not add a duplicate `cooldown_until` column;
   - add or update comments to define it as provider-account cooldown deadline.
8. If D-C = add `provider_accounts.cooldown_until`:
   - add nullable column;
   - add index for active cooldown;
   - define data sync/backfill from `health_state_until` or leave both distinct by contract.
9. If D-D = split health into later commit:
   - keep 0055 focused on outcome schema only;
   - add a mandatory follow-up item for health cooldown.
10. Down migration:
    - must not silently map `auth_expired` or equivalent to `permanent_disable`;
    - should raise an exception if new outcome rows exist;
    - may drop newly added nullable columns only if no data would be lost or Owner approves.

Text CHECK variant sketch, not selected until D-A/D-B:

```sql
-- 0055 up, Option A shape only after Owner selects physical strings.
ALTER TABLE oauth_refresh_audit_events
  DROP CONSTRAINT IF EXISTS oauth_refresh_audit_events_outcome_check;

ALTER TABLE oauth_refresh_audit_events
  ADD CONSTRAINT oauth_refresh_audit_events_outcome_check
  CHECK (outcome IN (
    -- existing outcomes retained here,
    -- selected new physical outcome strings inserted here
  ));
```

Down migration invariant:

```sql
-- 0055 down must refuse if rows would lose evidence.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM oauth_refresh_audit_events
    WHERE outcome IN (
      -- selected new physical outcome strings
    )
  ) THEN
    RAISE EXCEPTION 'refusing to downgrade: oauth_refresh_audit_events contains 0055 outcomes';
  END IF;
END $$;
```

Test requirements:

1. Insert one audit row for every selected new physical outcome.
2. Insert one audit row for an old outcome to prove backward compatibility.
3. Insert one invalid outcome and assert DB rejects it.
4. If `last_refresh_outcome` is updated, run the same acceptance/rejection checks against provider account updates.
5. Run migration up/down on a disposable PG database.
6. Verify down refuses when new outcome data exists.

### S2 — sqlc regeneration

Goal:

1. Make generated DB code match migration/query shape.
2. Avoid hand-editing generated files.

Execution steps:

1. Run:

```bash
cd backend
make generate
```

2. Inspect generated diffs.
3. If sqlc changes `InsertOAuthRefreshAuditEventParams.Outcome`, update `credentialworker.refreshAuditParams`.
4. If sqlc does not change any generated code under text CHECK, record that as expected.
5. Do not edit generated code manually.
6. Run:

```bash
cd backend
go test ./internal/db/auth ./internal/credentialworker -count=1
```

Expected:

1. No compile errors.
2. No nil audit writer regression.
3. No generated-code hand edits.

### S3 — Outcome classification upgrade

Goal:

1. Prevent provider refresh failures from collapsing into generic `permanent_disable`.
2. Keep generic failures generic.
3. Route known terminal/operational failures to selected outcomes.

Proposed design:

1. Add an outcome carrier contract in `internal/auth`, for example:

```go
type RefreshAuditOutcomeCarrier interface {
    RefreshAuditOutcome() Outcome
}
```

2. Add a helper that uses `errors.As`:

```go
func OutcomeFromRefreshError(err error) (Outcome, bool) {
    var carrier RefreshAuditOutcomeCarrier
    if errors.As(err, &carrier) {
        outcome := carrier.RefreshAuditOutcome()
        return outcome, outcome != ""
    }
    return "", false
}
```

3. Add selected outcome constants after D-B.
4. Update `Scheduler.processAccount`:
   - use provider-specific outcome when present;
   - otherwise keep existing `permanent_disable` behavior for final refresh failure.
5. Keep retry/backoff behavior unchanged.
6. Keep storm-budget behavior unchanged.
7. Keep audit sanitization unchanged.

Scheduler logic target:

```go
auditOutcome := auth.OutcomePermanentDisable
if classified, ok := auth.OutcomeFromRefreshError(err); ok {
    auditOutcome = classified
}
return errors.Join(err, s.recordAudit(ctx, account, auditOutcome, "", err))
```

Provider logic target:

1. Copilot 401 should wrap `ErrCopilotAuthExpired` with the selected auth-expired outcome.
2. Anthropic `RefreshError{Class: failureAuthExpired}` should expose the selected auth-expired outcome.
3. Anthropic `failureRateLimitExceeded` should expose the selected quota/rate-limit outcome only if D-B selects one.
4. Temporary failures should not expose a terminal outcome unless D-B selects a transient outcome.
5. Payload/decrypt failures should map to operator-attention or risk-control only if D-B selects that mapping.

Mutation self-check:

1. Remove `errors.As` outcome lookup: Copilot and Anthropic tests must fail.
2. Return `permanent_disable` for all errors: auth-expired tests must fail.
3. Make every error return auth-expired: generic refresh failure tests must fail.
4. Drop error sanitization: secret sentinel test must fail.

### S4 — P-A R-A3 true integration test

Goal:

1. Prove Copilot 401 writes selected auth-expired outcome into `oauth_refresh_audit_events`.
2. Prove the audit ledger row is committed in the same transaction.
3. Prove the test would fail if schema 0055 is missing.
4. Prove the test would fail if scheduler still records `permanent_disable`.

Recommended test name:

```go
func TestSchedulerCopilot401RecordsAuthExpiredAuditLedgerInSameTx(t *testing.T)
```

Fixture:

1. Real PG pool from `HUAKAI_DATABASE_URL`.
2. Full tenant/provider/channel/provider_account FK chain using existing helper style.
3. Copilot provider account row.
4. Scheduler with:
   - `WithVendorRefresher("copilot", refresher)`;
   - `WithTxPool(pool)`;
   - `WithAuditQueries(dbauth.New(pool))`;
   - `WithAuditLedger(pgLedger)`;
   - `WithAuditLedgerSigner(signer)`.
5. Mock Copilot token endpoint returns HTTP 401.
6. Credential store returns a payload containing a redacted/synthetic GitHub auth token.
7. Max attempts set to 1 to avoid timing noise.

Assertions:

1. `RunOnce` returns an error matching Copilot auth-expired cause.
2. `oauth_refresh_audit_events` has exactly one row for the tenant/account.
3. That row has the selected auth-expired physical outcome.
4. That row has sanitized `error_class` and `error_message_redacted`.
5. The raw synthetic token does not appear in audit row text.
6. `audit_ledger_entries` has exactly one row for the same tenant/request.
7. The ledger request ID matches or is derivable from the audit request ID.
8. No refreshed credential was saved.

Rollback companion:

1. Install a trigger rejecting the selected auth-expired outcome.
2. Run the same Copilot 401 path.
3. Assert `oauth_refresh_audit_events` row count is 0.
4. Assert `audit_ledger_entries` row count is 0.
5. This catches regression from same-transaction path to two-step path.

Mutation self-check:

1. Remove 0055 from DB: insert fails with CHECK violation and test fails.
2. Remove provider outcome wrapper: row is `permanent_disable`, test fails.
3. Remove `recordAudit` tx path: rollback companion leaves ledger row, test fails.
4. Replace 401 fixture with 500 fixture: expected auth-expired assertion fails.
5. Use a fixture where status alone would classify the same: forbidden; 500 vs 401 must be discriminating.

### S4b — Anthropic refresher true audit classification

Goal:

1. Prove Anthropic invalid-grant refresh failure carries the selected auth-expired outcome to scheduler audit.
2. Keep existing credential-store failure-class test.

Recommended tests:

```go
func TestAnthropicRefreshErrorCarriesAuthExpiredOutcome(t *testing.T)
func TestSchedulerAnthropicInvalidGrantRecordsAuthExpiredAuditLedgerInSameTx(t *testing.T)
```

Fixture:

1. `anthropicoauth.Refresher` with mock token endpoint returning:
   - HTTP 401;
   - body `{"error":"invalid_grant"}`.
2. A refresh store that returns an Anthropic OAuth credential record.
3. Scheduler provider-aware path calls `RefreshForProvider`.
4. PG integration variant uses the same transaction wiring as S4.

Assertions:

1. Existing credential store failure class remains `auth_expired`.
2. Scheduler audit row uses the selected auth-expired outcome.
3. Audit ledger row commits in same tx.
4. A 429 fixture maps to selected quota/rate-limit outcome only if D-B selects it.
5. A 500 fixture remains non-terminal/generic and must not produce auth-expired.

Mutation self-check:

1. Change `oauthErrorCode` body parsing to ignore JSON `invalid_grant`: auth-expired test fails.
2. Change classifier to rely only on 401 status: pair fixture with 401 non-invalid-grant body must fail.
3. Remove outcome carrier method from `RefreshError`: scheduler audit assertion fails.
4. Force all Anthropic errors to auth-expired: 500 fixture fails.

### S5 — health_state / dispatcher filtering

Goal:

1. Close the TR-D1 health persistence thread without duplicating existing schema.
2. Ensure dispatcher selection and cooldown state agree.

Current dispatcher fact:

```sql
AND pa.health_state IN ('operational', 'degraded')
```

Options:

1. If D-C reuses `health_state_until`:
   - ensure refresh failure write path sets `health_state='cooling_down'` and `health_state_until=<deadline>`;
   - add maintenance logic to clear expired cooldown back to `degraded` or `operational`;
   - keep dispatcher predicate unchanged.
2. If D-C adds `provider_accounts.cooldown_until`:
   - set it with `health_state='cooling_down'`;
   - update query or maintenance so expired cooldown does not stay excluded forever.
3. If D-C uses `channel_health_state.cooldown_until`:
   - decide whether dispatcher joins `channel_health_state` or a sync job denormalizes to `provider_accounts.health_state`;
   - joining may increase hot-path cost and should be measured.
4. If D-D splits health from this commit:
   - S5 is verification-only in this slice;
   - record mandatory follow-up for health write path.

Test fixtures:

1. Account A: `health_state='operational'`, no cooldown; must be eligible.
2. Account B: `health_state='degraded'`, no cooldown; must be eligible.
3. Account C: `health_state='cooling_down'`, future deadline; must be ineligible.
4. Account D: `health_state='cooling_down'`, past deadline; expected result depends on D-C:
   - if maintenance clears state, test the maintenance function then dispatcher eligibility;
   - if query treats expired cooldown as eligible, test query directly;
   - if no auto-clear in this commit, mark as mandatory follow-up and do not claim closure.
5. Account E: `credential_state='revoked'`; must be ineligible even if health is operational.

Mutation self-check:

1. Remove health predicate: cooling account becomes eligible and test fails.
2. Remove credential predicate: revoked account becomes eligible and test fails.
3. Ignore cooldown deadline: expired/future pair no longer discriminates and test fails.

---

## §6 Risk And Test Matrix

| Risk ID | Outcome / path | Real defect guarded | Discriminating fixture | Mutation self-check | Test level |
|---|---|---|---|---|---|
| R-001 | selected auth-expired outcome | Copilot 401 is audited as generic permanent disable | Copilot token endpoint returns 401; compare to 500 fixture | remove outcome carrier, audit row becomes generic | unit + true PG |
| R-002 | selected auth-expired outcome | Anthropic invalid-grant does not reach audit ledger | 401/400 with JSON `invalid_grant`; paired 401 with different body | ignore body parsing; fixture flips | unit + true PG |
| R-003 | selected revoked outcome | Revoked token/user auth is collapsed into transient | fixture with selected revoked signal distinct from invalid-grant | map all 401 to auth-expired; revoked assertion fails | unit |
| R-004 | selected quota outcome | Rate-limit/quota exhaustion causes permanent disable | 429 with retry-after vs 401 invalid-grant | map 429 to auth-expired; quota test fails | unit |
| R-005 | selected risk-control outcome | Vendor risk-control block is retried until account damage | risk-control body/status fixture distinct from quota | classify by status only; test fails | unit |
| R-006 | DB schema | 0055 missing in production rejects auth-expired insert | direct insert selected outcome into real PG | remove migration; CHECK violation | migration PG |
| R-007 | down migration | rollback destroys audit evidence | seed selected new outcome then run down | down silently maps/deletes; test fails | migration PG |
| R-008 | sqlc | generated type/signature mismatch hidden by manual edits | regenerate and compile | hand edit generated file; regeneration diff catches | build |
| R-009 | same tx | audit row commits without ledger or ledger without audit | trigger rejects audit insert | remove BeginFunc; ledger row remains | true PG |
| R-010 | secret leakage | upstream token appears in error/audit row | sentinel token in failed credential | include raw error; text scan fails | unit + PG |
| R-011 | health cooldown | cooling account still selected | operational/degraded/cooling fixture set | remove health predicate; selection test fails | SQL integration |
| R-012 | expired cooldown | account never recovers after deadline | future vs past cooldown pair | ignore deadline; pair no longer differs | SQL integration |
| R-013 | test quality | fixture passes even if body parsing removed | status-only expected differs from body-driven expected | remove body parser; test must fail | self-check |

Outcome-specific rules:

1. Each selected outcome needs at least one test where the correct output differs from the broken output.
2. A 401-only fixture is insufficient for body-driven auth-expired logic.
3. A generic 429 fixture is insufficient if quota and risk-control both use 429; body/header must distinguish them.
4. A revoked fixture must not share the same distinguishing feature as auth-expired unless D-B intentionally merges them.
5. If Owner merges two logical outcomes physically, tests must assert the merge is explicit and documented.
6. No test may use a nil stub that bypasses the risk under test.
7. True PG tests may skip when `HUAKAI_DATABASE_URL` is unset, but unit tests must still cover classification.

---

## §7 D Decision Points

### D-A — Physical DB outcome schema

| Option | Description | Reference project comparison | Pros | Cons | Codex recommendation |
|---|---|---|---|---|---|
| A | Keep current `text` column and extend named CHECK constraints. | Sub2API persists health results as DB records and uses maintenance/rollup paths, but the inspected anchor does not require a DB enum type. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290`; `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:363-424` | Minimal rewrite; keeps sqlc stable; safest schema gate. | Less type-safe than enum; CHECK name must be controlled. | Recommended for this slice. |
| B | Create PostgreSQL enum type for refresh audit outcomes and convert affected columns. | LiteLLM health framework uses structured healthy/unhealthy state construction, but the inspected anchor does not decide HUAKAI DB enum representation. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:269-345` | Stronger DB typing; explicit schema. | Higher lock/rewrite risk; sqlc churn; harder down migration. | Only if Owner explicitly wants DB enum type. |
| C | Create a domain over text with CHECK and cast affected columns to domain. | LiteLLM shared manager coordinates health result state with TTL/lock rather than a DB enum in the inspected health path. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:216-325` | Centralizes constraint; preserves text-ish behavior. | Still schema-heavy; less common in repo. | Acceptable but not preferred. |

### D-B — Logical outcome set and physical spelling

| Option | Description | Reference project comparison | Product impact | Codex recommendation |
|---|---|---|---|---|
| A | Minimal: add only auth-expired physical outcome now; defer revoked/quota/risk. | Reference health anchors show structured health outcomes are useful, but do not force all categories in one schema change. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:271-282`; `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:276-307` | Smallest blast radius; P-A R-A3 closes. | Too narrow for Owner's requested candidate set. |
| B | Add Owner's four logical outcomes now, with Owner-selected physical strings. | Sub2API records enough per-check detail to support later rollups; LiteLLM preserves exception/status metadata for unhealthy endpoints. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:271-282`; `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:289-307` | Better operator recovery and analytics. | Requires careful classification tests for each category. | Recommended if Owner accepts physical names. |
| C | Add a broader normalized set: auth, revoked, quota/rate, risk, transient, non-retryable, disabled. | LiteLLM distinguishes healthy/unhealthy and stores exception status, but this is not an OAuth refresh outcome taxonomy. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:269-307` | Future-proof. | More naming debate and test surface. | Defer unless Owner wants broader enum governance now. |

Decision needed:

1. Are `auth_expired` and `revoked` separate physical outcomes?
2. Is 429 named `quota_exhausted`, `rate_limit_exceeded`, or both?
3. Is risk-control a terminal outcome or a cooldown outcome?
4. Should existing `permanent_disable` remain or be replaced by more specific categories?

### D-C — Provider account cooldown storage

| Option | Description | Reference project comparison | Pros | Cons | Codex recommendation |
|---|---|---|---|---|---|
| A | Reuse existing `provider_accounts.health_state_until` as cooldown deadline. | Sub2API keeps health history and maintenance in a dedicated monitor service; HUAKAI already has a provider-account deadline column. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:348-449` | No duplicate column; current dispatcher already filters health_state. | Name differs from Owner's `cooldown_until` phrase. | Recommended unless Owner requires exact column name. |
| B | Add nullable `provider_accounts.cooldown_until`. | Sub2API uses explicit persisted health history and rollup state, supporting schema-first ops visibility. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290` | Name matches TR-D1/PH-D3 wording. | Duplicates `health_state_until` unless old column is deprecated; migration complexity. | Use only if Owner requires name alignment. |
| C | Use existing `channel_health_state.cooldown_until` and sync/join to dispatcher. | LiteLLM coordinates shared health state outside hot request state and can read cached health results. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:190-325` | Keeps richer F-CH-002 state. | Hot-path join or sync complexity; not the simplest TR-D1 close. | Defer unless channel-health owns all future health. |

### D-D — Include health-state schema in 0055 or split

| Option | Description | Reference project comparison | Impact | Codex recommendation |
|---|---|---|---|---|
| A | 0055 includes outcome schema and minimal cooldown reconciliation. | Sub2API combines health result persistence with monitor timestamp update in one service path. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290` | One DB review; faster TR-D1 closure. | Larger high-risk migration. | Accept only if D-C is simple reuse/comment or one nullable column. |
| B | 0055 closes outcome only; health cooldown gets 0056. | LiteLLM separates health check execution from shared coordination/cache concerns. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:225-307`; `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:190-325` | Smaller schema gate for P-A R-A3. | TR-D1 remains partially open. | Recommended if Owner wants low blast radius. |

### D-E — PH-D3 / TR-D4 cooldown policy defaults

| Option | Description | Reference project comparison | Impact | Codex recommendation |
|---|---|---|---|---|
| A | Configurable default: 3 consecutive classified failures -> 30min cooldown. | LiteLLM's shared manager exposes configurable health-check TTL and lock TTL. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:27-36` | Matches locked decisions; tunable per vendor later. | Needs config surface and tests. | Recommended. |
| B | One auth-expired/risk failure immediately revokes or disables. | Sub2API persists check results and continues returning result even if history write fails; that observed path is not an immediate account kill switch. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290` | Strong fail-closed. | Can overreact to vendor false positive. | Use only for clearly terminal auth-expired/revoked. |
| C | Exponential cooldown by outcome class. | LiteLLM waits for lock/cache and falls back locally after bounded wait, showing bounded backoff-style coordination. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:269-325` | More adaptive. | More code/tests; not necessary for schema gate. | Later enhancement. |

### D-F — Should `last_refresh_outcome` mirror new audit outcomes?

| Option | Description | Reference project comparison | Impact | Codex recommendation |
|---|---|---|---|---|
| A | Mirror selected detailed outcome into `provider_accounts.last_refresh_outcome`. | Sub2API updates monitor checked timestamp after persisting per-check history. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290` | Admin list can show last cause directly. | Requires CHECK update on provider_accounts. | Recommended if UI/admin reads provider_accounts. |
| B | Keep `last_refresh_outcome` coarse and use audit table for detail. | LiteLLM builds detailed endpoint health state separately from request execution. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:310-345` | Smaller schema. | Operators need audit query for cause. | Acceptable for minimal slice. |

---

## §8 Verification Plan

Pre-check:

```bash
git status --short
```

Expected:

1. Only intended implementation files are dirty.
2. No unrelated user changes are reverted.
3. No files are staged unless Owner later authorizes commit workflow.

Schema and sqlc:

```bash
cd backend
make generate
go test ./internal/db/auth -count=1
```

Migration round trip:

```bash
cd backend
make db-up
make db-migrate
HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" \
  go test ./internal/credentialworker -run 'TestOAuthRefreshOutcomeSchema|TestSchedulerCopilot401RecordsAuthExpired' -count=1
```

Notes:

1. Do not run `make db-reset` without explicit Owner approval.
2. If the dev DB is dirty, use a disposable DB or container.
3. The migration down test must use a controlled disposable database.

Targeted unit tests:

```bash
cd backend
go test ./internal/provider/copilot ./internal/anthropicoauth ./internal/credentialworker -count=1
```

Targeted integration tests:

```bash
cd backend
HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" \
  go test ./internal/credentialworker -run 'TestRecordAudit|TestSchedulerCopilot401|TestSchedulerAnthropicInvalidGrant' -count=1
```

Dispatcher tests:

```bash
cd backend
HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" \
  go test -tags=integration_pg ./internal/pool -run 'Health|Cooldown|Eligible' -count=1
```

Full backend confidence:

```bash
cd backend
go test ./... -race -count=1
```

Optional integration sweep:

```bash
cd backend
HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" \
  go test -tags=integration_pg -race -count=1 ./...
```

Review gate before any commit:

```bash
codex exec review --uncommitted --full-auto
```

Expected:

1. HIGH findings fixed before commit.
2. MED findings fixed or explicitly documented.
3. LOW findings tracked.
4. No commit before review.

---

## §9 Source Files

HUAKAI specs and process docs read:

1. `docs/process/plans/2026-05-24-token-refresh-worker-closure-synthesis.md:1-99`
2. `docs/process/plans/2026-05-24-decisions-locked.md:1-80`
3. `docs/process/2026-05-24-ref-anchor.md:1-49`
4. `docs/process/plans/2026-05-24-placeholder-session-adapters-claude.md:190-210`
5. `docs/process/plans/2026-05-24-placeholder-session-adapters-claude.md:268-276`
6. `docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md:193-208`
7. `docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md:508-515`
8. `docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md:623-634`
9. `docs/process/plans/2026-05-24-placeholder-session-adapters-synthesis.md:40-108`

HUAKAI schema/query/code read:

1. `backend/sql/migrations/0001_pool_routing.up.sql:108-164`
2. `backend/sql/migrations/0004_rate_limiting.up.sql:30-84`
3. `backend/sql/migrations/0006_upstream_credential_management.up.sql:19-83`
4. `backend/sql/migrations/0016_account_credentials.up.sql:1-70`
5. `backend/sql/migrations/0022_channel_health_state.up.sql:1-70`
6. `backend/sql/queries/auth_audit.sql:1-32`
7. `backend/sql/queries/auth_credentials.sql:1-57`
8. `backend/sql/queries/pool_accounts.sql:80-140`
9. `backend/sqlc.yaml:1-180`
10. `backend/Makefile:1-76`
11. `backend/internal/db/auth/auth_audit.sql.go:1-81`
12. `backend/internal/auth/audit.go:1-31`
13. `backend/internal/auth/auth.go:1-55`
14. `backend/internal/auditledger/types.go:1-61`
15. `backend/internal/credentialworker/audit.go:1-119`
16. `backend/internal/credentialworker/options.go:1-127`
17. `backend/internal/credentialworker/scheduler.go:150-215`
18. `backend/internal/credentialworker/scheduler_test.go:150-230`
19. `backend/internal/credentialworker/audit_tx_pg_test.go:1-281`
20. `backend/internal/credentialworker/mode_refresh.go:1-120`
21. `backend/internal/credentialworker/mode_refresh.go:420-470`
22. `backend/internal/credentialworker/refresher.go:130-180`
23. `backend/internal/credentialworker/refresh_adapter.go:70-120`
24. `backend/internal/provider/copilot/copilot_refresher.go:1-270`
25. `backend/internal/provider/copilot/copilot_refresher_test.go:70-130`
26. `backend/internal/anthropicoauth/refresher.go:1-90`
27. `backend/internal/anthropicoauth/refresher.go:100-240`
28. `backend/internal/anthropicoauth/refresher.go:260-310`
29. `backend/internal/anthropicoauth/refresher.go:420-450`
30. `backend/internal/anthropicoauth/refresher_test.go:1-70`
31. `backend/internal/credentialstore/postgres_store.go:520-800`
32. `backend/internal/credentialstore/credential_audit_tx_integration_test.go:1-200`
33. `backend/internal/credentialstore/types.go:1-70`
34. `backend/internal/credentialstore/types.go:228-270`

Commit evidence inspected:

1. `git show --stat 9165551`
2. `git show --stat f49867f`
3. `git show f49867f -- backend/internal/credentialworker/audit.go backend/internal/credentialworker/audit_tx_pg_test.go`

Reference source regions read:

1. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269-290`
2. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:348-449`
3. `BerriAI/litellm@414866767176:litellm/proxy/health_check.py:225-350`
4. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:16-90`
5. `BerriAI/litellm@414866767176:litellm/proxy/health_check_utils/shared_health_check_manager.py:190-325`

Protocol exception:

1. A broad search command accidentally printed snippets from `docs/process/plans/2026-05-24-auth-expired-schema-gate-claude.md`.
2. This file is not used as a source for this Codex plan.
3. It must not be used by implementers as evidence for this slice.

Observed regions: 58
Inferences: 9
Open questions: 6

Open questions:

1. Should physical DB strings exactly equal the logical names listed by Owner?
2. Should `revoked` be separate from `auth_expired` or an alias in this slice?
3. Should quota/rate-limit use `quota_exhausted`, `rate_limit_exceeded`, or both?
4. Should `provider_accounts.health_state_until` be renamed, reused, or shadowed by `cooldown_until`?
5. Should `provider_accounts.last_refresh_outcome` mirror detailed audit outcomes?
6. Should health-state persistence be included in 0055 or split into the next schema migration?

---

## §10 Lane + UTC

Lane: specifier

Agent: Codex / GPT-5

UTC timestamp: 2026-05-24T11:10Z plan lane; authored during 2026-05-24T11Z session.

Source files read: listed in §9.

Clean-room status:

1. No non-MIT source code copied.
2. No reference schemas copied.
3. No reference comments copied into implementation artifacts.
4. Reference behavior is paraphrased and cited.
5. LGPL Sub2API use is behavior-only evidence.

Owner 中文摘要：本 Codex lane 计划确认了真实缺口：`oauth_refresh_audit_events.outcome` 和 `provider_accounts.last_refresh_outcome` 的现有 CHECK 不接受 `auth_expired` 等新 outcome，而 `provider_accounts.health_state` 已经存在，TR-D1 真正要决定的是 cooldown 存在 `health_state_until`、新增 `cooldown_until`，还是复用 `channel_health_state.cooldown_until`。计划没有功能缩水，Copilot/Anthropic 的 auth-expired 分类都会升级为真实同事务 audit ledger 集成测试；clean-room 风险低，因为 Sub2API/LiteLLM 只作行为对照且未复制实现；安全风险集中在 DB schema、审计证据不可回滚、secret 泄漏和 dispatcher 错选账号，均有判别 fixture 与 mutation 自检。需要 Owner 确认 D-A 到 D-F，尤其是 outcome 物理命名、是否把 health cooldown 放进 0055、以及 PH-D3/TR-D4 默认 cooldown 策略。
