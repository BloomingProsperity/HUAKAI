# 2026-05-24 S2 refresh outcome wiring - Codex independent plan

| Owner directive | "S2 切片接通:把 ClassifyRefreshError 真接入 anthropic + copilot vendor refresher, audit 写入新 4 类 outcome。" |
| --- | --- |
| Scope | In: wire `credentialworker.ClassifyRefreshError` into existing Anthropic and Copilot refresh failure paths; preserve transient 5xx as the old stored outcome; update OAuth audit severity mapping in `backend/sql/queries/observability.sql`; regenerate sqlc output; close the deferred review note. Out: no migration changes, no auth core redesign, no billing/quota changes, no new runtime dependencies, no reference-source reading. |
| Success criteria | Anthropic 401 writes `auth_expired`; Copilot 429 writes `rate_limit_exceeded`; Copilot 403 risk body writes `risk_control_triggered`; 5xx still records the old transient/refresh-failed outcome rather than a new `transient_error` audit outcome; observability maps `auth_expired`, `risk_control_triggered`, and `account_disabled` to `error`, `rate_limit_exceeded` to `warning`; requested build/test commands pass or failures are reported with exact output. |
| Time estimate | 45-75 minutes wall clock, mostly tests, sqlc generation, and race test runtime. |
| Blast radius | Credential refresh failure persistence, credential refresh audit rows, Admin Ops audit-event severity filtering, generated sqlc billing query code. No database schema or hot-path request routing changes. |
| Failure modes | Wrong status extraction could collapse all vendor errors to `unknown`; mitigation: mutation-style tests assert exact good outcomes. A 5xx could start persisting `transient_error` although the schema may allow it; mitigation: explicit 5xx test asserts legacy outcome. sqlc generation could alter unrelated generated files; mitigation: inspect `git diff --stat` and generated diff. Race tests may expose existing flakes; mitigation: report separately without hiding code state. |
| Decision points | If `SaveRefreshFailure` signature needs to change across shared production stores, confirm whether to use `credentialworker.RefreshOutcome` directly or keep string boundaries. If real Postgres round-trip tests need a DB unavailable in this environment, report the skipped integration verification and keep deterministic unit coverage. No high-risk files are expected. |
| Pre-execution checklist | Confirm worktree status; confirm no matching Claude plan is being read; inspect existing refresher, audit, and sqlc patterns; verify no new files in frozen packages; write failing tests before production edits; regenerate sqlc only after SQL edit; run requested build/tests; run at most two `codex exec review --uncommitted` rounds if CLI is available; do not `git add`, commit, or push. |

## File Scope And Structure Check

- Modify existing `backend/internal/anthropicoauth/refresher.go`; no new file in frozen package.
- Modify existing `backend/internal/anthropicoauth/refresher_test.go`; no new file in frozen package.
- Modify existing `backend/internal/provider/copilot/copilot_refresher.go`.
- Modify existing `backend/internal/provider/copilot/copilot_refresher_test.go`.
- Modify existing `backend/internal/credentialworker/audit.go` only if the scheduler audit helper must accept `RefreshOutcome`; initial read suggests the vendor refreshers persist through credential-store failure helpers, so this may not need code changes.
- Modify existing `backend/internal/auth/auth.go` only if scheduler audit outcomes need named constants for the four new strings; prefer avoiding this unless tests prove it is needed.
- Modify existing `backend/sql/queries/observability.sql` and regenerate generated sqlc files under the configured package. Current config generates observability queries into `backend/internal/db/billing/observability.sql.go`, not `backend/internal/db/observability/*.sql.go`.
- Modify existing `docs/process/reviews/DEFERRED-audit-outcome-severity-mapping.md` by appending the requested close marker.

No target package gets a new source file. No frozen package receives a new file.

## Concrete Execution Order

1. Establish RED tests:
   - In `backend/internal/anthropicoauth/refresher_test.go`, strengthen the 401 test so the recorded audit/failure outcome is exactly `auth_expired`, and add a 5xx case that proves the stored outcome remains the old transient/temporary value rather than `transient_error`.
   - In `backend/internal/provider/copilot/copilot_refresher_test.go`, add exact-outcome tests for 429 and 403 risk body, plus a 5xx legacy-outcome test.
   - Run targeted tests and confirm new tests fail for the missing wiring.

2. Wire Anthropic:
   - Import `internal/credentialworker`.
   - Add a helper that extracts `RefreshError.StatusCode`.
   - In `refreshLockedRecord`, classify failure with `credentialworker.ClassifyRefreshError(err, "anthropic", statusCode)`.
   - Persist `auth_expired`, `rate_limit_exceeded`, `risk_control_triggered`, or `account_disabled` as strings when returned.
   - Map `OutcomeTransientError` and `OutcomeUnknown` back to the existing `classifyRefreshFailure(err)` result so 5xx remains the old transient/temporary stored literal.

3. Wire Copilot:
   - Import `internal/credentialworker`.
   - Preserve `ErrCopilotAuthExpired` behavior.
   - Make non-2xx service-token errors carry status and limited safe body text so `ClassifyRefreshError(err, "copilot", statusCode)` can distinguish 429 and 403 risk signals.
   - Persist the four new outcomes as strings through `RecordCopilotRefreshFailure`.
   - Map `OutcomeTransientError` and `OutcomeUnknown` to the legacy `refresh_failed` outcome.

4. Update observability severity:
   - Edit both OAuth refresh severity CASE expressions in `backend/sql/queries/observability.sql`.
   - Run `cd backend && sqlc generate`.
   - Inspect generated `backend/internal/db/billing/observability.sql.go`.

5. Close deferred record:
   - Append `[CLOSED 2026-05-24 by S2 切片实施, see commit]` to `docs/process/reviews/DEFERRED-audit-outcome-severity-mapping.md`.

6. Verify:
   - Run targeted RED/GREEN tests after implementation.
   - Run mutation self-checks by temporarily forcing the classifier bridge to return `unknown` for one protected case and confirming the exact-outcome test fails, then revert the mutation before final verification.
   - Run `cd backend && GOCACHE=/tmp/go-build go build ./...`.
   - Run `cd backend && GOCACHE=/tmp/go-build go test ./internal/anthropicoauth/... ./internal/provider/copilot/... ./internal/credentialworker/... -count=1 -race`.
   - For audit outcome round-trip, prefer an existing Postgres integration helper if configured. If no DB is available, verify the deterministic store adapter path inserts/persists the exact string and report that no live DB round-trip was possible.
   - Run `codex exec review --uncommitted --full-auto --sandbox read-only` once after staging is intentionally skipped? Owner said do not `git add`; since #8 normally requires staged intended diff, use the closest uncommitted read-only review without staging if CLI permits, otherwise report the mismatch. Run a second review only for S0/S1 or material behavior/test changes.

## Clean-Room Boundary

No LGPL/AGPL reference projects will be read. This implementation uses only HUAKAI internal code and the Owner-provided behavior contract. No upstream source identifiers, schemas, comments, tests, or file structures are copied.

## Assumptions

- Migration `0055` already allows the four new audit outcomes and no migration edit is required.
- `transient_error` remains an internal classifier result for retry semantics and is intentionally not stored as a new audit outcome in this slice.
- The wording "see commit" in the close marker is literal for this no-commit worktree unless Owner later provides a commit SHA.

## Cross-Discussion Status

This is Codex's independent plan. I did not read a Claude sibling plan for this descriptor. Per the project parallel-plan rule, execution should wait for Owner approval or a synthesized plan before implementation.
