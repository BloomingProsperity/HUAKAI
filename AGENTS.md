This file is agent-facing and authoritative.

# Agent Operating Rules

This project builds an MIT clean-room AI Gateway + Account Hub + Admin Ops Platform with full feature parity or better against empirical reference projects.

## Non-Negotiables

- Reference projects are evidence, not source-code providers.
- Do not copy AGPL/GPL/LGPL source code, distinctive file structures, comments, schemas, UI source, or implementation details.
- License risk may change the implementation method, but must not remove a feature.
- Security risk may change rollout, gating, or defaults, but must not remove a feature.
- Every reference feature must be mapped to `Implemented`, `Implemented Better`, `Merged Equivalent`, `Safe Equivalent`, `Plugin`, `Feature Flag`, or `Mandatory Roadmap`.
- No feature may be silently dropped.
- Do not modify `LICENSE` without explicit owner instruction.

## Owner Start Gate

See [docs/RULES.md §2 Owner Start Gate](docs/RULES.md#2-owner-start-gate) for the canonical rule (S-001/S-002) and the full list of valid start signals. All agents follow that rule unchanged.

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

## Agent Roles

- Claude: PM-Orchestrator and lead architect.
- Gemini: frontend UI and operations dashboard engineer.
- Codex: production reviewer, scenario test writer, feature parity auditor, and small safe patch engineer.

Codex must not be the primary large-feature implementer unless explicitly assigned.

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

## Where To Work

- Use `docs/` for authoritative planning, contracts, parity, risk, and release gates.
- Use `.agents/skills/` for complex workflows.
- Use `.claude/agents/` for Claude sub-agent definitions.
- Use `.gemini/hooks/` for Gemini guardrails.
- Do not write business implementation in this initialization pass.

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

## Cross-Review Protocol (added 2026-04-29)

When dispatched as **reviewer-lane** (e.g. `codex exec --sandbox read-only` with `docs/templates/codex-reviewer.md` piped to stdin), you MUST follow these rules — they survive `AGENTS.md` truncation only if you read the full template, so always read the template before producing output.

1. **Read-only physical guarantee**: you cannot edit any file. Output is a written report.
2. **Quoted evidence required**: every finding cites `file:line` for both the spec and the test code. Findings without citations are invalid.
3. **Coverage matrix is the spine**: every AT-* ID in the spec must appear in your matrix as one of COVERED / COVERED-WEAK / SKIPPED (with validity check) / MISSING.
4. **Severity is binding**:
   - HIGH = blocks Released-spec status. Owner cannot ship unfixed.
   - MED = must fix before opening the next vertical slice.
   - LOW = backlog item.
5. **Smell library** — flag these even if tests pass:
   - assertions like `res.X != bad` but never `res.X == good`
   - tests that `t.Skip` when a field is zero — coverage hole disguised as defense
   - "100 goroutines" in comment but `N=12` in code
   - test fixtures where winner and loser share the distinguishing feature
   - stubs not mirroring production SQL `WHERE` clauses
   - gate chains all `AllowAllGate` in tests, hiding gate-failure paths
6. **Output ends with Chinese 1-paragraph summary** for Owner: 总体覆盖度、最高优先级补测、是否阻塞下一 slice.

The cross-review template lives at `docs/templates/codex-reviewer.md`. Owner triggers it via `/cross-review` slash command in Claude Code.
