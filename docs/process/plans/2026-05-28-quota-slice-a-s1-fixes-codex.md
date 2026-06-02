# 2026-05-28 quota slice A S1 fixes

| Owner directive | "修复 HUAKAI 配额子系统切片 A 的 3 个 S1 缺陷(per-commit review Round 1 发现)。全部在 backend/sql/queries/quota.sql(必要时配套调 0070 migration / store.go 签名)。" |
| Scope | In: `backend/sql/queries/quota.sql`, necessary 0070 migration adjustments, only if needed `backend/internal/quota/store.go` signature alignment. Out: frozen packages, generated sqlc package, unrelated quota implementation, commits/staging/push. |
| Success criteria | Scope policy lookup pairs `scope_kinds[i]` with `scope_ids[i]`; local quota slot acquisition is serialized under READ COMMITTED; due reconciliation selection can reclaim stale `running` jobs and mark them running again without inserting duplicate active jobs. |
| Time estimate | 30-60 minutes wall clock; single Codex work unit. |
| Blast radius | Medium: quota enforcement SQL and new quota schema migration only. Incorrect SQL could allow quota overspend, false deny, or stuck reconciliation. |
| Failure modes | Tuple matching could break sqlc typing; mitigate with explicit casts and static/sqlc checks. Slot serialization could deadlock or fail first insert; mitigate with one per-scope lock row acquired inside the DB function before expiry cleanup and count. Stale job recovery could race with active workers; mitigate with stale lease predicate plus `FOR UPDATE SKIP LOCKED`. |
| Decision points | Owner already approved small schema adjustment if needed. Stop before auth, billing ledger, quota enforcement outside this slice, frozen package edits, destructive commands, or dependency changes. |
| Pre-execution checklist | 1. Confirm worktree branch and dirty files. 2. Read affected SQL and migration. 3. Run RED static checks that fail on the three reviewed defects. 4. Patch only approved files. 5. Run formatting/static checks and any available narrow Go checks. |

Concrete execution order:

1. Replace independent scope array predicates with tuple-paired `unnest(scope_kinds, scope_ids)`.
2. Add a tenant/scope quota concurrency lock table plus a local acquire function in 0070; call it from `AcquireQuotaConcurrencySlot` so the lock, expiry cleanup, count, and upsert run with fresh READ COMMITTED snapshots inside one DB routine.
3. Extend due-job selection and mark-running update to recover stale `running` jobs using a fixed lease timeout.
4. Verify no forbidden frozen package edits, no git staging, and no unrelated churn.
