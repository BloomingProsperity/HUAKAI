# 15 Async workers / cleanup

## Sub2API behavior summary

Sub2API uses bounded worker pools for usage writing: the pool tracks overflow, supports a synchronous fallback, exposes stats, and handles panics. A cleanup service creates, claims, executes, times out, cancels, and marks cleanup tasks. A subscription maintenance queue uses a bounded channel and fixed worker count. A scheduler snapshot service polls an outbox, batches events, rebuilds state with a timeout, and handles lag and backlog conditions. A scheduler outbox repository is an explicit event source separate from the snapshot state.

## Entity / fields

Async subsystems include usage worker pool, cleanup task, subscription maintenance queue, scheduler outbox, scheduler snapshot, and metrics.

## Request chain

Hot path enqueues bounded async work. Queue overflow follows configured drop/sample/sync policy. Cleanup and snapshot workers claim bounded tasks with timeout and cancellation.

## State machine

`pending -> queued -> running -> succeeded | failed | canceled | stale_reclaimed`; queues: `accepted -> overflow_sync | overflow_drop`.

## Failure modes

- Infinite goroutines during outage.
- Usage evidence lost without drop metrics.
- Cleanup task stuck running forever.
- Scheduler cache stale because outbox lag/backlog ignored.

## Sub2API capability

Sub2API has bounded worker pool, overflow policy, stats, cleanup task lifecycle, outbox snapshot and lag/backlog rebuild triggers.

## HUAKAI current capability

HUAKAI docs mention pool slots and workers, but not a unified bounded async policy in the reviewed account-to-API plan.

## HUAKAI gap

`MISSED_BY_HUAKAI`: no core account-to-API side effect should start unbounded goroutines.

## HUAKAI stronger design

Define platform-wide `AsyncJobPolicy`: queue size, worker count, timeout, overflow action, idempotency key, retry/dead-letter and metrics.

## Suggested Feature ID / level

- `F-ASYNC-BOUND-001`: L1
- `F-USAGE-WRITE-001`: L1
- `F-SCHED-OUTBOX-001`: L2
- `F-CLEANUP-TASK-001`: L2

## Acceptance tests

- Usage queue full follows fallback and increments metric.
- Cleanup task can be canceled and reclaimed.
- Scheduler outbox lag triggers full rebuild.

## Open questions

- open-question: whether to use DB outbox for all account state changes from day one.

---
Source files read: sub2api backend/internal/service/usage_record_worker_pool, backend/internal/service/usage_cleanup_service, backend/internal/service/subscription_maintenance_queue, backend/internal/service/scheduler_snapshot_service, backend/internal/service/scheduler_outbox
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
