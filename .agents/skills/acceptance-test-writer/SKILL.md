---
name: acceptance-test-writer
description: Use when turning capability contracts, real-world scenarios, bug patterns, and parity obligations into acceptance tests for normal, failure, and operator recovery paths.
---

This file is agent-facing and authoritative.

# Acceptance Test Writer

## Inputs

- `docs/02_CAPABILITY_CONTRACT.md`
- `docs/08_REAL_WORLD_SCENARIOS.md`
- `docs/09_BUG_PATTERN_LIBRARY.md`
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`

## Workflow

1. Select a capability and scenario.
2. Define preconditions.
3. Define normal path steps.
4. Define failure path steps.
5. Define operator recovery steps.
6. Define expected result and audit/usage/log evidence.
7. Add or update the test matrix.

## Required Coverage

- Gateway routing.
- Provider/account lifecycle.
- Quota and billing.
- Protocol compatibility.
- Admin operations.
- Security and secrets.
- Observability.

## Output

Acceptance test rows with clear expected results.
