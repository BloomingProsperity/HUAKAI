# 2026-06-06 Alert Eval Loop Codex Plan
| Owner directive | "Wire the alerting engine to live metrics so rules actually FIRE (branch fix/alert-eval-loop)." |
| Scope | Add a default-off alert evaluator background loop, tenant enumeration for enabled rules, a narrow live metric source from existing HUAKAI metrics, gateway wiring, and discriminating unit tests. Out of scope: migrations, new public routes, commits, `/home/ubuntu/refs`, frozen-package new files, integration/socket runs. |
| Success criteria | `alerting.Service.EvaluateRules` has a production caller when `HUAKAI_ALERTING_EVAL_ENABLED=true`; default config leaves the loop off; scheduler evaluates only tenants with enabled rules on ticks; context cancellation stops promptly; tests fail under the listed mutations; requested build/vet/unit commands are run and reported. |
| Time estimate | 60-90 minutes wall clock; one Codex work unit. |
| Blast radius | Alerting package store interface, Postgres alert-rule read query, in-process metrics snapshot source, gateway startup/shutdown wiring, config env parsing. |
| Failure modes | Store interface change can break alerting implementations: update memory and Postgres stores together and run package tests. Metric source can expose wrong names: keep names identical to existing OTel bridge metric names and test the snapshot. Scheduler goroutine can leak: test cancellation and wire runtime stop. Default-off can regress: config and wiring tests assert disabled behavior. |
| Decision points | Owner confirmation needed only for high-risk changes; this plan avoids migrations, auth, billing ledger, quota enforcement, secrets, deployment, and new runtime dependencies. |
| Pre-execution checklist | Confirm current worktree/branch; read `internal/alerting/service.go`, stores, `cmd/gateway/wiring.go`, `internal/otelbridge/provider.go`, `routes_alerting.go`; write failing scheduler/config/wiring tests first; verify RED; implement minimally; run package tests, build, and vet. |

## Concrete Execution Order

1. Add failing unit tests in `backend/internal/alerting` for tick evaluation, tenant filtering, and context cancellation using fake ticker, fake tenant lister, fake metric source, and fake evaluator.
2. Add failing unit tests in existing config/gateway test files for default-off alert eval config and disabled wiring not starting a runnable.
3. Run focused tests to verify RED.
4. Implement `backend/internal/alerting/scheduler.go` with `MetricSource`, enabled-tenant lister/evaluator interfaces, configurable ticker, default 60s interval, per-tenant snapshots, and clean `ctx.Done()` exit.
5. Add `ListTenantsWithEnabledRules` to the alerting store contract and implement it in memory and Postgres stores without schema changes.
6. Expose a minimal `otelbridge` expvar-backed metric source from existing bridged metric specs.
7. Extend existing env config with `HUAKAI_ALERTING_EVAL_ENABLED` default false and `HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS` default 60.
8. Wire gateway startup to construct the alerting store/service/scheduler and start it only when the flag is true; retain a stop hook for graceful shutdown.
9. Run focused tests, then requested build/vet/unit commands.
