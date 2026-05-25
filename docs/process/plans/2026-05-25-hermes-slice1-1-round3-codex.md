# 2026-05-25 Hermes Slice 1.1 Round 3 Codex Fix

| Owner directive | "你是 codex Round 3 fix,修 Hermes Slice 1.1 的 4 个 S1 (1 个 S2 ticket 化)。" |
| Scope | In: Hermes settings/profile mutation audit atomicity, chat response streaming flush, profile delete in-use guard, runner HMAC canonical string, focused tests, one deferred S2 ticket. Out: runner-side signature verification, schema migrations, billing/quota/auth core, git add/commit. |
| Success criteria | Requested commands pass: `cd backend && GOCACHE=/tmp/huakai-go-build-cache go build ./...`; `cd backend && GOCACHE=/tmp/huakai-go-build-cache go vet ./...`; `cd backend && GOCACHE=/tmp/huakai-go-build-cache go test ./internal/hermes/... ./internal/hermeshttp/... ./cmd/gateway/...`. |
| Time estimate | 1-2 hours wall clock; one Codex repair pass plus verification. |
| Blast radius | Medium: Hermes service and HTTP handlers touch transactional mutation behavior; gateway wiring changes only to provide the existing pgx pool to Hermes. No new runtime dependency and no frozen-package new file. |
| Failure modes | Transaction support could break tests using stubs; mitigate by preserving `NewService(store)` and adding a tx-enabled constructor for production. Audit rollback could return the wrong status; mitigate with handler-level regression. Profile delete race could remain if check and delete are split; mitigate by doing owner check, in-use check, delete, and success audit in one transaction. Streaming copy could swallow read/write errors; mitigate by returning copy errors to the handler path where practical. |
| Decision points | None expected. High-risk files (`LICENSE`, real secrets, auth core, billing ledger, quota enforcement, database schema, deployment scripts) are not in scope. |
| Pre-execution checklist | 1. Inspect Hermes service/handler/sqlc/test patterns. 2. Add red tests for each S1 where local test seams exist. 3. Patch service transaction helpers and handler calls. 4. Run sqlc generation after query change. 5. Run requested checks. 6. Report concise Chinese summary and `git diff --stat`. |

Concrete execution order:
1. Add `ProfileInUse` sqlc query and regenerate Hermes db code.
2. Add service tests for atomic audit rollback and profile in-use delete guard.
3. Add handler/chat tests for per-chunk flushing and profile-in-use 409 shape.
4. Update runner client signature test for method/path/query canonical string.
5. Implement transaction-enabled service constructor and atomic mutation APIs.
6. Update settings/profile handlers to call atomic APIs and keep failure audit for rejected operations.
7. Replace proxy response `io.Copy` with chunked flush loop.
8. Update production wiring to use the tx-enabled Hermes service constructor.
9. Run the requested build, vet, and package tests.
