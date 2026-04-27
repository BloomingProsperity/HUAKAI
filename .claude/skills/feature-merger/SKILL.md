---
name: feature-merger
description: Use when combining overlapping reference features into a local merged equivalent without shrinking user outcomes or hiding required capabilities.
---

This file is agent-facing and authoritative.

# Feature Merger

## Purpose

Merge overlapping reference features only when the merged local capability fully preserves or improves every user outcome.

## Workflow

1. List all reference features being merged.
2. Identify each feature's user outcome.
3. Identify operator controls, API behavior, audit needs, and failure paths.
4. Define the local merged capability.
5. Prove that each original user outcome remains covered.
6. Add acceptance tests for the merged behavior.
7. Mark parity matrix rows as `Merged Equivalent`.

## Invalid Merge Signs

- A configuration option disappears without safe replacement.
- Operator visibility is reduced.
- Auditability is weaker.
- A reference workflow can no longer be completed.
- The merged feature is only a future idea.

## Output

A concise merge note suitable for `docs/03_FEATURE_PARITY_MATRIX.md`.
