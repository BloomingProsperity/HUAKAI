# 13 Channel monitor / healthcheck

## Sub2API behavior summary

Sub2API stores monitor configuration including provider identity, endpoint, encrypted credential, model list, enabled flag, and check interval. A history table records each check result with status, latency, ping latency, message, and timestamp. A daily rollup table aggregates per-monitor and per-model availability and latency statistics. A runner starts schedules, fires tasks, prevents concurrent runs on the same monitor, and recovers from panics. The service runs checks, persists history, aggregates results, cleans old records, and rejects monitors whose credentials cannot be decrypted. An endpoint validator enforces scheme, path, and private-IP guard rules before scheduling. A dial-time SSRF and DNS-rebinding guard provides a second layer of protection at connection time.

## Entity / fields

Monitor includes configuration entity, encrypted credential, check history, daily rollup aggregates, and aggregation watermark.

## Request chain

Admin creates monitor -> endpoint/key validated -> runner schedules -> check primary/extra models -> persist history -> daily rollup -> cleanup.

## State machine

`configured -> scheduled -> in_flight -> operational/degraded/failed/error -> history_persisted -> rollup_aggregated -> pruned`.

## Failure modes

- SSRF through monitor endpoint.
- DNS rebinding after validation.
- Repeated monitor overlap causing false alarms.
- Bad encrypted key silently producing wrong checks.

## Sub2API capability

Sub2API has monitor runner, in-flight guard, panic recovery, SSRF guard, history, rollup and cleanup.

## HUAKAI current capability

HUAKAI has channel/provider health language but not yet this full monitor lifecycle in reviewed matrices.

## HUAKAI gap

`MISSED_BY_HUAKAI`: monitor result must feed account/channel state, not just render an admin panel.

## HUAKAI stronger design

Build monitor as a state producer: SSRF-safe checker, bounded runner, `monitor_events`, account/channel state update, false-positive dampening and rollups.

## Suggested Feature ID / level

- `F-CH-MON-001`: L2
- `F-CH-MON-SSRF-001`: L1
- `F-ACCAPI-MONITOR-STATE-001`: L2

## Acceptance tests

- Private/loopback endpoint is rejected at validation and dial time.
- Same monitor cannot run concurrently.
- Failed monitor updates account/channel state only after threshold.

## Open questions

- open-question: account monitor vs external channel monitor unification.

---
Source files read: sub2api backend/ent/schema/channel_monitor, backend/ent/schema/channel_monitor_history, backend/ent/schema/channel_monitor_daily_rollup, backend/internal/service/channel_monitor_runner, backend/internal/service/channel_monitor_service, backend/internal/service/channel_monitor_validate, backend/internal/service/channel_monitor_ssrf
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
