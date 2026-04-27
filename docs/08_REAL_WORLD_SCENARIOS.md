This file is agent-facing and authoritative.

# Real-World Scenarios

Scenarios protect full feature parity by proving that local behavior covers real reference-derived outcomes.

## Purpose

Scenarios define what the platform must handle in production. They are the bridge between reference evidence and acceptance tests.

## Scenario Groups

### Gateway Operations

- Route traffic across multiple providers when a preferred provider is healthy.
- Fail over when a provider account is disabled, exhausted, rate-limited, or unhealthy.
- Preserve streaming behavior across compatible providers.
- Normalize provider errors into operator-actionable responses.

### Account Hub

- Rotate a provider credential without downtime.
- Disable a compromised account and remove it from routing.
- Assign accounts to channels, users, groups, or route policies.
- Detect expired, invalid, or quota-exhausted credentials.

### Quota And Billing

- Enforce quota before provider spend occurs.
- Record token and request usage accurately.
- Reconcile user balance, provider cost, and admin adjustments.
- Prevent negative balance abuse unless explicitly configured.

### Admin Operations

- Search, filter, sort, paginate, and inspect users, keys, channels, providers, routes, accounts, logs, usage, billing records, and audit events.
- Perform bulk operations with confirmation and audit trail.
- Investigate a failed request from user to route to provider account to billing record.

### Security

- Redact secrets in logs and UI.
- Require permissioned access for dangerous operations.
- Preserve audit history for admin changes.
- Block leaked credentials and unsafe configuration.

## Scenario Rule

Every material feature must have at least one scenario before release readiness can be claimed.
