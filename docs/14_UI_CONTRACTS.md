This file is agent-facing and authoritative.

# UI Contracts

UI contracts must preserve full feature parity or better for admin and operations workflows.

## Purpose

Define admin and operations UI obligations for the AI Gateway + Account Hub + Admin Ops Platform.

## UI Surfaces

- Dashboard overview.
- Users and groups.
- API keys.
- Provider accounts.
- Channels.
- Routes and routing policies.
- Models and aliases.
- Quota and rate limits.
- Usage analytics.
- Billing records and adjustments.
- Request logs.
- Audit logs.
- Health checks and provider status.
- System settings.
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
