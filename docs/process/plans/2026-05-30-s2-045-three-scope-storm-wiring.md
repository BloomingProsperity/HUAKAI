# S2-045 — Wire three-scope refresh-storm control into the credential worker

**Date:** 2026-05-30  **Wave:** 2 (credentialacq cluster tail)  **Owner decision:** Option B (approved via AskUserQuestion 2026-05-30)

## Finding (verified at current HEAD, not trusted from audit)

A07 spec promises three-scope refresh-storm control (account / provider-endpoint / global). The
complete in-memory algorithm exists as `internal/gateway.StormPolicy` + `TokenBucket`
(`internal/gateway/storm_policy.go`, `token_bucket.go`) but has **zero production callers** (tests
only). The live refresh path `internal/credentialworker/scheduler.go:188` calls
`auth.StormController` through the narrow `stormAcquirer` interface
(`internal/credentialworker/types.go:20-22`), which is **account-scope only**.
`auth.StormController.AcquireProviderEndpoint` / `AcquireGlobal`
(`internal/auth/storm_controller.go:59-65`) hardcode `ErrStormScopeNotImplemented`.

Impact: cross-account stampedes on a vendor OAuth token endpoint are not throttled, and the
endpoint/global metrics A07 promises are absent. Severity S2 — the account-scope DB budget already
prevents the worst case (same-account thundering herd).

## Reference comparison (clean-room, file:line evidence; CLAUDE.md #15)

| Project | account scope | endpoint scope | global scope | durability |
|---|---|---|---|---|
| sub2api@91da8159 | in-mem mutex + **Redis SetNX** lock (`backend/internal/service/oauth_refresh_api.go:56-64`, `repository/gemini_token_cache.go:41-44`) | none | none | account is distributed (Redis); rest absent |
| CLIProxyAPI@21fad9db | in-mem time-backoff marker (`sdk/cliproxy/auth/conductor.go:4122-4138`) | none | in-mem worker-pool concurrency cap of 16 (`conductor.go:72-74`) | all process-local |
| new-api@20d3e73 | in-mem `atomic.Bool` task-overlap guard only (`service/codex_credential_refresh_task.go:56-59`) | none | none | process-local; essentially no defense |

**Conclusion:** HUAKAI's existing account scope (Postgres budget, durable + cross-replica) is
parity-or-stronger than all three. **No reference implements endpoint or global storm budgets
durably.** So endpoint+global is an enhancement beyond every reference, not a regression.

## Chosen design — Option B: keep DB account scope + add in-memory endpoint/global

No schema change (not HIGH-risk, no PROD migration gate). All edits in **non-frozen** packages
(`internal/auth`, `internal/credentialworker`, `cmd/gateway`); `internal/gateway` is frozen and is
the request-path layer, so the worker must not depend on it — the in-memory layer lives in
`internal/auth` beside `StormController` (its own small token bucket; gateway's `TokenBucket` is a
different layer's primitive and reusing it would invert layering).

### Files (all non-frozen)

1. **NEW `internal/auth/storm_scope.go`** — unexported monotonic token bucket (refill-on-demand,
   `tryAcquire(now)` / `refund(now)`, mirrors the proven A07.1 semantics) + a per-endpoint
   `sync.Map` of buckets + one global bucket. Disabled (admit-all) when its rate ≤ 0.
2. **`internal/auth/storm_controller.go`** — add endpoint/global limiter fields + `now func()`;
   implement real `AcquireProviderEndpoint`/`AcquireGlobal` (token-bucket admit → `(refund, "", nil)`;
   deny → `(nil, OutcomeStormBudgetExhausted, nil)`); add `NewStormControllerWithScopeBudget(queries,
   StormScopeConfig)`; keep `NewStormController(queries)` delegating with scopes **disabled**
   (backward-compatible — preserves today's behavior for the 3 existing callers).
3. **`internal/auth/auth_test.go` + NEW `internal/auth/storm_scope_test.go`** — the old
   `...DeferredScopesFailClosed` test asserted `ErrStormScopeNotImplemented`; that premise is now
   obsolete (scopes implemented). Replace with: unconfigured→admit, configured-exhausted→deny,
   nil-safe no-panic. New tests: endpoint deny, global deny refunds endpoint, refill over time.
4. **`internal/credentialworker/types.go`** — extend `stormAcquirer` with the two scope methods
   (signatures already match `StormController`).
5. **`internal/credentialworker/scheduler.go`** — `processAccount`: acquire account (DB, deferred
   release) → endpoint token → global token, in order. On endpoint deny: release account, audit
   `provider_endpoint`. On global deny: refund endpoint, release account, audit `global`. On success
   or fn-error: tokens stay consumed (failed attempt must not reopen the storm window — A07 rule);
   account slot released on defer either way.
6. **`internal/credentialworker/scheduler_test.go`** — update `stormSpy` to implement the two new
   methods (default admit); add denial tests asserting the audit scope label + that refresh is
   skipped + endpoint refund on global deny (mutation-checked).
7. **`cmd/gateway/wiring.go`** — parse `HUAKAI_STORM_{ENDPOINT,GLOBAL}_{RATE,BURST}` env (float;
   0/unset = scope disabled), pass to `NewStormControllerWithScopeBudget`. Fail-loud on malformed.

### Why unconfigured = admit (not the gateway primitive's "0 = never admit")

The endpoint/global layer is an **opt-in additive** throttle. The critical same-account storm
prevention is the account DB budget, always on. Endpoint/global default OFF until an operator sets
rates (same shape as S2-109: no config → safe passthrough). This is not a regression — there was
never a live endpoint/global protection to lose, and `ErrStormScopeNotImplemented` only ever guarded
a half-built scope from silently admitting; the scope is now fully built.

### Limitation (documented)

Endpoint/global buckets are process-local, so with N gateway replicas the effective endpoint rate is
N×configured. This is the same trade-off CLIProxyAPI accepts — its refresh-worker cap is an in-memory
worker pool, not a durable/cross-replica budget (CLIProxyAPI@21fad9db:sdk/cliproxy/auth/conductor.go:72-74).
Account scope remains DB-durable/cross-replica. Cross-replica endpoint/global = future roadmap
(Option A: durable budget tables).

## Verification
- `cd backend && go build ./...`; `go test ./internal/auth/... ./internal/credentialworker/...
  ./cmd/gateway/... -race -count=1`.
- Every new test mutation-checked (inject the defect → red).
- codex per-commit review (`codex exec review --uncommitted -m gpt-5.5 -c
  model_reasoning_effort=xhigh`), gate = no unresolved S0/S1.
