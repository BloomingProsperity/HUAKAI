This file is agent-facing and authoritative.

# Feature Lock

Full feature parity or better is mandatory. Feature lock exists to prevent capability shrinkage.

## Purpose

Feature lock prevents silent product shrinkage during clean-room design, security review, refactoring, or implementation.

## Locked Capability Groups

- Gateway provider management.
- Account and credential hub.
- Channel and routing controls.
- Protocol compatibility and conversion.
- Model registry and model aliasing.
- Quota, rate limits, usage, and billing.
- Admin operations dashboard.
- Health checks and reliability controls.
- Logs, audit trail, analytics, and observability.
- Authentication, authorization, and secret protection.
- Plugin and feature-flag extension paths.
- Scenario and acceptance test coverage.

## Change Control

A locked feature may only change through one of these outcomes:

- Stronger implementation.
- Merged equivalent.
- Safe equivalent.
- Plugin boundary.
- Feature flag.
- Mandatory roadmap entry.

Any proposal that removes a locked capability without one of those outcomes must be rejected.
