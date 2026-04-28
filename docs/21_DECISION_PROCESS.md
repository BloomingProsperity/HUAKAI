This file is agent-facing and authoritative.

# Decision Process (Round-Table Mode)

## Why This Exists

[12_AGENT_WORKFLOW.md](12_AGENT_WORKFLOW.md) defines a **sequential pipeline** (Claude → Gemini → Codex → Claude). This document defines the **round-table** mode used when a decision needs **three voices in parallel** before the Owner picks.

Use round-table when:

- A choice affects multiple capability groups or multiple authoritative docs.
- The agents are likely to disagree (license vs UX, security vs feature, billing vs operability).
- The decision must be defensible later (you need a written trail of who said what and why).

Do **not** use round-table for routine work where one agent is the obvious owner. Keep that on the Standard Flow.

## Artifact

A round-table decision is captured in a single file: `docs/decisions/DR-NNN-<slug>.md`, instantiated from [`docs/decisions/_TEMPLATE.md`](decisions/_TEMPLATE.md).

`NNN` is a zero-padded sequence (`000`, `001`, …). IDs never reuse, even if a DR is superseded.

## Lifecycle

```
Open → Discussion → Decided → Implemented → (Superseded by DR-MMM)
```

| State | Meaning | Who advances |
| --- | --- | --- |
| Open | DR file created with Question + Context. Agent sections empty. | Claude PM (usually) |
| Discussion | At least one agent has filled its section. Other sections may still be empty. | Any agent that is asked |
| Decided | Owner has written the decision. | Owner only |
| Implemented | Propagation checklist is fully checked. Affected docs have been updated. | Claude PM |
| Superseded | A later DR replaces this one. The original stays in the file for history. | Claude PM, citing the new DR |

## Section Ownership

Each agent edits **only its own section**. This is the concurrency rule that lets the file grow without conflicts.

| Section | Edited by |
| --- | --- |
| Header (Status, Date, Affected docs) | Claude PM |
| Question | Owner or Claude PM |
| Context | Claude PM |
| Claude (PM/Architect) view | Claude only |
| Codex (Reviewer) view | Codex only |
| Gemini (UI/Ops) view | Gemini only |
| Conflicts | Claude PM (synthesizes) |
| Owner Decision | Owner only |
| Propagation | Claude PM |

If an agent has nothing to add (e.g. the decision has no UI impact and Gemini has no view), it must still write `No material input — <one-line reason>` in its section. Empty sections are not allowed once the DR moves to Decided.

## Owner Workflow

1. Owner identifies a round-table-grade decision.
2. Owner asks Claude to open a DR (Claude creates the file from template, fills Question + Context, sets Status to `Open`, fills its own view, sets Status to `Discussion`).
3. Owner runs Codex (manually or via `/ask codex` / `/ccg`) and points it at the DR file. Codex reads, fills its section, commits.
4. Owner runs Gemini similarly.
5. Owner reads all three sections and the Conflicts section (Claude PM may pre-fill conflicts after step 3 + 4).
6. Owner writes the decision in the Owner Decision section, sets Status to `Decided`.
7. Owner asks Claude to propagate (update affected docs, tick the propagation checklist, set Status to `Implemented`).

Steps 3 + 4 can happen in either order or in parallel. There is no required sequence between Codex and Gemini once Claude's view is in.

## Staleness Protocol

A DR's `Context` section can go stale while it sits in `Discussion`. If new evidence, new decisions, or new mining batches change the basis on which agents and the Owner formed their views, the DR's recommendation may no longer match reality.

**Rule:** when a DR has been in `Discussion` state for **more than 7 calendar days**, the Claude PM must:

1. Re-read the DR file.
2. Re-read all docs referenced in the DR's `Affected docs` field.
3. Refresh any concrete numbers in the Context section (feature counts, evidence counts, reference counts) to current values.
4. Append a `## Staleness Refresh` log entry recording the refresh date and what changed (or "no material change").
5. Notify the Owner that the DR is ready for decision (or that a Conflicts re-synthesis is needed).

A DR may not move to `Decided` if its Context has not been refreshed within the last 7 days.

This rule was introduced 2026-04-28 after the Codex Phase 1 audit found that DR-003 was carrying stale numbers (35 features cited; actual 56) at the moment Owner approved it.

## Conflict Resolution Rules

When agents disagree, [docs/12 Escalation](12_AGENT_WORKFLOW.md) and the project preservation rules apply, in priority order:

1. **Feature parity preservation** wins over implementation convenience.
2. **Clean-room safety** wins over feature ergonomics.
3. **Implementation method** is the variable that absorbs all other tradeoffs.
4. **Owner decision** overrides all of the above.

Claude PM must surface the conflict explicitly in the Conflicts section using the format:

```
- [Topic]: <Claude says X> vs <Codex says Y> — material reason: <one line>
```

Cosmetic disagreements (wording, ordering) are not conflicts. Only material disagreements that would change the outcome belong here.

## Propagation Checklist

Every Decided DR must be propagated. Claude PM is responsible for:

- Updating each affected doc to reference the DR (e.g. `Decided in [DR-001](decisions/DR-001-...md)`).
- Updating [10_RISK_REGISTER.md](10_RISK_REGISTER.md) if the decision changes a risk's mitigation.
- Updating [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) if the decision changes a disposition.
- Updating skills under [.agents/skills/](../.agents/skills/) if the decision changes a workflow.
- Marking the DR's checklist items as ✅ in the file.

## DR vs Standard Flow

| Decision type | Use which |
| --- | --- |
| Cross-cutting architecture choice (language, multi-tenancy, clean-room methodology) | Round-Table (DR) |
| Adding a new capability row to parity matrix | Standard Flow |
| Audit finding from Codex about a single feature | Standard Flow |
| UI redesign of one resource page | Standard Flow |
| Decision that overrides a previous DR | Round-Table (new DR with `Supersedes` field) |
| Phase exit decision | Round-Table |

When in doubt, prefer Round-Table — the cost of an extra DR file is small; the cost of an undocumented decision is large.

## Tooling Hint (Optional)

If the OMC plugin is available, `/ccg` (Claude-Codex-Gemini tri-model orchestration) can drive a DR by routing the same prompt to all three agents and synthesizing results. The agents must still write their sections themselves; `/ccg` handles dispatch and aggregation, not authoring.
