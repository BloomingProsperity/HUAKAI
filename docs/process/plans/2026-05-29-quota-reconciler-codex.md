# 2026-05-29 quota reconciler

| Field | Plan |
| --- | --- |
| Owner directive | "实现 HUAKAI 配额 reconciler(补偿队列消费者)。greenfield,只在 backend/internal/quota 新增文件...不 commit、不 push。" |
| Scope | In: `backend/internal/quota/reconciler.go` and `backend/internal/quota/reconciler_integration_test.go`. Out: cmd wiring, ticker loop, tenant enumeration, lease sweep, migrations, billing/auth/quota schema changes. |
| Success criteria | Reconciler drains due jobs for one tenant and replays settle/release/cache-hit through existing `Service`; failed jobs back off; already-terminal idempotency completes job; six true-PG tests discriminate the requested mutations. |
| Time estimate | 1 implementation session; most time in test fixture alignment and PG verification. |
| Blast radius | Medium: quota finalization orchestration only. No schema or billing ledger changes. |
| Failure modes | Wrong replay branch can complete without quota movement; mitigate with window/status assertions. Future jobs could be processed if due filtering is bypassed; mitigate with future-job test. Failed jobs could be completed; mitigate with attempt/next_run_at assertion. |
| Decision points | Use reservation `PredictedCost` as settle replay actual-cost proxy per Owner decision; use existing legal release reason `upstream_error` because `reconciliation_release` is not currently allowed. |
| Pre-execution checklist | Read quota Service/PGStore/tests; write tests first; verify red; implement only new files; run gofmt; run build and true-PG quota tests when DB is available. |
| Concrete execution order | 1. Add integration tests. 2. Run targeted test expecting missing Reconciler compile failure. 3. Add reconciler implementation. 4. Run targeted tests. 5. Run `go build ./...` and `go test -tags=integration_pg ./internal/quota/... -count=1`. |

Notes:
- This plan artifact is required by AGENTS Plan-Before-Execute. It is outside the Owner's implementation file scope, so it is intentionally limited to process documentation and not committed.
