# 10 Model routing / capability

## Sub2API behavior summary

Sub2API handles model routing across three levels: the group entity carries model routing rules, scope assignments, and dispatch settings; the account service has account-level model mapping and support checks including explicit compact compatibility checks; the scheduler filters candidates by image capability, compact capability, and model capability. A gateway-level channel mapping and model list cache exist. The full chain resolves requested model through group and account mapping, applies capability filtering, and determines the upstream model and billing model.

## Entity / fields

Model capability spans group-level routing rules, account-level model mapping, account capability flags, request capability requirements, billing model source, and routing cache.

## Request chain

Requested model -> route/group/account mapping -> capability filter -> upstream model -> billing model.

## State machine

`requested -> mapped -> capability_checked -> upstream_model_resolved -> billing_model_resolved -> snapshot_captured`.

## Failure modes

- Mutable admin mapping changes in-flight request semantics.
- Account supports model but not required compact/image mode.
- Billing model mismatch causes wrong cost.

## Sub2API capability

Sub2API has group/account model routing and scheduler capability filters.

## HUAKAI current capability

Audit says current `CapabilitySnapshot` is too coarse in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:341`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: mutable `model_allow_list/capability_flags` are not enough.

## HUAKAI stronger design

Persist `CapabilitySnapshot`: requested model, mapped upstream model, billing model, required capability, account capability, mapping version, account capability version.

## Suggested Feature ID / level

- `F-ACCAPI-CAP-SNAP-001`: L1
- `F-MODEL-ROUTE-001`: L2
- `F-MODEL-BILLING-SOURCE-001`: L1

## Acceptance tests

- Admin edits mapping after request start; attempt keeps original snapshot.
- Account lacking image capability is excluded with reason.
- Usage stores requested/mapped/billing model.

## Open questions

- open-question: JSONB snapshot vs normalized snapshot tables.

---
Source files read: sub2api backend/ent/schema/group, backend/internal/service/account, backend/internal/service/openai_account_scheduler, backend/internal/service/gateway_service
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
