# 2026-05-18 refund replica and verify degrade
| Owner directive | "修 P2#3 + P2#4: refund 加 replica enqueue + verify derivation 优雅降级." |
| Scope | In: `backend/internal/billing/settler.go`, `backend/internal/billing/*test.go`, `backend/internal/gatewayhttp/cost_receipt_handler.go`, `backend/internal/gatewayhttp/*test.go`. Out: frontend, Rust, `audit/mismatch_detector.go`, `cmd/gateway/main.go`, `audit/refund_worker.go`, reference project source. |
| Success criteria | Refund writes a billing event replica intent in the same transaction when `WithReplicaTarget` is configured; receipt verify returns HTTP 200 with `valid=true` and `verdict=unknown` when signature is valid but derivation is unavailable; requested build and tests pass or failures are reported honestly. |
| Time estimate | 20-35 minutes wall clock, one Codex implementer lane. |
| Blast radius | Billing refund transaction and receipt verification response semantics. |
| Failure modes | Replica enqueue can fail and roll back refund like settle/abort; verify degradation could hide signature failures, mitigated by only degrading after `valid` is true; tests may require existing DB test setup. |
| Decision points | No Owner sign-off expected unless requested changes require high-risk files, schema changes, new dependencies, or forbidden files. |
| Pre-execution checklist | Read target regions; preserve dirty worktree changes; make minimal patches; add AT tests; run requested Go build and race tests with `GOCACHE=/tmp/go-cache`; report residual risk. |
