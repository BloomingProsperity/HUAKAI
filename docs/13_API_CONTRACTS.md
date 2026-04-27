This file is agent-facing and authoritative.

# API Contracts

API contracts must preserve full feature parity or better while using independent clean-room design.

## Purpose

Define the expected API surface at a product-contract level without copying reference implementation details.

## Contract Areas

- Authentication.
- User API keys.
- Provider accounts.
- Channels.
- Routes.
- Model registry and aliases.
- Gateway request handling.
- Protocol conversion.
- Quota and rate limits.
- Usage records.
- Billing records.
- Admin audit logs.
- Health and observability.
- Feature flags.
- Plugins.

## Contract Requirements

- APIs must be versioned or otherwise migration-safe.
- Dangerous operations must be permissioned and audited.
- Secrets must never be returned after creation unless explicitly designed as one-time reveal.
- Error responses must be actionable and must not leak secrets.
- Request IDs must support operational investigation.
- Compatibility APIs must preserve documented behavior across streaming and non-streaming flows.

## Clean-Room Rule

API behavior may be compatible with reference-observed user outcomes. Do not copy non-MIT route organization, schema names, handler structure, or implementation.
