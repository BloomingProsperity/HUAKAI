---
name: feature-parity-auditor
description: Use when checking whether every reference-derived feature is implemented, safely equivalent, pluginized, feature-flagged, merged equivalently, or placed on mandatory roadmap.
---

This file is agent-facing and authoritative.

# Feature Parity Auditor

## Purpose

Prevent silent feature loss.

## Inputs

- `docs/02_CAPABILITY_CONTRACT.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/04_FEATURE_LOCK.md`
- `docs/07_REFERENCE_EVIDENCE_LEDGER.md`
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`

## Audit Steps

1. List all reference evidence items.
2. Verify each has a parity matrix row.
3. Verify each row has one valid disposition.
4. Reject invalid dispositions such as dropped, ignored, not needed, or too risky.
5. Check that merged equivalents cover all user outcomes.
6. Check that safe equivalents preserve the feature while reducing risk.
7. Check that mandatory roadmap items are visible release blockers.
8. Check that each implemented or equivalent feature has an acceptance test direction.

## Output

- Missing feature rows.
- Invalid dispositions.
- Weak merged-equivalent claims.
- Missing tests.
- Release blockers.
