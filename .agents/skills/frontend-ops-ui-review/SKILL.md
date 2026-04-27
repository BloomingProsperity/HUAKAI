---
name: frontend-ops-ui-review
description: Use when designing or reviewing admin operations UI for gateway, account hub, quota, billing, logs, audit, provider health, routing, plugins, and feature flags.
---

This file is agent-facing and authoritative.

# Frontend Ops UI Review

## Purpose

Ensure the admin UI supports real operations workflows and full feature parity.

## Review Checklist

- Major resources have search, filter, sort, pagination, and detail inspection.
- Dangerous actions are permissioned, confirmed, and audited.
- Secrets are redacted by default.
- Status, owner, last activity, limits, and health are visible where relevant.
- Operators can trace request to user, key, route, provider account, usage, billing, and audit events.
- Feature flags and plugin states are visible.
- No reference UI source, distinctive layout, styling, or copy is copied from non-MIT projects.

## Output

UI gaps, parity gaps, operator workflow gaps, and acceptance test suggestions.
