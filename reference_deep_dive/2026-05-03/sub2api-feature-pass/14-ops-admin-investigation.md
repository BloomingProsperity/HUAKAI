# 14 Ops / admin investigation

## Sub2API behavior summary

Sub2API's ops layer links request, user, API key, account, group, and upstream errors in a detail record. The ops service records errors, batches logs, lists request details, and supports resolution updates. A request detail list view exists. An ops retry service can replay upstream events, persist new attempt records, and resolve errors. A repository layer inserts, updates, reads, and links retry attempts to resolutions. An ops concurrency view exposes account, group, platform, and user concurrency and waiting counts. Admin bulk-update, clear-account-error, and set-schedulable actions exist as separate operations.

## Entity / fields

Ops records request ID, client request ID, user, API key, group, account, upstream status, upstream error detail, retry attempts, used account reference, and resolution status.

## Request chain

Gateway failure -> sanitized error log -> admin views detail -> optional retry/pinned retry -> retry attempt saved -> error resolved or remains open.

## State machine

`error_recorded -> triaged -> retry_requested -> retry_running -> retry_succeeded/resolved | retry_failed -> manual_action`.

## Failure modes

- Admin cannot answer why this request failed.
- Retry without pinned account can reproduce different behavior.
- Error/request body leaks secrets.

## Sub2API capability

Sub2API has ops error logs, request detail, upstream events, retry attempts, concurrency views and admin actions.

## HUAKAI current capability

HUAKAI audit says admin lacks trace request -> key -> binding -> account -> credential -> adapter -> upstream -> usage.

## HUAKAI gap

`MISSED_BY_HUAKAI`: admin trace must be designed with the account-to-API spine, not added after incidents.

## HUAKAI stronger design

Create `RequestTrace`: request -> API key -> binding -> route plan -> attempts -> credential lease -> adapter/injector -> upstream response/error -> usage -> state events.

## Suggested Feature ID / level

- `F-OPS-TRACE-001`: L1
- `F-OPS-RETRY-001`: L2
- `F-OPS-CONCURRENCY-001`: L2
- `F-ACCAPI-ADMIN-ACTION-001`: L2

## Acceptance tests

- Failed trace shows binding, account, credential version and classifier reason.
- Admin pinned retry stores retry attempt and used account.
- Secret fields are redacted in ops detail.

## Open questions

- open-question: retention duration for full trace vs summary trace.

---
Source files read: sub2api backend/internal/service/ops_models, backend/internal/service/ops_service, backend/internal/service/ops_request_details, backend/internal/service/ops_retry, backend/internal/repository/ops_repo, backend/internal/service/ops_concurrency, backend/internal/service/admin_service
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
