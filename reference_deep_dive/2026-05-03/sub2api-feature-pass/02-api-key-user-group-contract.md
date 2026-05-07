# 02 API key / user / group contract

## Sub2API behavior summary

Sub2API models a layered identity and quota contract: a user owns balance, concurrency ceiling, and RPM limit; an API key belongs to a user and group, carries IP allowlist rules, quota ceiling, consumed quota, expiry, and multi-window rate/usage counters; a group adds exclusivity mode, status, fallback group reference, and RPM behavior; user-allowed-group membership is a first-class relationship. The contract is hierarchical: public key resolves user and group, user capacity and key quota/rate are checked before account pool selection, and group constrains candidate accounts and fallback policy.

## Entity / fields

The contract is `user -> api_key -> group -> allowed group -> account group`. It includes user balance/concurrency/RPM, API key quota/rate/expiry/IP rules, group exclusivity/fallback/model routing and allowed group membership.

## Request chain

Public key resolves user and group. User capacity and key quota/rate are checked before account pool selection. Group constrains candidate accounts and fallback policy.

## State machine

API key active -> quota exhausted/expired/disabled. User active -> disabled/concurrency limited. Group active -> disabled/exclusive/fallback eligible.

## Failure modes

- A valid key can fail because group is disabled.
- User capacity can fail before account capacity.
- Group exclusivity can make fallback unsafe unless explicit.

## Sub2API capability

Sub2API has a concrete user/key/group contract rather than bare API keys.

## HUAKAI current capability

`APIKeyBinding` is currently identified as missing in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:41`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: `APIKeyBinding` should absorb contract semantics, not just link one key to one account.

## HUAKAI stronger design

Define `api_key_bindings`: `api_key_id`, `tenant_id`, `pool_group_id`, `priority`, `weight`, `fallback_policy`, `max_concurrency`, `max_waiting`, `wait_timeout_ms`, `state`, `reason`, `version`. Capture `api_key_contract_snapshot` per request.

## Suggested Feature ID / level

- `F-ACCAPI-BIND-001`: L1
- `F-ACCAPI-CONTRACT-001`: L1
- `F-ACCAPI-KEY-CAPACITY-001`: L1

## Acceptance tests

- Key bound to pool A cannot use pool B.
- Key quota exhaustion differs from account pool exhaustion.
- User concurrency full does not acquire account slot.

## Open questions

- open-question: whether HUAKAI needs separate groups or can use `pool_groups + bindings`.

---
Source files read: sub2api backend/ent/schema/api_key, backend/ent/schema/user, backend/ent/schema/group, backend/ent/schema/user_allowed_group
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
