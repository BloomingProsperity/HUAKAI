# Hermes Native Integration Plan

Status: planning only. No runtime, database, API, or UI code is implemented by this document.

## Goal

HUAKAI should provide a native Hermes-style operations assistant from the Operations area of the product. As repositioned in WAVE H1 (2026-06-13), Hermes is an ADMIN/OPERATOR-facing ops assistant, not an end-user AI chat surface: it is gated to admin_tokens-authenticated operators behind the feature flag `HUAKAI_HERMES_ADMIN_ONLY` (default true; set false to restore the legacy end-user path for rollback). An authorized operator sees a simple enable switch and a streaming chat surface scoped to the tenant/user ops context they are acting within. When enabled, Hermes stays available until disabled. Conversation history and memory are retained as admin diagnostic sessions — encrypted at rest per migration 0091 and subject to the retention boundary — not as end-user data.

The integration should preserve the upstream Hermes/OpenClaw-style runtime as much as possible. HUAKAI should manage configuration, API access, permissions, billing visibility, and audit boundaries.

## Product Shape

The Operations module contains a Hermes entry with:

- An enable or disable switch.
- A normal streaming chat panel.
- Conversation history.
- Persisted memory.
- API source selection.
- Optional status, usage, and runtime logs for administrators.

Under the admin-only repositioning the Hermes entry lives in the operator/admin surface; the streaming chat panel, conversation history, and persisted memory are the operator's diagnostic sessions for the tenant/user context they select, not an end-user chat product.

Disabling Hermes stops new chats and background work for that operator-scoped context, but does not delete history, memory, API profiles, or configuration. Deletion must be a separate explicit action.

## Runtime Model

Use a singleton Hermes runner service, not one container per user.

```text
HUAKAI UI
  -> HUAKAI Gateway
     -> Hermes controller
     -> API profile, billing, RBAC, audit
     -> hermes-runner
        -> native Hermes/OpenClaw-style runtime
        -> HUAKAI API or external API
```

The runner is a separate service boundary from the Go gateway. It may run in the same Docker Compose deployment, but it should not be bundled into the gateway container.

The runner owns Hermes process lifecycle, workspace directories, skills, sessions, and runtime upgrades. The gateway owns identity, permissions, API keys, groups, billing facts, and audit records.

## API Sources

Hermes supports three API source modes:

- `managed_huakai_api`: HUAKAI automatically creates an internal API key for Hermes. Hermes calls the HUAKAI OpenAI-compatible API, and HUAKAI performs account-to-API routing.
- `dedicated_group`: The user or administrator selects a group or pool reserved for Hermes usage. This is the recommended default because usage, quota, and policy stay isolated.
- `external_api`: The user provides an external API endpoint and key. HUAKAI stores the key encrypted and renders it into the Hermes runtime configuration.

Future work may add hybrid fallback, but the first implementation should keep selection explicit.

## Data Isolation

There is one Hermes runner, but every user or tenant must have isolated state.

Suggested logical storage:

```text
/var/lib/huakai/hermes/
  instances/global/
  tenants/{tenant_id}/users/{user_id}/
    conversations/
    memory/
    sessions/
    runtime-state/
```

No user should share long-term memory or conversation state with another tenant or user unless a later enterprise workspace feature explicitly grants that behavior.

## Suggested Tables

```text
hermes_settings
hermes_api_profiles
hermes_conversations
hermes_messages
hermes_memory_items
hermes_tool_calls
hermes_audit_events
hermes_runtime_events
```

`hermes_tool_calls` and `hermes_audit_events` are mandatory before enabling any HUAKAI operations tool. Every tool invocation must record actor, tenant, requested action, sanitized arguments, result, and correlation IDs.

## Suggested APIs

```text
GET    /v1/hermes/settings
POST   /v1/hermes/settings/enable
POST   /v1/hermes/settings/disable

POST   /v1/hermes/chat
GET    /v1/hermes/conversations
GET    /v1/hermes/conversations/{conversation_id}
DELETE /v1/hermes/conversations/{conversation_id}

GET    /v1/hermes/memory
DELETE /v1/hermes/memory/{memory_id}

GET    /v1/hermes/api-profiles
POST   /v1/hermes/api-profiles
POST   /v1/hermes/api-profiles/{profile_id}/rotate-key
```

The UI talks only to HUAKAI Gateway. The browser should not call the Hermes runner directly.

## HUAKAI Skill Pack

Hermes may receive a HUAKAI skill pack that exposes controlled operations tools:

```text
huakai.request_diagnose
huakai.credential_diagnose
huakai.renew_trigger
huakai.dlq_inspect
huakai.dlq_replay
huakai.account_pause
huakai.account_resume
huakai.audit_lookup
```

These tools must be mediated by HUAKAI Gateway. Hermes submits a tool request; HUAKAI checks RBAC, policy, tenant scope, and audit requirements before execution.

## Deployment

Recommended Docker Compose shape:

```text
gateway
postgres
hermes-runner
rust-tls-sidecar optional
```

The `hermes-runner` service may be idle when no user has Hermes enabled. The UI switch should not need to create Docker containers at runtime. It should enable or disable the feature state and ask the runner to start or stop background activity for that user or tenant.

## Upgrades

Hermes runtime upgrades should be independent from gateway upgrades:

- Gateway image: HUAKAI API, RBAC, billing, audit, and UI contracts.
- Hermes runner image: Hermes runtime, process supervisor, tool bridge, runtime dependencies.
- HUAKAI skill pack: versioned skills and tool schemas.

Upgrades must preserve Hermes data volumes, conversations, memory, API profiles, and settings. Rollback should be possible by restoring the previous runner image while keeping the same data volume when schema compatibility allows it.

## Go and Rust Boundaries

Go owns the Hermes integration:

- settings
- API profiles
- user and tenant isolation
- billing and usage attribution
- audit
- tool gateway
- gateway-to-runner proxy

Rust should not own Hermes lifecycle or memory. Rust may expose transport, TLS sidecar, or mimicry health facts to Go, which Hermes can later read through HUAKAI tools.

## First Implementation Slice

The first slice should implement only the native hosting spine:

1. `hermes_settings` and API profile storage.
2. One singleton `hermes-runner` service definition.
3. Gateway endpoints for enable, disable, settings, chat proxy, conversations, and messages.
4. Dedicated Hermes API key or group creation path.
5. Basic audit events for enable, disable, profile changes, and chat starts.

Operations tools should come after the chat and persistence path is stable.

## Non-Goals

- Do not implement a separate Hermes container per user.
- Do not let the browser talk directly to Hermes.
- Do not allow Hermes direct database access.
- Do not store plaintext external API keys.
- Do not share memory across tenants by default.
- Do not implement automatic production repair in the first slice.

