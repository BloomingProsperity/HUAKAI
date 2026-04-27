---
name: release-readiness-gate
description: Use when deciding whether a release can proceed based on parity, clean-room status, acceptance tests, security, billing, UI operations, and mandatory roadmap items.
---

This file is agent-facing and authoritative.

# Release Readiness Gate

## Inputs

- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/05_CLEAN_ROOM_POLICY.md`
- `docs/10_RISK_REGISTER.md`
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`
- `docs/15_RELEASE_GATES.md`

## Gate Checks

1. Every reference feature has a valid disposition.
2. No feature is silently dropped.
3. Clean-room review has no unresolved contamination risk.
4. Acceptance tests cover normal, failure, and recovery paths.
5. Security risks have mitigations.
6. Billing and quota behavior is testable and reconcilable.
7. Admin UI supports operator workflows.
8. Mandatory roadmap items are either resolved or explicitly blocking.

## Output

Release decision: `Pass`, `Pass With Documented Exceptions`, or `Block`.

`Pass With Documented Exceptions` cannot be used to claim full parity when mandatory roadmap items remain open.
