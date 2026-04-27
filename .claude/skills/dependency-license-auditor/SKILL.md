---
name: dependency-license-auditor
description: Use when reviewing dependencies, package additions, transitive licenses, or release readiness for MIT compatibility and license-risk mitigation.
---

This file is agent-facing and authoritative.

# Dependency License Auditor

Full feature parity or better remains mandatory; license risk changes dependency strategy, not product scope.

## Purpose

Prevent dependency choices from contaminating the MIT project or forcing feature deletion.

## Workflow

1. Identify direct and transitive dependencies.
2. Record license type and source.
3. Flag GPL, AGPL, LGPL, unknown, custom, or unclear licenses.
4. Prefer permissive alternatives when possible.
5. If a risky dependency is required, propose isolation, replacement, plugin boundary, or mandatory roadmap.
6. Update `docs/10_RISK_REGISTER.md`.

## Rule

Dependency risk changes implementation strategy. It does not remove product obligations.

## Output

License findings, risk level, and mitigation recommendation.
