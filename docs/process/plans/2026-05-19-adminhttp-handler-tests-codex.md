# 2026-05-19 adminhttp handler tests codex
| Owner directive | "给 backend/internal/adminhttp/ 真实 handler 包加测试 (audit list MED)." |
| Scope | In: `backend/internal/adminhttp/` test coverage for api key admin handlers. Out: reference reverse-proxy source, frontend, Rust, vendor/boring, audit, billing, community, proto, schema, auth core, quota enforcement, production handler behavior unless a compile-only support change is unavoidable. |
| Success criteria | Add `TestAT_ADMIN_001_*` coverage for missing auth 401, insufficient role / wrong scope 403, cross-tenant isolation, happy path 200/201, and validation 400; `go build ./...` passes; `go test ./internal/adminhttp/... -race -count=1 -timeout 180s` passes. |
| Time estimate | 30-60 minutes wall clock, one Codex executor lane. |
| Blast radius | Test-only intended. If tests use a real Postgres fixture, writes are isolated by generated names and cleanup hooks. |
| Failure modes | Concrete dependencies in `AdminAPIKeysDeps` may require integration-style tests instead of stubbed unit tests; mitigate by reusing existing `HUAKAI_DATABASE_URL` skip pattern if no DB is configured. Race/build may expose unrelated package issues; report exact command output. |
| Decision points | Stop for Owner confirmation if implementation requires schema changes, production auth/billing/quota edits, new runtime dependencies, destructive commands, or touching prohibited directories. |
| Pre-execution checklist | 1. List adminhttp files. 2. Read actual handler and local httptest patterns. 3. Reuse existing admin/db helpers where possible. 4. Add only adminhttp tests. 5. Run requested build and targeted race test. |
