---
name: production-scenario-review
description: Use when reviewing whether a planned feature, UI, API, or release handles realistic production operations, failures, abuse cases, and recovery workflows.
---

This file is agent-facing and authoritative.

# Production Scenario Review

Full feature parity or better remains mandatory; missing production scenarios are parity risks.

## Purpose

Ensure the platform works under real operator pressure, not only happy-path demos.

## Review Questions

- What happens when a provider is down?
- What happens when a provider account is disabled, expired, rate-limited, or out of balance?
- What happens when concurrent requests exhaust quota?
- Can an operator trace one failed request end to end?
- Are dangerous actions permissioned, confirmed, and audited?
- Are secrets redacted?
- Can the system recover without database surgery?

## Output

- Missing scenarios.
- Missing recovery flows.
- Missing observability.
- Missing tests.
- Risks to add to `docs/10_RISK_REGISTER.md`.
