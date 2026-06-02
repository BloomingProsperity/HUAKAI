# 2026-05-28 quota B2b review fixes

| Owner directive | "修配额 B2b review 发现:1 个 S1(必修)+ 1 个 S2(顺手修)。全部在 backend/internal/quota" |
| Scope | In: `backend/internal/quota/service.go`, `backend/internal/quota/service_settle.go`, quota tests. Out: migrations, billing, wiring, frozen packages, commit/push. |
| Success criteria | Reserve snapshots persist concrete enforce request/cost windows; Settle/Release/CommitCacheHit finalize the snapshotted windows; request audit amounts use request units; new cross-window PG test fails before the fix and passes after; quota PG tests and `go build ./...` pass. |
| Time estimate | 1-2 hours wall clock; one Codex session. |
| Blast radius | Quota reserve/finalization accounting for request and cost windows; audit amount semantics for finalization events. |
| Failure modes | Stranding reserved holds if snapshot parsing rejects valid records; double-applying windows if duplicate records are mishandled; audit metric/value mismatch; brittle test that does not distinguish W0 from W1. |
| Decision points | No Owner sign-off needed unless the fix requires schema/migration, billing/wiring changes, or new runtime dependencies. |
| Pre-execution checklist | Read existing quota reserve/finalization paths; add discriminating PG regression test; verify red; implement snapshot enrichment and snapshot-based finalization; run targeted test, full quota PG tests, and backend build. |

Concrete execution order:

1. Inspect `service.go`, `reservation.go`, `service_settle.go`, and existing quota PG fixtures.
2. Add `TestServiceSettle_CrossWindowFinalizationReleasesReservedWindow` to existing quota PG tests.
3. Run the targeted PG test against current behavior and confirm it fails because W0 remains reserved or W1 receives settlement.
4. Update reserve/reactivate to build policy snapshots after evaluation, enriching only enforce request/cost records with concrete window bounds from `evaluated.enforceWindows`.
5. Update finalization to parse reservation snapshots and apply settlement/release to exact snapshot windows. Missing concrete enforce request/cost windows return an error so reconciliation is queued by existing finalization error handling.
6. Fix request-metric finalization audit rows to use `reservation.ReservedUnits` and request settled units.
7. Run targeted PG test, all quota PG tests, and `cd backend && go build ./...`.
