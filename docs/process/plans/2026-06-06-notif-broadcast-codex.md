# 2026-06-06 Admin Broadcast Notifications + User Inbox

| Owner directive | "TASK: Add admin BROADCAST notification + per-user inbox (branch fix/notif-broadcast). Verified real_missing: announcement board is pull-only (no per-user/read-state); notify pkg is threshold-alert only. Reach CLOSURE. No shortcuts." |
| Scope | In: HUAKAI-native migration 0104, new `internal/usernotice` store/service, new `internal/usernoticehttp` handlers, gateway route wiring, OpenAPI paths/schemas, discriminating unit and integration_pg tests. Out: `/home/ubuntu/refs`, commits, production deployment, billing/auth/quota core changes, integration_pg execution by Codex. |
| Success criteria | Admin broadcast creates one inbox row per active user in the admin tenant; user list/count/read routes are session scoped to own tenant and user; validation rejects empty title/body and unknown severity; OpenAPI has every new public route; requested build/vet/unit checks run; `backend/` and `docs/` are staged without commit. |
| Time estimate | 2-4 wall-clock hours in one Codex work unit; integration_pg mutation confirmation remains PM-run per directive. |
| Blast radius | New table, new HTTP routes, gateway wiring, and OpenAPI contract. Existing announcement, notify settings, auth, billing, quota, gatewayhttp, gateway, and proto packages should remain behaviorally unchanged. |
| Failure modes | Fan-out without tenant filter could notify other tenants; list/read without user filter could leak or mutate another user's inbox; unbounded fan-out could overload large tenants; OpenAPI drift could fail route consistency; migration could collide with latest version; package additions to frozen packages would violate structure rules. Mitigation: discriminating tests with mutation comments, batch insert from active users only, no new frozen package files, route consistency test, migration shape test. |
| Decision points | No additional Owner sign-off needed for 0104 because task explicitly authorizes it. Stop if implementation requires auth core, quota/billing ledger, real secrets, runtime dependency additions, or changing `LICENSE`. |
| Pre-execution checklist | 1. Confirm branch and dirty state. 2. Read `docs/RULES.md`, announcement, notification settings, session middleware, route wiring, recent migrations. 3. Write RED tests before production code. 4. Keep new packages outside frozen packages. 5. Update OpenAPI for all mounted public routes. 6. Run requested local checks. 7. Stage `backend/` and `docs/` only, no commit. |

## Execution Order

1. Add `internal/usernotice` tests for validation, fan-out tenant scoping, own-user list/unread/read behavior, and 0104 migration shape.
2. Add `internal/usernoticehttp` tests for admin auth, validation, session-scoped list/count/read, and own-only read behavior.
3. Implement `internal/usernotice` types, service, memory store, and PostgreSQL store.
4. Add `0104_user_notifications.up.sql` and `.down.sql`.
5. Implement `internal/usernoticehttp` handlers using existing admin and session patterns.
6. Wire service construction in `cmd/gateway/wiring.go` and mount routes from `cmd/gateway/routes.go` / `routes_notifications.go`.
7. Add OpenAPI paths/schemas and run route consistency.
8. Run targeted tests, build, vet, and requested package tests.
9. `git add backend/ docs/` and report status without committing.

## Assumptions

- Active recipients are rows in `users` with `tenant_id = $tenant` and `status = 'active'`.
- Broadcast payload currently targets all active users in the admin tenant; filtered audiences are preserved as a later extension point, not silently removed.
- `created_by_admin` records the admin token id when available.
- HTTP user inbox routes require session middleware and do not accept public `tenant_id` or `user_id` overrides.

## Clean-Room Note

This work is HUAKAI-native implementation from Owner-provided behavior requirements and local code patterns. Codex will not read `/home/ubuntu/refs` and will not copy reference source, distinctive structures, comments, tests, schemas, or identifiers.
