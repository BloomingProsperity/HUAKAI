This file is agent-facing and authoritative.

# Domain Model

## Purpose

Sketch the conceptual entity-relationship landscape so Phase 2 can lock contracts without re-debating what "channel" or "route" means. Definitions live in [18_GLOSSARY.md](18_GLOSSARY.md); this file shows how the entities relate.

This is a Phase 1 working draft. Owner must confirm before it is treated as a contract.

## Entity Map

```
                                 ┌────────────────┐
                                 │     User       │
                                 └────────┬───────┘
                                          │ owns
                                          ▼
                       ┌──────────────────────────────────────┐
                       │              API Key                 │
                       │  (issued by platform; never upstream)│
                       └────────┬─────────────────────────────┘
                                │ presented with each request
                                ▼
                    ┌────────────────────────┐         ┌─────────────────┐
                    │  User Group            │◀────────│   Quota         │
                    │ (rate, model allow)    │ enforces└─────────────────┘
                    └──────────┬─────────────┘
                               │ eligible-for
                               ▼
                    ┌────────────────────────┐
                    │        Route           │
                    │ (match -> Channel pref)│
                    └──────────┬─────────────┘
                               │ selects
                               ▼
                    ┌────────────────────────┐         ┌─────────────────┐
                    │       Channel          │◀────────│  Model Registry │
                    │ (allow list, mapping)  │ exposes └─────────────────┘
                    └──────────┬─────────────┘
                               │ pools
                               ▼
                    ┌────────────────────────┐
                    │   Provider Account     │
                    │ (upstream credential)  │
                    └──────────┬─────────────┘
                               │ belongs-to
                               ▼
                    ┌────────────────────────┐
                    │       Provider         │
                    └────────────────────────┘

  Per-request artifacts (immutable, append-only):
    Usage Record, Audit Event, Billing Ledger Entry
```

## Cardinalities (Working Assumptions)

| Relationship | Cardinality | Notes |
| --- | --- | --- |
| User → API Key | 1 → N | A User may rotate or hold multiple keys. |
| User → User Group | N → M | Users may belong to multiple groups. |
| User Group → Quota | 1 → N | Per-model and per-time-window quotas are separate rows. |
| Route → Channel preference | 1 → N (ordered) | Order encodes priority and fallback. |
| Channel → Provider Account | 1 → N | Pool. |
| Provider Account → Provider | N → 1 | Each Account belongs to exactly one Provider. |
| Channel → Model | N → M | Model exposure is per-Channel, with mapping rules. |
| Request → Usage Record | 1 → 1 | Streaming requests still produce one final record. |
| Usage Record → Billing Ledger Entry | 1 → 0..N | Some requests yield zero ledger entries (free tier, internal probe). |
| Audit Event → any mutable entity | N → 1 | Every dangerous mutation produces an Audit Event. |

## Invariants

These must hold in every implementation phase that introduces the corresponding entity. Violations are bugs, not design choices.

1. **API Key never carries upstream credentials.** Forwarding an API Key value upstream is a defect.
2. **Disabled Provider Account is not routable.** Route eligibility is computed from current Account state, not from a cached snapshot.
3. **Quota reservation precedes upstream spend.** A request that bypasses quota reservation cannot produce a Usage Record. (Exception: explicit dry-run probe requests, marked as such.)
4. **Usage Record is immutable.** Corrections happen via paired adjustment rows in the Billing Ledger, never by mutating an existing Usage Record.
5. **Audit Event covers every dangerous Admin action.** Bulk operations produce one Audit Event per affected entity, plus one bulk-summary Audit Event.
6. **Secrets are redacted at capture and at render.** Upstream credentials, API Key values past the first creation reveal, and any field marked sensitive must be redacted in logs, traces, and UI.

## Multi-Tenancy: Decided

**Decision (2026-04-28, [DR-001](decisions/DR-001-multi-tenancy.md)):** Tenant-aware from day 1 with a single default tenant in MVP. Multi-tenant admin UI deferred to Phase 9.

Concretely, every primary table carries a non-null `tenant_id` from the first schema. MVP hard-codes a single tenant constant; tenancy is server-resolved from the API Key and is not exposed to clients in MVP API contracts. Phase 9 adds: tenant onboarding, tenant suspension, tenant switcher in admin UI, per-tenant feature flags, and cross-tenant audit/investigation views.

Phase 2 schema review must include cross-tenant isolation tests: a User in tenant A must never observe rows scoped to tenant B, including via crafted query parameters, shared cache keys, or unscoped joins.

## Open Questions For Phase 2

1. Are User Groups orthogonal to Quota, or does each Group own its quota inline?
2. Is "Channel" allowed to mix Provider Accounts from different Providers (cross-provider failover)? Or is a Channel always single-Provider, and Routes do cross-Provider failover?
3. Does the Model Registry live globally, per-tenant, or per-Channel?
4. Are Routes tenant-scoped or global?
5. Streaming usage accounting: is the Usage Record written at end-of-stream, or progressively with a final settlement row?

## Resolved (Phase 1)

- **Implementation language for backend and frontend** — Decided in [DR-003](decisions/DR-003-technology-stack.md), 2026-04-28: Go for backend, TypeScript for frontend with types generated from the backend's OpenAPI / JSON Schema contract.
- **Multi-tenancy data shape** — Decided in [DR-001](decisions/DR-001-multi-tenancy.md): tenant-aware schema from day 1; MVP uses a single hard-coded default tenant.
