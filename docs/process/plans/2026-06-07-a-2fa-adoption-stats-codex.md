# 2026-06-07 A 2FA Adoption Stats Codex Plan

| Owner directive | "模块A闭环 — 管理端只读 2FA 采纳统计(AUTH-108 的只读半;force-disable 半 park 待 Owner)" |
| Scope | In: admin-user read-only tenant-scoped 2FA adoption stats query, handler, route, focused tests. Out: force-disable, schema migration, secrets, auth/billing/quota core, reference-source reading, git commit. |
| Success criteria | `GET /2fa-adoption-stats` is admin-authenticated, tenant-operator scoped, read-only, returns `enabled_users`, `total_users`, and `enabled_rate`; test proves tenant B enabled settings do not leak into tenant A stats. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass. |
| Blast radius | Low-to-medium: one admin read-only route, one sqlc query, generated admin db code, and adminuserhttp tests. |
| Failure modes | Missing tenant predicate leaks cross-tenant stats; route ordering could collide with `/{id}`; generated sqlc code could mismatch expected integer types; local DB-dependent integration tests may be unavailable. Mitigation: put literal route before `/{id}`, use `resolveTenant`, add tenant-leak test, run build/vet/unit tests. |
| Decision points | No Owner sign-off needed unless implementation requires schema migration, force-disable mutation, new runtime dependency, touching high-risk files, or reading reference source. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and Owner Start Gate. 2. Read `internal/adminuserhttp/routes.go`. 3. Read `sql/queries/admin_users.sql` and generated `internal/db/admin`. 4. Locate `users` and `two_factor_settings` schema. 5. Inspect existing adminuserhttp test patterns. 6. Write failing test before production code. 7. Regenerate sqlc. 8. Run requested checks. |

Execution order:
1. Add a focused adminuserhttp test for tenant-scoped 2FA stats.
2. Run the focused test and confirm it fails because the route/store method is missing.
3. Add `AdminGetTwoFAAdoptionStatsForTenant` to `sql/queries/admin_users.sql`.
4. Regenerate sqlc output under `internal/db/admin`.
5. Extend `userReadStore`, add response body and handler, and mount `GET /2fa-adoption-stats` before `/{id}`.
6. Run focused tests and requested build/vet/test commands.
