# 2026-05-31 S1-024 refresh health filter
| Owner directive | "Do ONE task now... S1-024 ... 凭据刷新调度忽略 provider-account 健康吊销/冷却" |
| Scope | In: refresh scheduling read filters, locked refresh reread guard, discriminating tests for revoked/future-cooldown exclusion and healthy eligibility. Out: S2-062 health-state write FSM changes, schema changes, auth/billing/quota core redesign, new runtime dependencies. |
| Success criteria | Revoked provider accounts and accounts whose `health_state_until` is still in the future do not enter refresh scans; locked reread refuses an account after health is revoked before refresh; healthy accounts still refresh; tests fail if health predicates are removed. |
| Time estimate | 60-90 minutes wall clock; one Codex worker pass plus bounded self-review. |
| Blast radius | Credential refresh scheduler may skip too much or too little provider capacity; locked reread changes can turn previously refreshable records into not-found errors when health is unsafe. |
| Failure modes | Predicate too broad blocks expired transient cooldown recovery; predicate too narrow keeps hammering revoked/risk accounts; test fixture passes without checking the destructive refresh call; generated sqlc file drifts from query source; integration PG tests may skip if `HUAKAI_DATABASE_URL` is unset. Mitigation: use paired revoked/cooldown/healthy fixtures, assert returned IDs and refresh calls, update query source plus generated output, run targeted tests. |
| Decision points | No Owner sign-off expected: no schema, no dependencies, no secrets, no frozen-package new files. If a schema change or auth-core redesign becomes necessary, park the task. |
| Pre-execution checklist | Read `.coordination/DISPATCH.md`, `CLAUDE.md` #8/#13/#14, S2-062 health write-side code, scheduler/list query paths, and locked `LoadForRefresh` path; claim task with `task.sh start`; create isolated worktree because the primary checkout has unrelated dirty coordination files. |

## Concrete Execution Order

1. Add RED tests in existing non-frozen package files:
   - `backend/internal/credentialworker/audit_tx_pg_test.go` for real-PG `AccountCredentialRefreshQueries.ListAccountsForRefresh` revoked/future-cooldown exclusion and healthy control.
   - `backend/internal/credentialworker/mode_refresh_test.go` or an existing suitable test file for locked reread skip before adapter refresh.
2. Run targeted tests and confirm the new tests fail against current code for the intended reason.
3. Implement the smallest read-side predicates:
   - `backend/internal/credentialworker/mode_refresh.go` production query.
   - `backend/internal/credentialstore/postgres_store.go` locked `LoadForRefresh` query.
   - `backend/sql/queries/pool_accounts.sql` plus generated `backend/internal/db/billing/pool_accounts.sql.go` for the legacy sqlc query.
4. Run targeted tests and `go test ./internal/credentialworker ./internal/credentialstore` from `backend/`.
5. Stage intended diff, run bounded self-review, normalize any findings, commit, push `HEAD:work/s1-024`, and mark task `review` with branch + SHA + self-review result.

## Package/File Structure Check

No new files are planned under frozen packages. The planned code/test edits are in existing files under `internal/credentialworker`, `internal/credentialstore`, `internal/db/billing`, and `backend/sql/queries`; none are listed as frozen in CLAUDE.md #13.
