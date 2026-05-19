This file is agent-facing and authoritative.

# Capability Contract

## Contract Rule

Capabilities are product obligations. A capability may be redesigned, merged, isolated, or staged, but it may not disappear because it is inconvenient, risky, or present in a non-MIT reference.

## Core Capabilities

### Gateway

- Provider abstraction.
- Model registry and aliasing.
- Routing rules.
- Weighted routing.
- Fallback routing.
- Retry and timeout controls.
- Streaming and non-streaming request handling.
- Request and response normalization.
- Error normalization.

### Protocol

- OpenAI-compatible API surface.
- Provider-specific protocol adapters.
- Safe protocol conversion.
- Request validation.
- Response compatibility.

### Account Hub

- Provider account inventory.
- API key and token management.
- Account health status.
- Account assignment to channels, users, groups, or routes.
- Credential rotation workflow.
- Expiration and disablement controls.

### Authentication and Identity

- Pluggable authentication-provider abstraction supporting email/password and one or more OAuth identity sources without hardcoding any one source.
- Session persistence requiring an operator-supplied secret; default-generated secrets must not be accepted in production.
- First-run bootstrap workflow that forces a credential change before any other privileged operation.
- Single Sign-On (SSO) integration as a Plugin (Personal Edition L3 / SaaS Edition L4); see [DR-002](process/decisions/DR-002-product-editions.md).

### Channels And Providers

- Channel creation and configuration.
- Provider endpoint configuration.
- Model availability controls.
- Channel status, priority, and limits.
- Provider-specific settings without leaking implementation assumptions into shared contracts.

### Provider Account Pool (Relay-Station Identity)

- Pooling Group as a first-class entity that aggregates one or more Provider Accounts into one logical capacity (per [01_PROJECT_BRIEF.md §Product Identity](01_PROJECT_BRIEF.md), [DR-002](process/decisions/DR-002-product-editions.md)).
- Pool-level health view: per-Pool aggregate balance / availability / failure count, plus per-Account detail.
- Pool-aware routing: Routes resolve to a Pool, then a policy selects a Provider Account within that Pool.
- Sticky session routing: same-session multi-turn requests pin to the same Account within a Pool.
- Per-User × per-Account independent concurrency caps when pooling is active.
- Token-level usage attribution that fairly accounts pooled consumption back to the requesting User.
- Pool-aware billing reconciliation: usage may span multiple Accounts; Billing Ledger entries reference the Pool, the chosen Account, and the routing reason.

### Edition / Run Mode

- Edition flag plumbing that toggles SaaS-only features (payment, multi-tenant onboarding, cross-tenant abuse tools) off in Personal Edition deployments per [DR-002](process/decisions/DR-002-product-editions.md).
- Cost-ceiling alert mandatory in every Edition (even when payment is disabled) so unmetered Personal Edition deployments still surface runaway spend.

### Quota, Billing, And Usage

- User and group quota.
- Per-model and per-provider cost accounting.
- Token and request usage records.
- Recharge, deduction, balance, or credit workflows where applicable.
- Admin correction and audit trail.

### Reliability

- Health checks.
- Circuit breakers.
- Backoff and retry policy.
- Failover.
- Degraded-mode visibility.

### Security

- Authentication and authorization.
- Secret redaction.
- Admin audit logs.
- Abuse controls.
- Permissioned operations.
- Secure defaults.

### Observability

- Request logs.
- Usage analytics.
- Provider/account health views.
- Error trends.
- Operator alerts and investigation surfaces.

### Admin Operations

- User, key, account, channel, route, provider, quota, billing, usage, log, and system setting management.
- Bulk operations where scenarios justify them.
- Filtering, search, sorting, pagination, export, and audit visibility.

## Disposition Requirement

Each capability must be mapped in `docs/03_FEATURE_PARITY_MATRIX.md` before release.
