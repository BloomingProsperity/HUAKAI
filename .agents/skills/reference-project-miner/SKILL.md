---
name: reference-project-miner
description: Use when studying Sub2API, New API, All API Hub, or similar maintained projects to extract clean-room feature evidence, workflows, risks, and test ideas without copying implementation.
---

This file is agent-facing and authoritative.

# Reference Project Miner

## Allowed Inputs

- Public docs.
- Public issues and discussions.
- Release notes.
- Public demos or screenshots.
- Observable API behavior.
- User-visible configuration concepts.

## Forbidden Inputs From Non-MIT References

- Source code.
- File structure.
- Comments.
- Database schemas.
- UI source.
- Distinctive naming or implementation details.
- Copied tests.

## Workflow

1. Identify a user-visible behavior, workflow, risk, or test expectation.
2. Record it in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`.
3. Convert it into a feature candidate.
4. Add or update the matching row in `docs/03_FEATURE_PARITY_MATRIX.md`.
5. If risky, add a mitigation to `docs/10_RISK_REGISTER.md`.
6. If user-facing, add a scenario to `docs/08_REAL_WORLD_SCENARIOS.md`.

## Mandatory: Deep Decomposition Per Reference Per Feature

Per Owner directive 2026-04-28 ([22_DEEP_MINING_MANDATE.md](../../docs/22_DEEP_MINING_MANDATE.md)): README-only evidence is insufficient for L1/L2 features. Every L1/L2 row in `docs/03_FEATURE_PARITY_MATRIX.md` must cite at least one `E-X-DEEP-NNN` row whose `Source Type` is `Source code (deep read)`, with verified URL attribution. When a feature appears across multiple references, each cited reference must contribute its own `E-X-DEEP-NNN` row. The Algorithmic Insights for HUAKAI Core section in `docs/07_REFERENCE_EVIDENCE_LEDGER.md` must carry a KEEP / IMPROVE / AVOID directive for every L1/L2 feature. Phase 1 cannot exit until this mandate is satisfied.

## Evidence Format

Capture what the user can do or observe. Do not capture how the reference implemented it.

## Output

Clean-room evidence rows and parity candidates.
