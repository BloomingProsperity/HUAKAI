This file is agent-facing and authoritative.

# Glossary

## Purpose

Pin down the meaning of overloaded domain terms used across this project. AI gateway / account hub products use words like "channel", "provider", "account", "key", "model", "route" inconsistently in the wild. Local definitions take precedence over reference-project usage.

This glossary is a Phase 1 working draft. Owner confirmation is required before any term is locked into API or UI contracts.

## Core Entities

### Provider

The upstream AI service operator. Examples: OpenAI, Anthropic, Google (Gemini), DeepSeek, Azure OpenAI, AWS Bedrock.

A Provider is identified by a stable internal slug (e.g. `openai`, `anthropic`). It is **not** a deployment or a credential.

### Provider Account

A single set of upstream credentials belonging to a Provider, owned by the platform operator (not by an end user).

A Provider Account holds:

- Provider reference.
- One or more upstream credentials (API key, OAuth refresh token, or signed session).
- Lifecycle state (`active`, `disabled`, `expired`, `quota-exhausted`, `under-investigation`).
- Optional metadata: tier, balance, daily limit, region, label.

A Provider Account is the **smallest routable unit** on the upstream side.

### Channel

A configurable bridge between the gateway and one or more Provider Accounts, exposing a slice of capability to Routes.

A Channel holds:

- Provider Account selection policy (single account, pool, or weighted set).
- Allowed model list and model-to-upstream-name mapping.
- Per-channel limits (rate, concurrency, daily token cap).
- Status (`enabled`, `paused`, `degraded`).

A Channel is **not** a Provider Account; it is a higher-level grouping. A Channel may pool multiple Accounts of the same Provider, or constrain which Accounts a Route can reach.

### Route

The selection rule that turns an incoming request into a Channel choice.

A Route holds:

- Match criteria (model name, requesting User Group, request tag, source IP allow-list).
- Ordered Channel preference list with weights and fallback rules.
- Failover policy.

A Route is **how the gateway decides where a request goes**. A Channel is **what the gateway is choosing between**.

### Model

A logical model identifier exposed to clients (e.g. `gpt-4o`, `claude-sonnet-4-6`).

The Model Registry maps each logical Model to one or more upstream Provider model names per Channel. A Model may have aliases (e.g. `claude-3.5-sonnet` → `claude-sonnet-4-6`).

### User

The end consumer of the gateway. A User authenticates against the platform itself (not the upstream Provider).

### User Group

A named collection of Users with shared quota, rate limits, allowed models, and Route eligibility.

### API Key

A platform-issued credential owned by a User. The User presents an API Key to the gateway; the gateway resolves the User, applies quota and authorization, and forwards the request through a Route to a Channel to a Provider Account.

API Keys are **never** upstream Provider credentials. The two must not be conflated in code, logs, or UI.

### Quota

A reservable budget tied to a User, User Group, Model, Provider, or time window. Quota is **decremented atomically before** upstream spend occurs, and reconciled after the request completes.

### Usage Record

An immutable row describing one request: User, API Key, Model, Channel, Provider Account, token counts, cost context, status, request id, timestamps. Usage Records feed billing, quota, observability, and audit.

### Billing Ledger

The append-only money-movement log. Distinct from Usage Records: a Usage Record describes consumption; a Billing Ledger entry describes a charge, recharge, deduction, or admin correction. Reconciliation joins the two.

### Audit Event

An immutable row describing an operator action that changed system state: who, what target, what action, when, request id, before/after summary. Audit Events are required for every dangerous Admin operation.

## Pipeline Direction

```
Client request
  -> API Key auth
  -> User + User Group resolution
  -> Quota reservation
  -> Route match
  -> Channel selection
  -> Provider Account selection (within Channel)
  -> Upstream Provider call
  -> Response + Usage Record + Quota reconciliation
  -> Optional Billing Ledger entry
```

## Terms To Avoid

- "Token" alone is ambiguous (API auth token? LLM token unit?). Use "API Key" for auth credentials; use "request token" or "completion token" for model accounting units.
- "Account" alone is ambiguous (User account? Provider Account?). Always qualify.
- "Key" alone is ambiguous. Use "API Key" (issued to User) or "upstream credential" (held inside Provider Account).
- "Endpoint" alone is ambiguous. Use "Provider endpoint" (upstream URL) or "gateway endpoint" (our own surface).
