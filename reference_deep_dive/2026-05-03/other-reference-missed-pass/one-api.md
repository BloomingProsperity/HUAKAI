# one-api missed pass

## Version

- Branch: `main`
- Commit: `8df4a2670b98`
- Tag: `8df4a26`
- Files: 564

## Source areas read

- Channel ability selection.
- Channel test and auto-disable.
- Token/user quota pre-consume and post-consume.
- Redis quota cache.

## Behavior-confirmed capabilities

- Channel selection is based on model/group ability and priority, with random choice among highest-priority channels.
- Channel status changes are tied back to ability availability so a disabled channel leaves routing immediately.
- Channel testing can auto-disable a failing channel as part of the test result path.
- Quota is reserved before the relay call and either returned or settled after the relay completes, making pre-consume a correctness guarantee rather than a heuristic.
- A Redis quota cache avoids every quota check hitting the primary database.

## HUAKAI gap

HUAKAI has ClaimGate and Settler concepts, but one-api shows two practical production lessons:

- reserve-before-forward is a user-facing correctness feature, not only an accounting detail;
- channel auto-disable must be linked to routing eligibility immediately, not only shown in a monitor.

## Upgrade design

- Turn HUAKAI's `ClaimGate` into a strict pre-call contract: reserve, forward, settle/refund, emit audit.
- Add route eligibility cache with versioned invalidation, not ad hoc Redis decrement.
- Require every auto-disable to produce `account_state_event` and change scheduler eligibility in the same transaction or outbox sequence.
- Make quota rollback idempotent by `claim_id`, not by request retry heuristics.

## Suggested Feature IDs

- `F-QUOTA-PRECONSUME-001` L1: pre-consume, refund, post-settle invariant.
- `F-CHANNEL-SELF-HEAL-001` L2: test-triggered auto-disable/re-enable with scheduler linkage.
- `F-ROUTE-ELIGIBILITY-CACHE-001` L2: model/group/channel eligibility cache with version pin.

## Acceptance test direction

- Simulate forwarder failure after pre-consume and assert quota returns exactly once.
- Simulate channel test failure and assert the next router plan excludes the channel.
- Run two concurrent refunds for the same claim and assert no double credit.

## Open questions

- Whether HUAKAI should cache quota in Redis for L1 or use Postgres-only until load testing proves need.
- Whether auto-disable should be immediate or require two failing probes in production.

---
Source files read: one-api model/ability, model/channel, controller/channel-test, relay/controller/helper, relay/controller/text, model/token, model/cache
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
