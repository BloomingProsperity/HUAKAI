# 2026-05-28 S1-029 Streaming Provisional Reconcile

| Owner directive | `TASK: IMPLEMENT audit fix S1-029 (streaming provisional charge + reconcile worker) in this worktree. Money/audit path — be precise. ... Do NOT git add/commit/push.` |
| Scope | In: HUAKAI-internal Go backend only; provisional output-only streaming cost fallback; sqlc queries for pending reconciliation finalization; new `backend/internal/settlementreconcile` worker; gateway lifecycle wiring; discriminating tests and verification. Out: DB migrations, reference-project source reads, fake authoritative upstream fetches, commits/pushes. |
| Success criteria | Stream settle with no reported usage and delivered tokens records nonzero output-only provisional cost, `usage_source=inferred`, and `pending_reconciliation=true`; aged pending `usage_records` are finalized after grace while fresh rows stay pending; generated sqlc diff is limited to the new queries with v1.27.0 comments unchanged; requested build/vet/test/integration commands are run and reported. |
| Time estimate | 2-4 hours wall clock; one Codex implementation lane. |
| Blast radius | Money/audit path: `usage_records.actual_cost`, `usage_records.pending_reconciliation`, gateway worker lifecycle, generated billing query interface. |
| Failure modes | Overcharging by estimating input tokens: avoided by pricing output tokens only. Clearing pending too early: avoided by 5m grace and integration test with a fresh pending row. Accumulating pending forever: avoided by periodic finalize-after-grace worker. sqlc churn: use pinned `$(go env GOPATH)/bin/sqlc` v1.27.0 only. Frozen-package violation: edit existing `gatewayhttp` files only; new files only under non-frozen `settlementreconcile`. |
| Decision points | None currently needing Owner sign-off: Owner already specified formula, worker policy, cadence, grace, batch, and clean-room boundary. If a required DB schema change or new runtime dependency appears necessary, stop for Owner confirmation. |
| Reference-project comparison | Not applicable for this execution plan: Owner explicitly constrained this task to HUAKAI-internal code and forbade reference-project source reads. No new Owner decision option is being surfaced here. |
| Pre-execution checklist | Read `AGENTS.md` and `CLAUDE.md` rules #8/#13/#14; confirm linked worktree and clean status; inspect existing streaming settle, pricing, sqlc, worker lifecycle, and integration seed patterns; write tests before production changes; avoid `git add/commit/push`. |

## File Scope

- Modify existing frozen-package file only: `backend/internal/gatewayhttp/chat_completions_stream.go`.
- Modify existing frozen-package test file only: `backend/internal/gatewayhttp/chat_completions_pricing_test.go`.
- Modify existing SQL owner file: `backend/sql/queries/billing_settle.sql`.
- Regenerate generated billing sqlc files under `backend/internal/db/billing/`.
- Create non-frozen package files:
  - `backend/internal/settlementreconcile/reconciler.go`
  - `backend/internal/settlementreconcile/reconciler_integration_test.go`
- Modify lifecycle wiring:
  - `backend/cmd/gateway/wiring.go`
  - `backend/cmd/gateway/lifecycle.go`

## Execution Order

1. Add a failing gatewayhttp unit test that constructs `streamingCompletionEvent` with zero reported usage, positive `DeliveredTokenCount`, and an output rate; assert `ActualCost = delivered_tokens * output_micro_usd / 1_000_000`, `UsageSource == inferred`, and `PendingReconciliation == true`.
2. Run the targeted gatewayhttp test and confirm RED for the current missing provisional branch.
3. Implement the minimal provisional output-only fallback in `chat_completions_stream.go`, keeping cache-cost wiring unchanged and adding the required one-line Chinese rationale comment.
4. Run the targeted gatewayhttp test and confirm GREEN.
5. Add `SelectPendingReconciliationForFinalize` and `FinalizePendingReconciliation` to `billing_settle.sql`, tenant-scoping the update.
6. Run pinned sqlc v1.27.0 generation, installing exactly v1.27.0 only if missing.
7. Add `settlementreconcile` worker mirroring `billing.LeaseSweeper`: default cadence 60s, grace 5m, batch 100, start/stop loop, batch reconciliation, continue-on-error, structured batch log, and future true-up extension comment.
8. Add an `integration_pg` test that seeds one aged pending row and one fresh pending row, runs `reconcileOnce`, then asserts only the aged row is finalized.
9. Run the targeted integration test and confirm GREEN; later prove RED by temporarily disabling the finalize/update logic and restoring it.
10. Wire the worker in gateway runtime start/stop.
11. Run requested verification:
    - `go build ./...`
    - `go vet ./internal/gatewayhttp/... ./internal/settlementreconcile/... ./internal/billing/... ./cmd/gateway/...`
    - `go test ./...`
    - `go test -tags integration_pg -count=1 ./internal/settlementreconcile/... ./internal/billing/...`
12. Produce requested final evidence without staging or committing: changed files, `diff -U2` for non-generated files, generated-file list with no version-comment churn confirmation, verify outputs, RED-GREEN proofs, and Chinese Owner summary.

## Execution Note

During the integration run, PostgreSQL raised `usage_records is append-only: UPDATE` from migration `0039_money_path_append_only_triggers`. Because this task explicitly forbids migrations and money-path trigger changes require Owner confirmation, execution used an append-only safe equivalent: finalization writes a zero-delta `usage_record_reconciliation_events` marker with `reconciliation_source='finalize_after_grace'`, and subsequent worker scans exclude rows that already have that marker. Physical `usage_records.pending_reconciliation=false` remains an Owner-confirmation item if required.
