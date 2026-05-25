# 2026-05-21 PR2 Attempt Error Taxonomy Plan

| Owner directive | "HUAKAI 方向 1 Phase 1，PR1 已完成提交(c4d85f7）。现在执行 PR2。" |
|---|---|
| Scope | In: attempt-level retry taxonomy, transport error class mapping, attempt outcome type definitions, taxonomy unit tests, router/gateway end-class drift guard. Out: handler retry loop, billing/claim/settle changes, DB schema/SQL, runtime dependencies, git operations. |
| Success criteria | `backend/internal/gateway` exposes `TransportErrorClass`, `CredentialRefreshIntent`, `AttemptRetryDecision`, and classification helpers that converge HTTP classification, local dispatch errors, and future transport classes into one decision. `backend/internal/gatewayhttp` has PR3-ready attempt input/outcome structs without executing attempts. Unit tests cover the full PR2 taxonomy table including synthesis override for 401. Router retryable end classes are guarded against drift from `gateway.StreamEndClass`. Required build and race tests pass with real output. |
| Time estimate | 1.5-2.5 hours wall clock in this session. |
| Blast radius | Medium: new exported gateway classification API can affect future retry behavior; PR2 should not alter live handler behavior except optional small helper integration. Router drift guard is test-only unless package visibility requires a tiny exported helper. |
| Failure modes | Misclassifying 401 could create unbounded auth failover later; mitigate with explicit `AuthFailure` decision flag and tests. Treating local config/protocol errors as retryable could hide real deployment problems; mitigate with conservative default and tests. TLS/url wrapping can be easy to miss; mitigate with `errors.As` chain tests. Router/gateway drift guard could force a router import cycle if implemented directly; mitigate by adding a router test helper instead of importing gateway from router production code. |
| Decision points | No high-risk Owner sign-off expected. If implementation requires touching billing, schema, auth core, quota enforcement, runtime dependencies, or deleting files, stop and ask Owner. If `chat_completions_error.go` integration expands beyond helper usage, defer to PR3. |
| Pre-execution checklist | 1. Read `docs/RULES.md` owner gate and risk rules. 2. Read synthesis §3 override-1 and codex plan §5.1/§8/§11. 3. Inspect existing `gateway.Classify`, `StreamEndClass`, router retryable classes, and handler error helpers. 4. Write failing PR2 tests first. 5. Implement minimal taxonomy/types. 6. Run focused tests, then required build and race test commands. |

## Concrete Execution Order

1. Add failing tests in `backend/internal/gateway/attempt_error_test.go` for HTTP 5xx/529/504, 429 with `Retry-After`, 401 synthesis override, 403, client 4xx/413, unknown upstream, local timeout, network timeout, TLS/x509/url errors, header timeout, and nonretryable local config errors.
2. Add failing router drift guard in `backend/internal/router/router_test.go` through a package-local helper returning retryable strings; add an external consistency test under a package that can import both router and gateway without creating an import cycle.
3. Run focused tests and confirm RED failures.
4. Implement `backend/internal/gateway/attempt_error.go` with English identifiers and concise Chinese comments where the retry/auth-budget semantics need context.
5. Add `backend/internal/gatewayhttp/chat_completions_attempt.go` with PR3-ready type definitions and narrow helper methods only; do not implement `runAttempt`.
6. Add the router package test helper or equivalent drift guard support without making router production import gateway.
7. Run focused tests until green.
8. Run required verification commands:
   - `GOCACHE=/tmp/go-cache go build -C /home/codex/HUAKAI/backend ./...`
   - `GOCACHE=/tmp/go-cache go test -C /home/codex/HUAKAI/backend ./internal/gateway/... ./internal/gatewayhttp/... ./internal/router/... -race -count=1 -timeout 180s`

## Self-Review

- No placeholders or speculative upstream claims.
- No billing, schema, SQL, runtime dependency, or git operation.
- 401 follows synthesis override: retryable before delivery, switch account, auth-budget observable through the decision, no channelhealth degradation semantics in PR2.
- 403 remains terminal for failover.
