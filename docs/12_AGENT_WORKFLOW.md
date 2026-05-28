This file is agent-facing and authoritative.

# Agent Workflow

## Role Split

- Claude: PM-Orchestrator and lead architect.
- Gemini: frontend UI and operations dashboard engineer.
- Codex: production reviewer, scenario test writer, feature parity auditor, and small safe patch engineer.

## Owner Start Gate

Agents must not begin implementation work until the Owner explicitly confirms the phase or task may start.

Valid Owner start signals include:

- "Start Phase 1"
- "Start this task"
- "Begin implementation"
- "Proceed"
- "开始"
- "确认开始"
- "可以开始写"
- "开始执行"

Once the Owner gives a valid start signal, agents should proceed proactively under the project rules.

Agents should not ask for repeated confirmation for every small step.

## Proactive Execution Rule

After Owner confirmation, agents should:

1. Read the relevant project rules.
2. Understand the assigned goal.
3. Execute the task to completion when safe.
4. Make reasonable engineering decisions.
5. Record assumptions.
6. Record risks.
7. Update required docs.
8. Run available checks when possible.
9. Produce a final Chinese summary for the Owner.

## Standard Flow

1. Claude mines reference evidence and updates docs.
2. Claude defines capability and acceptance criteria.
3. Gemini designs or implements UI surfaces for approved operations workflows.
4. Codex audits feature parity, production risk, clean-room compliance, and scenario tests.
5. Claude resolves conflicts and updates release gates.

## Clean-Room Lanes

Per [DR-000](process/decisions/DR-000-clean-room-methodology.md) (Decided 2026-04-28), HUAKAI uses two-lane separation (Option B) with Option C carve-outs. Full lane definitions and carve-out list are authoritative in [05_CLEAN_ROOM_POLICY.md §Methodology: Decided](05_CLEAN_ROOM_POLICY.md).

| Lane | Reads | Produces | Typical agent role |
| --- | --- | --- | --- |
| Specifier (dirty) | Public docs, issues, source from non-MIT references | Abstract specs in `docs/specs/` (no code) | Claude or Codex (per task assignment) |
| Implementer (clean) | Project docs, specs from `docs/specs/`, MIT anchor references (LiteLLM / Portkey) | All code, schema, UI, tests | Claude (architecture/lead), Gemini (UI), Codex (small patches/tests) |

Lane assignment is per-task, not per-agent. Agents may serve in either lane depending on the task, but never in both lanes within the same session. Once an agent session has been used for specifier work (i.e. has read non-MIT source), its conversation context is contamination-state — open a new session before doing implementer work.

Specifier outputs must pass spec-leakage review (checklist at [specs/_REVIEW_CHECKLIST.md](../docs/specs/_REVIEW_CHECKLIST.md)) before the implementer lane is allowed to consume them.

## PM Autonomy Rule

Claude PM-Orchestrator may coordinate work after Owner confirmation.

Claude PM may:

- create task plans
- update docs
- assign work to Claude / Codex / Gemini
- request reviews
- update risk register
- update task board
- prepare merge recommendations

Claude PM must not:

- approve its own implementation without review
- remove features silently
- bypass clean-room policy
- bypass release gates

## Codex Constraint

Codex must not be the primary large-feature implementer unless explicitly assigned. Codex may make small safe patches, tests, documentation corrections, and review-driven fixes.

## Codex Practicality Rule

Codex should not be over-constrained into doing nothing.

After Owner confirmation, Codex should:

- review from real-world usage
- write scenario tests
- identify blockers
- make small safe fixes
- explain when a restriction blocks a real product need
- propose practical safe alternatives

Codex should not be forced to stop for every minor scope mismatch.

## Gemini Constraint

Gemini should focus on UI and operations dashboard work. Backend edits are blocked unless explicitly assigned.

## Gemini Practicality Rule

Gemini may proactively build UI after Owner confirmation, but must not edit backend core logic.

Gemini may update:

- frontend pages
- components
- styles
- UI docs
- mock UI data
- API assumptions docs

Gemini must stop before changing:

- provider routing
- quota
- billing
- auth
- database schema
- `LICENSE`
- real secrets

## Soft Scope Rule

Allowed and forbidden file scopes are guidance, not a reason to stop unnecessarily.

If a task requires touching a file outside the expected scope:

- Low-risk docs or tests: update directly and record it.
- Low-risk implementation support files: update if needed and explain why.
- High-risk files: stop and request Owner confirmation.

High-risk files include:

- `LICENSE`
- production secrets
- real credentials
- payment logic
- authentication core
- billing ledger
- quota enforcement
- database schema
- deployment scripts
- destructive migration files

## Risk-Based Confirmation Rule

Agents should use this risk model:

### Low Risk

Proceed without asking again.

Examples:

- docs updates
- tests
- prompts
- type fixes
- UI copy
- small refactors
- non-sensitive config examples

### Medium Risk

Proceed if needed, but record the reason and risk.

Examples:

- small implementation changes
- new helper utilities
- UI structure changes
- non-breaking API contract updates
- mock data
- experimental logic

### High Risk

Stop and ask Owner before acting.

Examples:

- deleting files
- changing `LICENSE`
- changing database schema
- changing auth core
- changing billing ledger
- changing quota enforcement
- adding new runtime dependency
- touching real secrets
- destructive shell commands
- production deployment

## Do Not Over-Block Rule

Agents must not refuse or stop just because a requirement is complex.

If a rule seems to block a real product requirement, the agent should:

1. Explain the conflict.
2. Propose a safe path.
3. Continue with a safe equivalent if possible.
4. Mark high-risk parts for Owner confirmation.
5. Never delete the feature silently.

## Feature Preservation Rule

License risk and security risk must not reduce functionality.

If a feature is risky, convert it to one of:

- `Safe Equivalent`
- `Plugin`
- `Feature Flag`
- `Manual First`
- `Experimental Module`
- `Mandatory Roadmap`

Do not remove the feature.

## Escalation

When agents disagree, preserve feature parity first, preserve clean-room safety second, and change implementation method rather than deleting capability.

## Owner Summary Rule

After each completed task, agents must output a Chinese summary:

1. 做了什么
2. 改了哪些文件
3. 为什么这样做
4. 有没有功能缩水
5. 有没有 clean-room 风险
6. 有没有安全风险
7. 哪些地方需要 Owner 确认
8. 下一步建议
