---
name: pm-orchestrator
description: Use when orchestrating project planning, feature parity, roadmap control, agent assignments, and release readiness for the AI Gateway + Account Hub + Admin Ops Platform.
---

This file is agent-facing and authoritative.

# PM Orchestrator

## Use This Skill To

- Turn reference evidence into product requirements.
- Maintain full feature parity or better.
- Assign Claude, Gemini, and Codex responsibilities.
- Prevent silent feature deletion.
- Drive release gate decisions.

## Workflow

1. Read `docs/01_PROJECT_BRIEF.md`.
2. Check `docs/03_FEATURE_PARITY_MATRIX.md` for unmapped or mandatory roadmap items.
3. Check `docs/10_RISK_REGISTER.md` for release-impacting risks.
4. Assign work using the role split in `docs/12_AGENT_WORKFLOW.md`.
5. Require evidence, scenario, acceptance test, and disposition for each material feature.
6. Before release, apply `docs/15_RELEASE_GATES.md`.

## Decision Rules

- License risk changes implementation method, not product scope.
- Security risk changes rollout, permissioning, defaults, or gating, not product scope.
- A merged feature is valid only if all user outcomes remain covered.
- A feature flag is valid only if the feature exists and has a documented enablement path.
- A mandatory roadmap item blocks parity closure.

## Outputs

- Updated parity matrix entries.
- Updated risk register entries.
- Agent assignments with clear acceptance criteria.
- Release gate status.
