# 2026-05-25 Hermes Phase-1 Slice 1.3 Discriminating Tests Codex

| Owner directive | "你是 codex 实施 lane,执行 Hermes phase-1 Slice 1.3: discriminating tests + mutation 自检。" |
| Scope | In: add focused discriminating tests for Hermes audit sanitization, tenant scoping, profile-delete in-use guard, runner HMAC canonical coverage, runner freshness rejection, and HTTP atomic audit behavior. Out: E2E tests, production benchmark, frontend tests, Slice 2 streaming behavior, git add/commit. |
| Success criteria | New tests include one-line regression intent and mutation-check comments; fixtures are discriminating for secret disclosure, tenant leakage, data corruption, and replay/freshness risks; `go build ./...`, `go vet ./...`, and `go test ./internal/hermes/... ./internal/hermeshttp/... -count=1 -race` pass with `GOCACHE=/tmp/huakai-go-build-cache`. |
| Time estimate | 2-3 hours wall clock; one Codex implementation pass plus verification. |
| Blast radius | Low-to-medium: test files in non-frozen Hermes packages, plus possible small test seam only if existing code cannot expose the required behavior. No production DB schema, auth core, billing ledger, quota enforcement, deployment script, or frozen package changes. |
| Failure modes | Weak fixture that still passes under broken code: write comments naming the exact mutation and assert correct/broken output divergence where practical. Over-mocking store behavior: capture sqlc parameter structs and transaction store usage. Runner freshness not Go-native: test the checked-in runner verifier through a subprocess without adding runtime dependencies, or record the gap if unavailable. Race/build failures from test helper duplication: keep helpers package-local and run targeted race test before full checks. |
| Decision points | None expected. Stop for Owner confirmation only if a required fix would touch high-risk files such as schema, auth core, billing/quota logic, deployment scripts, or real secrets. |
| Pre-execution checklist | 1. Read `docs/RULES.md` Owner Start Gate and AGENTS test/package rules. 2. Inspect existing Hermes service, runner client, HTTP handler, and current tests. 3. Confirm new file targets are not frozen packages. 4. Add tests first and run targeted red/green where feasible. 5. Patch only minimal test seams if compilation or required coverage demands it. 6. Run requested verification commands with explicit `GOCACHE`. 7. Report mutation-comment examples, PASS evidence, and `git diff --stat` in Chinese. |

## Target Files And Package Freeze Check

| File | Package | Frozen? | Purpose |
| --- | --- | --- | --- |
| `backend/internal/hermes/audit_sanitize_test.go` | `internal/hermes` | No | Redaction and non-sensitive preservation tests. |
| `backend/internal/hermes/tenant_isolation_test.go` | `internal/hermes` | No | Tenant-scoped settings and audit parameter tests. |
| `backend/internal/hermes/profile_delete_test.go` | `internal/hermes` | No | Profile in-use delete guard tests. |
| `backend/internal/hermes/runner_canonical_hmac_test.go` | `internal/hermes` | No | Canonical HMAC dimensions and freshness rejection coverage. |
| `backend/internal/hermeshttp/atomic_audit_test.go` | `internal/hermeshttp` | No | Handler/service transaction atomicity tests under audit failure. |
| `backend/cmd/gateway/openapi_consistency_test.go` | `cmd/gateway` | No | Read only unless Hermes path coverage is missing. |

## Execution Order

1. Add audit sanitization tests with explicit secret vs `[REDACTED]` assertions and unchanged safe fields.
2. Add tenant isolation tests that capture `TenantID` in `UpsertSettingsParams`, `GetSettingsParams`, and `InsertAuditEventParams`.
3. Add profile delete tests that prove `ProfileInUse` prevents deletion and unused profiles delete normally.
4. Add runner canonical HMAC tests comparing signatures across method, query, tenant, and user changes; add freshness rejection coverage against the runner verifier where practical.
5. Add Hermes HTTP atomic audit tests using transaction-aware stubs to prove audit failure prevents commit for enable and profile create.
6. Run targeted package tests, then full requested build/vet/race checks.
