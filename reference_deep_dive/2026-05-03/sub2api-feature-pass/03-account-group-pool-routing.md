# 03 Account group / pool routing

## Sub2API behavior summary

Sub2API uses a many-to-many account-group join with per-binding priority, indexed and navigable from both sides. The group entity carries normal and invalid-request fallback group references, model routing configuration, scope assignments, and dispatch settings. Admin paths support group copy and bind operations that filter accounts and attach them to groups. Pool membership (account-groups) holds priority; the group adds fallback, model routing, scopes, and dispatch policy on top.

## Entity / fields

`account_groups` is pool membership with priority. `groups` adds fallback, model routing, scopes and dispatch policy.

## Request chain

API key selects group. Group selects account pool. Scheduler filters account state/capacity/capability. If policy allows, fallback group expands candidates.

## State machine

Primary group available -> select account; primary exhausted -> fallback group; invalid request -> invalid-request fallback; model mapping enabled -> mapped model/account capability.

## Failure modes

- Fallback without audit makes account choice opaque.
- Priority without load scoring can overload preferred accounts.
- Mixed account types in one group can break context compatibility.

## Sub2API capability

Group M2M, priority, fallback and model routing are behavior-confirmed.

## HUAKAI current capability

HUAKAI has Router/Resource Pool/Executor concepts in `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:28`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: `pool_groups`, `api_key_bindings` and `routes` must form one deterministic selection contract.

## HUAKAI stronger design

Persist `RoutePlan` and `AccountSelectionPlan`: selected pool, fallback chain, candidate order, exclusion reasons, scoring inputs and final reason.

## Suggested Feature ID / level

- `F-ACCAPI-POOL-ORDER-001`: L1
- `F-ACCAPI-FALLBACK-GROUP-001`: L1
- `F-ROUTE-PLAN-001`: L1

## Acceptance tests

- Fallback group used only on configured exhaustion/error.
- Candidate exclusion reasons are visible.
- Admin routing edits do not mutate in-flight plan.

## Open questions

- open-question: model routing ownership between `routes` and `pool_groups`.

---
Source files read: sub2api backend/ent/schema/account_group, backend/ent/schema/group, backend/internal/service/admin_service
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
