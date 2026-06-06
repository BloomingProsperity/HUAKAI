# 2026-06-06 credentialacq finalize race
| Owner directive | "CRED-F3 cancel/finalize 竞态发已撤销凭据(SECURITY/AUTH S1, 审计真缺陷, 极保守)" |
| Scope | In: `backend/internal/credentialacq/session_store.go`, existing credentialacq session-store tests, and this plan artifact. Out: `/home/ubuntu/refs`, frozen packages, schema, auth core, billing, quota, runtime dependencies, commits. |
| Success criteria | `Cancel` refuses consumed flows with `ErrFlowReplay`; `MarkFinalized` cannot overwrite cancelled/expired/failed flows; normal and idempotent finalize paths remain green; required local gates run with recorded output. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Credential acquisition flow state transitions only. Failure could block legitimate finalize/cancel flows or leave a cancelled flow finalizable. |
| Failure modes | Non-discriminating fake tests: add real-PG SQL test with mutation notes. Over-broad finalize guard: include normal and idempotent controls. File growth: keep edits surgical and avoid new frozen-package files. |
| Decision points | No Owner sign-off needed unless the fix requires schema changes, credential revocation implementation, or a new runtime dependency. Those are out of scope and must stop. |
| Pre-execution checklist | 1. Read only HUAKAI repo files. 2. Confirm existing SQL predicates and test harness. 3. Write failing tests before production changes. 4. Run targeted RED. 5. Apply minimal guards. 6. Run `gofmt`, `go build ./...`, `go vet ./internal/credentialacq/...`, `go test ./internal/credentialacq/... -count=1`. |

Concrete execution order:

1. Add real-PG coverage for consumed-then-cancel and cancelled-then-finalize, plus normal/idempotent finalize controls.
2. Run the targeted real-PG test command once to confirm the new test catches the current bug when Postgres is available; if `HUAKAI_DATABASE_URL` is unset, record the skip and rely on PM's integration gate.
3. Add `AND consumed_at IS NULL` to `Cancel`.
4. Add `AND cancelled_at IS NULL` and `AND status NOT IN ('cancelled', 'expired', 'failed')` to `MarkFinalized`, with no-row re-fetch returning `ErrFlowReplay` for terminal rows.
5. Align the local fake store with the same guards so non-PG package tests model the SQL contract.
6. Run the required local gates. Do not commit.
