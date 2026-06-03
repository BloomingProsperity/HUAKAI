# 2026-06-03 admin overview snapshotcache codex

| Owner directive | "在已有 v1 /v1/admin/usage/overview 基础上,加融合强化层 —— 抗缓存击穿的 TTL 快照缓存 + 缓存命中头"; "严禁读任何外部参考源码(/home/ubuntu/refs 等一律不开)"; "不要 git commit" |
| Scope | In: add `backend/internal/snapshotcache` TTL snapshot cache, TDD coverage for hit/miss/expiry/singleflight/error retry, wrap `/v1/admin/usage/overview`, and add `X-Snapshot-Cache` response header tests. Out: external reference source reads, schema/auth/billing/quota changes, frozen packages, runtime dependency changes, commits. |
| Success criteria | Same-key overview requests within 30s reuse cached aggregate response and return `X-Snapshot-Cache: hit`; miss/expired/error paths return `miss`; concurrent same-key misses execute one loader; loader errors are not cached; requested build/vet/test gate either passes or blockers are recorded. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Medium-low: one new internal helper package and a read-only admin analytics handler. A cache bug could serve stale dashboard aggregates within the fixed 30s TTL; no sensitive plaintext is stored. |
| Failure modes | Missing inflight coalescing could stampede the database; missing expiry check could serve values forever; caching errors could mask database recovery; unstable cache key could miss every poll; global cache state could leak between tests. Mitigation: discriminating unit tests with loader call counts, a short package-level reset helper for tests, and exact query-derived key fields. |
| Decision points | None expected. High-risk files (`LICENSE`, secrets, schema migrations, auth core, billing ledger writes, quota enforcement, deployment scripts) are out of scope and require Owner confirmation. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Confirm coordination locks. 3. Read local overview handler/tests. 4. Write failing snapshotcache and overview cache tests first. 5. Implement minimal stdlib cache. 6. Run focused red/green tests and requested gate. |

## File Scope

- Create `backend/internal/snapshotcache/cache.go`: non-frozen package; stdlib mutex + map TTL cache + per-key inflight call coalescing.
- Create `backend/internal/snapshotcache/cache_test.go`: hit, expiry, singleflight, and error non-caching tests.
- Modify `backend/internal/usageanalyticshttp/overview_handler.go`: wrap existing totals+trend loader in `snapshotcache.GetOrLoad`, cache key from parsed query, 30s TTL, and `X-Snapshot-Cache` header.
- Modify `backend/internal/usageanalyticshttp/overview_handler_test.go`: verify same-window cache hit, TTL expiry behavior through package helper, and concurrent same-key miss coalescing at handler level.

## Clean-Room Boundary

This work uses only the Owner/PM-provided design and HUAKAI-local code. No `/home/ubuntu/refs` paths or external reference project source are in scope. The implementation is a fresh stdlib mutex/map design for a generic TTL cache and stores only read-only aggregate response objects.
