This file is agent-facing and authoritative.

# UI Contracts

UI contracts must preserve full feature parity or better for admin and operations workflows.

## Purpose

Define admin and operations UI obligations for the AI Gateway + Account Hub + Admin Ops Platform.

## UI Surfaces

- Dashboard overview (operator's first screen; surfaces Pool health, Edition mode, recent error trends).
- Users and groups.
- API keys.
- Provider accounts.
- **Pooling Groups** (relay-station identity per [01_PROJECT_BRIEF.md](01_PROJECT_BRIEF.md)) — list, detail, add/remove member Accounts, per-Pool health and balance view, per-Account hot-spot diagnostic.
- Channels.
- Routes and routing policies (pool-aware: shows which Pool a Route resolves to and why a given request landed on a specific Account).
- Models and aliases.
- Quota and rate limits (per-User × per-Account concurrency view).
- Usage analytics (with Pool / Account drill-down and sticky-session distribution).
- Billing records and adjustments (pool-aware reconciliation views).
- Request logs.
- Audit logs.
- Health checks and provider status.
- System settings (including Edition / run-mode display).
- Plugins and feature flags.

## Operations Requirements

- Search, filter, sort, paginate, and inspect major resources.
- Show status, ownership, risk, and last activity where relevant.
- Make dangerous actions explicit, permissioned, confirmed, and audited.
- Provide investigation paths from request to user, key, route, provider account, usage, billing, and audit events.
- Redact secrets by default.
- Expose feature flags and plugin states clearly.

## Clean-Room UI Rule

Reference UI may inform workflows and states. Do not copy non-MIT UI source, distinctive layout, styling, copy, component structure, or implementation detail.
