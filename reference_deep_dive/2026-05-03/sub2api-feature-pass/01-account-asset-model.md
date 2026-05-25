# 01 Account asset model

## Sub2API behavior summary

Sub2API treats an upstream account as a composite schedulable asset. Each account carries identity material (platform and credential type), credential storage (secret material and supplementary metadata), transport affiliation (proxy binding), capacity controls (concurrency ceiling, load weighting, priority rank, cost multiplier), runtime blocking state (cooldown timestamps, overload expiry, temporary unschedulable flag with reason), and session-window context. Schedulability is not a single boolean — it is computed at request time by combining admin intent, credential validity, runtime cooldown, capacity state, and session context. Service-layer logic enforces RPM and window-cost checks as part of the scheduling decision.

## Entity / fields

Sub2API treats an upstream account as an asset: identity, credential material, transport affiliation, capacity controls, priority, cost multiplier, cooldown state, and session context live together on one entity.

## Request chain

`api key/group -> account candidates -> schedulability filters -> capacity lease -> credential injection -> upstream attempt -> usage/account state`.

## State machine

Not a single enum. It is composed from admin intent, credential state, runtime cooldown, capacity state and context state.

## Failure modes

- Valid credential can still be unschedulable because the account is overloaded or model-rate-limited.
- Healthy account can be unsafe for a specific session window.
- Proxy/TLS mismatch can reduce conversion stability even if logical auth passes.

## Sub2API capability

Account-as-asset structure and service-level schedulability are behavior-confirmed.

## HUAKAI current capability

HUAKAI has resource pool/slot concepts in `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:75` and `provider_accounts.in_flight_count` notes in `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:189`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: `provider_accounts` must become the canonical account asset, not just provider config.

## HUAKAI stronger design

Create `account_state_view` over account table and state events. Every attempt records `provider_account_id`, `account_state_snapshot`, `pool_group_id`, `api_key_binding_id` and `credential_version`.

## Suggested Feature ID / level

- `F-ACCAPI-ASSET-001`: L1
- `F-ACCAPI-STATE-001`: L1
- `F-ACCAPI-ASSET-OPS-001`: L2

## Acceptance tests

- Expired credential blocks scheduling even when account is enabled.
- Temporary unschedulable account is skipped with reason.
- Proxy/TLS profile changes affect new attempts only.

## Open questions

- open-question: SQL view vs materialized projection for `account_state_view`.

---
Source files read: sub2api backend/ent/schema/account, backend/internal/service/account
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
