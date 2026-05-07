# 04 Concurrency slot / wait plan

## Sub2API behavior summary

Sub2API maintains runtime concurrency leases and counters separately for account slots and user slots, both stored in cache. Acquisition of account and user slots are independent operations. Waiting counters exist for both dimensions. Expired account slots are cleaned on a schedule and batch-read. A wait-with-timeout path exists in the gateway helper. Sticky and fallback wait timeouts plus maximum waiting counts are configurable. An ops view aggregates capacity and waiting counts by account, group, and platform. This is not just a column on the account table — it is a live cache-backed lease system.

## Entity / fields

Runtime leases/counters exist for account slot, user slot, wait count and cleanup. This is not just an `account.concurrency` column.

## Request chain

Resolve key/user/group -> acquire user slot -> select account -> acquire or wait for account slot -> forward -> release.

## State machine

`candidate_selected -> user_wait/acquired -> account_wait/acquired -> forwarding -> released -> cleanup_if_stale`.

## Failure modes

- Forced key-account binding still needs concurrency behavior.
- Account full and user full require different denial reasons.
- Slot lease is not credential lease.

## Sub2API capability

Sub2API already has account/user slots, wait counters, timeout and ops capacity views.

## HUAKAI current capability

HUAKAI uses `pool_slot_acquisitions.id` as `lease_id` in `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:75`, while the audit says this is not a credential lease in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:38`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: account concurrency is core. Binding an API key to account/pool must define wait/fallback/fast-fail behavior.

## HUAKAI stronger design

Add `CapacityPlan` to binding: max account/user concurrency, max waiting, wait timeout, fallback on full and stream wait keepalive. Keep `credential_leases` separate from `pool_slot_acquisitions`.

## Suggested Feature ID / level

- `F-ACCAPI-CAPACITY-001`: L1
- `F-ACCAPI-WAIT-001`: L1
- `F-ACCAPI-CRED-LEASE-001`: L1

## Acceptance tests

- Full bound account waits, then falls back or fails by policy.
- User capacity denial happens before account slot acquisition.
- Stale slot cleanup emits metric and releases capacity.

## Open questions

- open-question: Redis/cache counters vs DB rows for wait queues.

---
Source files read: sub2api backend/internal/service/concurrency_service, backend/internal/handler/gateway_helper, backend/internal/config/config, backend/internal/service/ops_concurrency
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
