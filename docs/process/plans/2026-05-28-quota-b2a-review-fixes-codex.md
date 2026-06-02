# 2026-05-28 Quota B2a Review Fixes Codex Plan

| Owner directive | "修配额子系统切片 B2a 的 review 发现:3 个 S1(必修)+ 1 个 S2(顺手便宜修)。全部在 backend/internal/quota,greenfield,不碰 billing/wiring/gatewayhttp/gateway/proto/migration。不 commit/push。" |
| --- | --- |
| Scope | In: fix Reserve request usage accounting, concurrent same-claim idempotency, PostgresStore transaction contract honesty, and concurrency-deny audit payload inside `backend/internal/quota`. Out: migrations, generated DB schema, billing, wiring, gatewayhttp, gateway, proto, auth, commits, pushes. |
| Success criteria | New true-PG service tests fail on the reviewed defects before implementation and pass after; existing quota tests still pass; `go build ./internal/quota/...` passes from `backend`; no non-quota code files change. |
| Time estimate | 60-90 minutes wall clock, dominated by red/green PG tests and concurrency tuning. |
| Blast radius | Medium inside quota only. Wrong logic can over-allow or fail-close quota reservations, but no schema, billing ledger, auth, gateway, or deployment surface is touched. |
| Failure modes | PG serialization/unique conflict is not consistently reproducible: use a discriminating concurrent test with same claim and DB row assertions. Request metric may still rely on `request_count`: assert seeded `reserved_value + settled_value = limit` with low `request_count`. Concurrency audit may retain misleading zero values: assert payload omits those fields and marks saturation. |
| Decision points | Stop before changing migrations, sqlc queries, Store interface, billing, gateway wiring, frozen packages, runtime dependencies, or destructive DB operations. |
| Pre-execution checklist | 1. Read current quota service/store/test code. 2. Add failing PG tests for S1-1 and S1-2 first. 3. Implement the smallest service/store fixes. 4. Add or adjust a narrow assertion for S2-1. 5. Gofmt touched quota files. 6. Run build and quota tests, including real PG when `HUAKAI_DATABASE_URL` is available. |

## Concrete Execution Order

1. Add request-metric reserved+settled deny test to `backend/internal/quota/service_integration_test.go`.
2. Add concurrent same-claim Reserve test to `backend/internal/quota/service_integration_test.go`.
3. Run targeted PG tests to confirm they fail for the intended reasons.
4. Change `backend/internal/quota/service.go` so request usage uses `reserved_value + settled_value`, request reserve delta is always the per-request unit, idempotent insert conflicts re-read the committed reservation, and concurrency saturated payload avoids fake zero counters.
5. Change `backend/internal/quota/pg_store.go` contract text/path so `NewPostgresStore` only advertises DBTX values that can begin their own transaction.
6. Run `gofmt` and verification commands from `backend`.
