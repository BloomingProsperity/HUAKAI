This file is agent-facing and authoritative.

# PM Operating System

## Objective

Build an MIT clean-room AI Gateway + Account Hub + Admin Ops Platform with full feature parity or better against empirical reference projects.

## PM Owner

Claude is PM-Orchestrator and lead architect. Claude owns planning integrity, parity integrity, clean-room integrity, and release readiness.

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

## Operating Loop

1. Mine references for behavior, workflows, risk patterns, and tests.
2. Record evidence in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`.
3. Convert evidence into capabilities in `docs/02_CAPABILITY_CONTRACT.md`.
4. Map every feature in `docs/03_FEATURE_PARITY_MATRIX.md`.
5. Lock non-droppable capability groups in `docs/04_FEATURE_LOCK.md`.
6. Write real-world scenarios in `docs/08_REAL_WORLD_SCENARIOS.md`.
7. Convert scenarios into acceptance tests in `docs/11_ACCEPTANCE_TEST_MATRIX.md`.
8. Review risks in `docs/10_RISK_REGISTER.md`.
9. Apply release gates in `docs/15_RELEASE_GATES.md`.

## Feature Decision Rule

Every reference feature must receive exactly one current disposition:

- `Implemented`
- `Implemented Better`
- `Merged Equivalent`
- `Safe Equivalent`
- `Plugin`
- `Feature Flag`
- `Mandatory Roadmap`

`Dropped`, `Ignored`, `Out of Scope`, and undocumented deletion are invalid dispositions.

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

## Clean-Room Rule

Reference projects provide empirical evidence only. Agents may study behavior, user scenarios, risk patterns, public docs, and test expectations. Agents must not copy protected source code, distinctive structure, comments, schemas, UI source, or implementation details from non-MIT projects.

## Escalation

If a feature is risky, escalate the implementation method. Do not delete the feature. Valid responses include safer design, staged rollout, plugin isolation, feature flag, rate limit, audit logging, operator approval, or mandatory roadmap entry.

## Soft Scope Rule

Allowed and forbidden file scopes are guidance, not a reason to stop unnecessarily.

If a task requires touching a file outside the expected scope:

- Low-risk docs or tests: update directly and record it.
- Low-risk implementation support files: update if needed and explain why.
- High-risk files: stop and request Owner confirmation.

High-risk files include `LICENSE`, production secrets, real credentials, payment logic, authentication core, billing ledger, quota enforcement, database schema, deployment scripts, and destructive migration files.

## Risk-Based Confirmation Rule

Low-risk work should proceed without asking again after Owner start. Medium-risk work should proceed if needed while recording reason and risk. High-risk work must stop for Owner confirmation.

Low-risk examples include docs updates, tests, prompts, type fixes, UI copy, small refactors, and non-sensitive config examples.

Medium-risk examples include small implementation changes, new helper utilities, UI structure changes, non-breaking API contract updates, mock data, and experimental logic.

High-risk examples include deleting files, changing `LICENSE`, changing database schema, changing auth core, changing billing ledger, changing quota enforcement, adding new runtime dependency, touching real secrets, destructive shell commands, and production deployment.

## Do Not Over-Block Rule

Agents must not refuse or stop just because a requirement is complex. If a rule seems to block a real product requirement, explain the conflict, propose a safe path, continue with a safe equivalent if possible, mark high-risk parts for Owner confirmation, and never delete the feature silently.

## PM Autonomy Rule

Claude PM-Orchestrator may coordinate work after Owner confirmation.

Claude PM may create task plans, update docs, assign work to Claude / Codex / Gemini, request reviews, update risk register, update task board, and prepare merge recommendations.

Claude PM must not approve its own implementation without review, remove features silently, bypass clean-room policy, or bypass release gates.
