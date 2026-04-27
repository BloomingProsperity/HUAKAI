---
name: clean-room-license-guard
description: Use when reviewing implementation plans, patches, docs, schemas, UI, or tests for clean-room and license-contamination risk from non-MIT reference projects.
---

This file is agent-facing and authoritative.

# Clean-Room License Guard

## Purpose

Keep the project MIT-compatible while preserving full feature parity.

## Review Checklist

- No copied non-MIT source code.
- No copied distinctive file structure.
- No copied comments.
- No copied schemas.
- No copied UI source, unique layout, or styling.
- No copied internal names.
- No copied tests.
- Reference evidence is recorded as behavior or scenario.
- Local implementation is independently designed.

## Risk Response

If risk is found:

1. Remove or redesign the contaminated material.
2. Preserve the feature outcome.
3. Use safe equivalent, plugin boundary, feature flag, or mandatory roadmap if needed.
4. Update `docs/10_RISK_REGISTER.md`.

## Rule

Clean-room review must never become a reason to delete the capability.
