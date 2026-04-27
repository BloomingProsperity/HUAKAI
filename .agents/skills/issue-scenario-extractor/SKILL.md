---
name: issue-scenario-extractor
description: Use when converting public issues, bug reports, discussions, or operational complaints into clean-room real-world scenarios and acceptance test ideas.
---

This file is agent-facing and authoritative.

# Issue Scenario Extractor

Full feature parity or better remains mandatory; issue-derived risk may change design, not remove capability.

## Purpose

Convert public issue evidence into production scenarios without copying code or implementation details.

## Workflow

1. Read the issue for user impact, trigger, environment, expected behavior, and observed failure.
2. Ignore implementation patches unless the license permits reuse and the owner explicitly allows it.
3. Write the scenario in `docs/08_REAL_WORLD_SCENARIOS.md`.
4. Add a bug pattern to `docs/09_BUG_PATTERN_LIBRARY.md` if the failure can recur.
5. Add an acceptance test idea to `docs/11_ACCEPTANCE_TEST_MATRIX.md`.
6. Link the issue as behavior evidence in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`.

## Extraction Template

- Actor:
- Preconditions:
- Trigger:
- Failure:
- Expected recovery:
- Capability affected:
- Risk:
- Acceptance test direction:

## Rule

Issues reveal production risk. They do not authorize copying fixes from non-MIT code.
