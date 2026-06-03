# 2026-06-03 Content Moderation Slice 1 Codex Plan

| Owner directive | "Implement ONLY its first slice ... build the moderation Screener + stores as a SELF-CONTAINED package internal/moderation ... WITHOUT wiring it into the gatewayhttp dispatch hot path yet" |
| Scope | In: additive migration `0082`, sqlc moderation package, `internal/moderation`, `internal/moderationhttp`, and an unwired `cmd/gateway/routes_moderation.go` integration helper. Out: gatewayhttp hot-path dispatch wiring, platform-policy fee charging, auth core changes, billing ledger changes, real credentials, commit/stage. |
| Success criteria | `cd backend && sqlc generate`; `go build ./...`; `go vet ./internal/moderation/... ./internal/moderationhttp/...`; `go test ./internal/moderation/... ./internal/moderationhttp/...`; discriminating tests have red evidence and mutation evidence. |
| Time estimate | 2-3 hours wall clock in one Codex session. |
| Blast radius | New migration and new packages can affect sqlc generation and full backend build. No request dispatch path is modified in this slice. |
| Failure modes | sqlc parse/type mismatch; migration constraint incompatible with existing schema; tests with non-discriminating fixtures; accidental raw body logging; route helper accidentally wired into `routes.go`; files exceeding package/file budgets. Mitigation: verify actual schema first, use interfaces/fakes for tests, store only hashes/metadata, inspect diff before final. |
| Decision points | Owner/PM must approve before landing, and PM must separately wire `cmd/gateway/routes_moderation.go` plus later gatewayhttp hot-path screener call. Any need to alter auth, billing, quota, or dispatch stops this slice. |
| Pre-execution checklist | Read `docs/process/gap-specs/content-moderation.md`; verify `ErrorClassPlatformPolicy`; verify `api_keys.status` values; verify migration max/current filenames; verify sqlc package layout; confirm frozen packages receive no new files; write tests before implementation. |

## Concrete Execution Order

1. Add `0082_content_moderation.up.sql` and `.down.sql` with only new moderation tables and indexes.
2. Add `sql/queries/moderation.sql` and a new `sqlc.yaml` block generating `internal/db/moderation`.
3. Write failing tests for `internal/moderation` screener decisions, fail-open/fail-closed behavior, sampler/audit metadata, store cache behavior, and ban counter disable logic through fakes.
4. Run package tests and capture expected red output before production code exists.
5. Implement `internal/moderation` interfaces, screener, bounded TTL LRU cache, DB-backed keyword/hash/config/audit/ban helpers, and no-op/in-memory test seams.
6. Write failing tests for `internal/moderationhttp` keyword/config handlers using fake query/auth dependencies.
7. Implement `internal/moderationhttp` handlers and mount function.
8. Add `cmd/gateway/routes_moderation.go` with an unwired helper documenting the PM integration point; do not edit `routes.go` or `gatewayhttp`.
9. Run `sqlc generate`, format, build, vet, targeted tests, and mutation checks.
10. Inspect `git status`/`git diff --stat` to confirm no hot-path dispatch file changed and no frozen package received a new file.

## Premise Corrections Recorded Before Execution

- The gap spec says migration `0077`; the Owner prompt reserves `0082`, so this slice uses `0082`.
- The gap spec says edit `routes.go`; the Owner prompt says `routes.go` is over-limit and PM wires later, so this slice adds only `cmd/gateway/routes_moderation.go`.
- The gap spec includes admin route wiring and later fee paths; this slice leaves both gateway dispatch and billing/fee behavior untouched.
- Existing `api_keys.status` has no `banned`; ban counter support must set `disabled`.
