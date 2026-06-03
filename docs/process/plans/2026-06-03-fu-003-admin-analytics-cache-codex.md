# 2026-06-03 FU-003 admin analytics snapshot cache (Codex)

| Owner directive | "Task① 回扫升级 FU-003:给 admin leaderboard + performance 两个 handler 加抗击穿快照缓存 + 缓存命中头,和已 land 的 overview 一致" |
| Scope | In: `backend/internal/usageanalyticshttp/{leaderboard,performance}_handler.go` and existing tests in the same package. Out: SQL, migrations, auth, billing ledger, quota, frozen packages, runtime dependencies, commits. |
| Success criteria | Leaderboard and performance wrap their fetch+response assembly in `snapshotcache.GetOrLoad`; set `X-Snapshot-Cache: hit|miss`; use stable parsed keys; errors are not cached; tests cover hit, TTL expiry, concurrent same-key coalescing, and independent keys. Required backend build/vet/test gate runs. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Admin analytics read handlers only. A bad cache key could serve wrong leaderboard/performance slices; a missing header could break ops observability; weak tests could miss stampede regression. |
| Failure modes | Unstable key including `settledSince` prevents hits; type assertion failure path differs from overview; global cache state leaks between tests; concurrent test races on unsynchronized stub counters; test fixtures pass even if cache wrapper is removed. Mitigation: mirror overview type/header handling, isolate unique query keys per test, use mutex/atomic counters, and assert backend call counts. |
| Decision points | No Owner sign-off expected unless implementation requires SQL/schema/auth/billing/quota changes, new dependencies, or touching frozen packages. |
| Reference projects in scope | None for source reading. Owner explicitly set CLEAN-ROOM: "严禁读 /home/ubuntu/refs 等任何外部源码"; this plan makes no external reference-project behavior claims. CLIProxyAPI/sub2api/new-api are deliberately not read for this HUAKAI-internal parity-with-overview follow-up. |
| Clean-room guard | Read only HUAKAI-owned files under this worktree: `CLAUDE.md`, `AGENTS.md`, `.coordination/README.md`, `backend/internal/usageanalyticshttp/*`, and `backend/internal/snapshotcache/*`. Do not access `/home/ubuntu/refs`. |
| Package structure | No files in frozen `backend/internal/{gatewayhttp,gateway,proto}`. Existing non-frozen package `backend/internal/usageanalyticshttp` remains under budget; no new implementation package needed. |
| Pre-execution checklist | 1. Read project rules and coordination protocol. 2. Claim edit lock. 3. Read overview cache pattern and target handlers/tests. 4. Write failing tests first. 5. Run targeted tests to verify RED. 6. Implement minimal cache wrapper. 7. Run targeted GREEN tests. 8. Run required gate. |

## Concrete Execution Order

1. Add leaderboard snapshot-cache tests to the existing test file:
   - same parsed `by/window/limit` twice: first miss, second hit, one backend query;
   - TTL expiry causes a second backend query and miss;
   - concurrent same-key requests coalesce to one backend query;
   - different stable keys stay independent.
2. Add equivalent performance tests.
3. Run targeted package tests and confirm the new cache tests fail before production code changes.
4. Add `snapshotcache` imports, TTL vars, response loaders, and stable key helpers to leaderboard and performance handlers.
5. Run targeted package tests again and then the required backend build/vet/test gate.
