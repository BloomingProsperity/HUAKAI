# 2026-06-06 alert rules engine

| Owner directive | "TASK: Add ALERT RULES engine (rules + events + silences) to HUAKAI (branch fix/alert-rules). Verified real_missing... Reach CLOSURE: CRUD + eval + silence + migration + tests. No shortcuts." |
| Scope | In: HUAKAI-native alert rules, alert events, silences, migration 0103, admin HTTP CRUD/list routes, OpenAPI entries, unit tests, integration_pg migration/store tests. Out: reference source reads, scheduler goroutine, commits, PM-only integration socket runs. |
| Success criteria | Admin CRUD exists for rules and silences; event list supports rule/state filters and pagination; `EvaluateRules` fires, resolves, suppresses under active silence, and is idempotent; tenant filters are enforced; migration up/down creates and drops three tenant-scoped tables and indexes; OpenAPI consistency can see all new public routes. |
| Time estimate | 2-3 hours wall clock in one Codex session; tests/build/vet may extend depending on existing suite state. |
| Blast radius | New packages `backend/internal/alerting` and `backend/internal/alertinghttp`, new gateway route mount, migration files, OpenAPI docs, and focused tests. Frozen packages `gatewayhttp`, `gateway`, and `proto` receive no new files. |
| Failure modes | Duplicate firing events if idempotency is missed; resolved events not updated; silence matching not tenant/rule scoped; HTTP routes mounted without OpenAPI entries; migration down leaves objects behind; typed-nil dependencies panic route registration. Mitigation: discriminating tests, temp-schema migration probe, nil-safe mount helper, and final build/vet/test pass. |
| Decision points | No high-risk Owner confirmation is needed for the planned changes. No database schema beyond the explicitly requested 0103 migration will be changed. No runtime dependency will be added. |
| Pre-execution checklist | Read moderation/announcement admin patterns; read observability metric snapshot context; read migration style 0101/0102; confirm latest migration is 0102; confirm no `/home/ubuntu/refs` reads; write tests before implementation; update OpenAPI before final consistency test; run requested verification commands. |

## Execution order

1. Add `internal/alerting` unit tests for rule CRUD validation, eval fire/resolve/silence/idempotency, and tenant scope.
2. Implement `internal/alerting` types, service, memory store, and pgx store with tenant-scoped SQL.
3. Add migration `0103_alerting.up.sql` and `0103_alerting.down.sql`; add integration_pg migration/store probes.
4. Add `internal/alertinghttp` handler tests, then implement admin CRUD/list handlers and helpers.
5. Add `cmd/gateway/routes_alerting.go` and call it from `mountAdminRoutes`.
6. Add `/v1/admin/alert-rules`, `/v1/admin/alert-events`, and `/v1/admin/alert-silences` paths plus schemas to `docs/openapi/openapi.yaml`.
7. Run focused tests, OpenAPI consistency tests, `go build ./...`, and `go vet ./...`.

## Assumptions

- The direct Owner task is the approval to proceed after this plan artifact; this Codex-only session cannot independently produce or reconcile a Claude parallel plan.
- Alert evaluation consumes a caller-provided `map[string]float64` snapshot. Existing `internal/observability` only aggregates request-completion counters in memory and does not expose a stable alert snapshot API in this slice.
- Deleting a rule is hard delete only when no events reference it; HTTP exposes the store error if FK history prevents deletion. Disabling is the preservation path for rules with history.
