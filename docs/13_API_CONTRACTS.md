This file is agent-facing and authoritative.

# API Contracts

API contracts must preserve full feature parity or better while using independent clean-room design.

## Purpose

Define the expected API surface at a product-contract level without copying reference implementation details.

## Contract Areas

- Authentication.
- User API keys.
- Provider accounts.
- **Pooling Groups** (relay-station identity per [01_PROJECT_BRIEF.md](01_PROJECT_BRIEF.md)) — first-class entity with CRUD, member-Account add/remove, health view, and routing-policy attachment.
- Channels.
- Routes (resolves to a Pooling Group + Account-selection policy).
- Model registry and aliases.
- Gateway request handling (including sticky session routing and pool-aware Account selection).
- Protocol conversion.
- Quota and rate limits (including per-User × per-Account concurrency caps).
- Usage records (with Pooling Group, chosen Account, and routing reason fields).
- Billing records (pool-aware reconciliation).
- Edition / run-mode introspection endpoint (read-only; surfaces which Edition this deployment runs as, per [DR-002](process/decisions/DR-002-product-editions.md)).
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
- **OpenAPI / JSON Schema is the contract source of truth** ([DR-003](process/decisions/DR-003-technology-stack.md)). The Go backend defines the contract via OpenAPI artifact; the TypeScript frontend's request/response types are generated from that artifact via codegen. Hand-written shared types between backend and frontend are not allowed.

## Clean-Room Rule

API behavior may be compatible with reference-observed user outcomes. Do not copy non-MIT route organization, schema names, handler structure, or implementation.
