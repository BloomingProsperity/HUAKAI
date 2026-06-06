# 2026-06-06 channel-health fleet summary endpoint - Codex plan

| Owner directive | "TASK: Add channel-health fleet SUMMARY endpoint (branch fix/cred-health-summary). Verified real_missing: per-channel list/detail exist, no fleet rollup. Reach CLOSURE. Read-only, admin-gated, tenant-scoped. No shortcuts." |
| Scope | Add `GET /v1/admin/channel-health/summary` as a read-only platform-admin endpoint. In scope: existing frozen handler file edit, channelhealth service/store aggregate, memory parity, focused unit/handler tests, OpenAPI contract. Out of scope: migrations, commits, reference-source reads, billing/quota/auth core changes, integration_pg execution. |
| Success criteria | Summary returns `{by_state, total, oldest_cooldown_at?}` for the requested tenant only; non-admin receives 403; OpenAPI includes the public route and schema; focused tests fail before implementation and pass after implementation; requested build/vet/unit commands are run and reported. |
| Time estimate | 60-90 minutes wall clock in this Codex session. |
| Blast radius | Low-to-medium: read-only admin API plus channelhealth store interface change. Main risks are route shadowing by `/{channel_id}`, weak tenant scoping, and breaking existing channelhealth service/store implementations. |
| Failure modes | `/summary` captured as detail route: register summary before `/{channel_id}`. Dropped tenant filter: add discriminating tenant A/B tests at service/handler level and Postgres aggregate SQL with `WHERE tenant_id = $1`. Weak state count tests: use multiple states with nonuniform counts and exact expected map. Interface breakage: update both PostgresStore and MemoryStore plus test stubs. OpenAPI drift: add path and schemas next to existing channel-health contract. |
| Decision points | None expected. High-risk files are not touched. PM will run `integration_pg`; no migration will be created. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and existing channelhealth handler/store/service/tests. 2. Confirm frozen packages get no new files. 3. Confirm no reference-source reads. 4. Write failing tests first. 5. Implement minimal read-only aggregate. 6. Update OpenAPI. 7. Run build/vet/focused unit commands. 8. Stage `backend/` and `docs/` only, without committing. |

## Concrete execution order

1. Add tests in existing `backend/internal/gatewayhttp/channel_health_admin_handler_test.go` for summary counts, tenant scoping, and admin role rejection with explicit mutation comments.
2. Add service/memory parity coverage in existing channelhealth tests if handler coverage does not exercise the service/store aggregate directly enough.
3. Run the focused tests and confirm RED because `SummarizeChannelHealth` and `/summary` do not exist yet.
4. Add `ChannelHealthSummary` type and `SummarizeChannelHealth` to `backend/internal/channelhealth/types.go`, `service.go`, `store_memory.go`, and `store_postgres.go`.
5. Edit existing `backend/internal/gatewayhttp/channel_health_admin_handler.go` to add `SummarizeChannelHealth` to the controller interface, register `r.Get("/summary", ...)` before `/{channel_id}`, and serialize the summary.
6. Update `docs/openapi/openapi.yaml` with `/v1/admin/channel-health/summary` and `ChannelHealthSummaryResponse`.
7. Run focused tests, then `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./... && go vet ./...`, then unit tests for `internal/channelhealth`, `internal/gatewayhttp`, and `cmd/gateway`.
8. Run `git add backend/ docs/`, inspect status, and report the result without committing.

## Clean-room and package-structure notes

- This is HUAKAI-native implementation from the Owner-provided behavior sketch and existing local patterns.
- No reference source paths are read.
- `backend/internal/gatewayhttp` is frozen: only the existing `channel_health_admin_handler.go` and existing test file are edited; no new file is added there.
- `backend/internal/channelhealth` is non-frozen and receives aggregate support in existing files only.
