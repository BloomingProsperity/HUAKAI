# HUAKAI (华凯)

> MIT clean-room AI Gateway + Account Hub + Admin Ops Platform.

**Status:** Phase C / N+5b in progress. The backend now has a working clean-room gateway core slice, not just governance documents.

The current implemented request path is:

```text
Inbound Auth -> Model Registry -> Router Plan -> ClaimGate Reserve
-> Resource Pool Select -> Stream Forwarder -> Billing/Observability Settler
```

The project is still early. Multi-attempt fallback routing, first-class `attempt_id` / `lease_id`, real provider adapters, production pricing, admin APIs, and the frontend console are still active roadmap work.

## Mission

Reach full feature parity or better with high-signal maintained AI gateway and account hub projects, using a clean-room reimplementation that stays MIT-compatible. Reference projects are evidence sources only; no reference feature may be silently dropped, and risk changes implementation method rather than scope.

## Repository Layout

| Path | Purpose |
| --- | --- |
| [backend/](backend/) | Go backend core: gateway HTTP entrypoint, inbound auth, model registry, router engine, resource pool, protocol translation, streaming forwarder, billing/observability ledger, SQL migrations, and tests. |
| [frontend/](frontend/) | Frontend workspace placeholder. The operations console is not implemented yet. |
| [CLAUDE.md](CLAUDE.md) / [GEMINI.md](GEMINI.md) / [AGENTS.md](AGENTS.md) | Per-agent operating charters. |
| [docs/](docs/) | Authoritative governance, contracts, parity matrix, risk register, release gates, specs, and plans. |
| [docs_zh/](docs_zh/) | Owner-facing Chinese summaries. English docs remain canonical unless a decision says otherwise. |
| [docs/plans/](docs/plans/) | Execution plans and Claude/Codex cross-discussion records for implementation slices. |
| [backend/sql/migrations/](backend/sql/migrations/) | PostgreSQL migrations for pool routing, billing/observability, inbound auth, model registry, and related core tables. |
| [.agents/skills/](.agents/skills/) | Tool-agnostic skill definitions. |
| [.claude/skills/](.claude/skills/) | Mirror of `.agents/skills/` for Claude Code discovery. |
| [.claude/agents/](.claude/agents/) | Claude sub-agent role definitions. |
| [.gemini/hooks/](.gemini/hooks/) | Gemini guardrail shell hooks. |

## Current Backend Slice

The current live path is `POST /v1/chat/completions`.

Implemented:

- Table-backed inbound API key auth in `backend/internal/auth`.
- PostgreSQL-backed model registry in `backend/internal/registry`.
- L0 router engine in `backend/internal/router`.
- Resource pool selection and claim writeback in `backend/internal/pool`.
- Streaming forwarder and usage draft extraction in `backend/internal/gateway`.
- Tx1/Tx2 billing and observability settlement in `backend/internal/billing`.
- PostgreSQL migrations through `0008_model_registry`.

Known limitations:

- Router is still L0: one primary attempt from `PoolCandidates[0]`.
- Gateway executor logic is still embedded in the chat handler.
- `attempt_id` and `lease_id` are documented but not yet first-class schema fields.
- Provider adapters are not production-complete; the current happy path uses mock upstream bytes and an Anthropic SSE parser.
- Successful requests still settle with a fixed placeholder cost.
- Admin APIs and the frontend operations console are not implemented yet.

## Where to Start

1. Read [docs/01_PROJECT_BRIEF.md](docs/01_PROJECT_BRIEF.md) for product scope.
2. Read [docs/00_PM_OPERATING_SYSTEM.md](docs/00_PM_OPERATING_SYSTEM.md) for the operating loop.
3. Read [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) before touching anything driven by external references.
4. Read [docs/16_PHASED_DELIVERY_PLAN.md](docs/16_PHASED_DELIVERY_PLAN.md) to understand phasing.
5. For backend core work, read [docs/specs/_invariants/cross-module-boundaries.md](docs/specs/_invariants/cross-module-boundaries.md) before editing `backend/internal/{auth,registry,router,pool,gateway,gatewayhttp,billing,obs,proto}`.
6. For the current request path, start with [backend/cmd/gateway/main.go](backend/cmd/gateway/main.go) and [backend/internal/gatewayhttp/chat_completions_handler.go](backend/internal/gatewayhttp/chat_completions_handler.go).
7. For UI work, read [docs/14_UI_CONTRACTS.md](docs/14_UI_CONTRACTS.md) and [docs/08_REAL_WORLD_SCENARIOS.md](docs/08_REAL_WORLD_SCENARIOS.md).

## Verification

From `backend/`:

```bash
go test ./...
go test -tags integration_pg ./...
go test -tags smoke ./cmd/gateway
```

`integration_pg` and `smoke` require `HUAKAI_DATABASE_URL` to point at a migrated PostgreSQL database.

## Reference Projects

Reference projects are evidence sources only, never source-code providers. Their license types determine clean-room handling. Verified license status is tracked in [docs/06_REFERENCE_PROJECTS.md](docs/06_REFERENCE_PROJECTS.md).

## How Agents Discuss Decisions

Routine work follows the Standard Flow defined in [docs/12_AGENT_WORKFLOW.md](docs/12_AGENT_WORKFLOW.md). When a decision needs multiple independent views before the Owner picks, agents use the Round-Table mode defined in [docs/21_DECISION_PROCESS.md](docs/21_DECISION_PROCESS.md).

Round-Table decisions live under [docs/decisions/](docs/decisions/). Each agent writes only its own section; the Owner writes the final decision in the same file.

## License

[MIT](LICENSE). Contributions to this repository must remain MIT-compatible. See [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) for what is allowed and forbidden when learning from external projects.

## Contributing

Implementation is active. All changes remain owner-directed and must follow the clean-room policy, plan-before-execute discipline, cross-review protocol, and cross-module boundary invariants.
