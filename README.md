# HUAKAI (华凯)

> MIT clean-room AI Gateway + Account Hub + Admin Ops Platform.

**Status:** Phase 0.5 — governance baseline established, no business code yet.

This repository currently contains only the project's **operating charter, agent rules, clean-room policy, parity controls, and phased delivery plan**. Implementation begins after the Owner explicitly starts a phase (see [Owner Start Gate](docs/00_PM_OPERATING_SYSTEM.md)).

## Mission

Reach **full feature parity or better** with high-signal maintained AI gateway / account hub projects, using a clean-room reimplementation that stays MIT-compatible. No reference feature may be silently dropped; risk changes implementation method, not scope.

## Repository Layout

| Path | Purpose |
| --- | --- |
| [CLAUDE.md](CLAUDE.md) / [GEMINI.md](GEMINI.md) / [AGENTS.md](AGENTS.md) | Per-agent operating charters (authoritative). |
| [docs/](docs/) | English authoritative governance, contracts, parity matrix, risk register, release gates. |
| [docs_zh/](docs_zh/) | Owner-facing Chinese summaries (informational; English is canonical). |
| [.agents/skills/](.agents/skills/) | Tool-agnostic skill definitions (canonical). |
| [.claude/skills/](.claude/skills/) | Mirror of `.agents/skills/` for Claude Code discovery (do not edit; see [.claude/skills/CANONICAL.md](.claude/skills/CANONICAL.md)). |
| [.claude/agents/](.claude/agents/) | Claude sub-agent role definitions. |
| [.gemini/hooks/](.gemini/hooks/) | Gemini guardrail shell hooks. |

## Where to Start

1. Read [docs/01_PROJECT_BRIEF.md](docs/01_PROJECT_BRIEF.md) for product scope.
2. Read [docs/00_PM_OPERATING_SYSTEM.md](docs/00_PM_OPERATING_SYSTEM.md) for the operating loop.
3. Read [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) before touching anything driven by external references.
4. Read [docs/16_PHASED_DELIVERY_PLAN.md](docs/16_PHASED_DELIVERY_PLAN.md) to understand phasing.
5. For UI work, read [docs/14_UI_CONTRACTS.md](docs/14_UI_CONTRACTS.md) and [docs/08_REAL_WORLD_SCENARIOS.md](docs/08_REAL_WORLD_SCENARIOS.md).

## Reference Projects

Reference projects are evidence sources only, never source-code providers. Their license types determine clean-room handling. Verified license status is tracked in [docs/06_REFERENCE_PROJECTS.md](docs/06_REFERENCE_PROJECTS.md).

## How Agents Discuss Decisions

Routine work follows the **Standard Flow** (sequential pipeline) defined in [docs/12_AGENT_WORKFLOW.md](docs/12_AGENT_WORKFLOW.md). When a decision needs three voices in parallel before the Owner picks — language choice, multi-tenancy model, clean-room methodology, phase exit — agents use the **Round-Table** mode defined in [docs/21_DECISION_PROCESS.md](docs/21_DECISION_PROCESS.md).

Round-Table decisions live as files under [docs/decisions/](docs/decisions/), one file per decision, instantiated from [docs/decisions/_TEMPLATE.md](docs/decisions/_TEMPLATE.md). Each agent writes only its own section; the Owner writes the final decision in the same file. Examples:

- [DR-000-clean-room-methodology.md](docs/decisions/DR-000-clean-room-methodology.md) — currently in Discussion, awaiting Codex / Gemini views and Owner pick.

## License

[MIT](LICENSE). Contributions to this repository must remain MIT-compatible. See [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) for what is allowed and forbidden when learning from external projects.

## Contributing

Phase 0.5 is documentation only. After the Owner starts Phase 1, contribution rules will be added to `CONTRIBUTING.md`. Until then, all changes are owner-directed.
