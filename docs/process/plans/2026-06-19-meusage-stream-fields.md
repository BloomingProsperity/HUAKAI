# Plan — self-service usage record: stream / stream_terminated_reason / requested_at

Date: 2026-06-19 · Author: Claude PM · Slice: disjoint-mining backlog #2 · Feature area: F-meusage (self-service usage transparency)

## Scope
Surface three already-persisted, already-selected fields on the customer self-service usage record
(`GET /v1/me/usage` and `GET /v1/generation`, `backend/internal/meusagehttp/handler.go`):
- `stream` (bool) — whether the request was a streaming request (request mode).
- `stream_terminated_reason` (string, omitempty) — why a stream ended (diagnostic; absent for normal completion).
- `requested_at` (string date-time, omitempty) — request arrival time, enabling client-side latency calc.

## Not-already-built (verified real code, 2026-06-19)
- `usageRecord` DTO (`handler.go:54-66`) has no stream/stream_terminated_reason/requested_at (grep confirmed).

## Value-in-hand (verified — zero db/schema/sqlc change)
- `dbbilling.ListUsageRecordsRow` (`db/billing/observability.sql.go`) already carries `Stream bool`,
  `StreamTerminatedReason *string`, `RequestedAt pgtype.Timestamptz`; the `ListUsageRecords` query already
  SELECTs (lines ~193-195) and scans (~308/313) all three. `GetUsageRecordByRequestIDRow` (the `/v1/generation`
  path) carries the same three. So both projection paths have the values in hand — pure handler-local change.

## Existing in-repo precedent (admin parity)
- `backend/internal/gatewayhttp/admin_observability_helpers.go:51-55` already projects the admin usage view with
  the exact wire names `stream`, `stream_terminated_reason`, `requested_at`. This slice brings the customer
  self-service view to the same projection (read-only; admin view is a parity reference, not edited).

## #16 triple-mirror (real source cites)
- new-api `model/log.go:37,46,47` — its user-facing log surfaces `created_at` + `use_time` + `is_stream`
  (strongest precedent: stream mode + timing in a self-service usage/log view).
- sub2api — usage models carry `created_at` timing but the model layer shows no per-record stream-mode/
  termination field at this granularity (no equivalent at our precision for `stream_terminated_reason`).
- CLIProxyAPI — pure relay with request statistics; no persisted per-record usage log carrying stream
  termination semantics (no equivalent).
- **HUAKAI delta (生态/ecosystem)**: self-service record exposes request mode + arrival time + the *reason* a
  stream terminated (finer diagnostic than is_stream alone), all already persisted and already on the admin
  view — closing the self-service↔admin parity gap from a single read store.

## Changes
1. `handler.go` — add 3 fields to `usageRecord` (`stream` always-present, `stream_terminated_reason,omitempty`,
   `requested_at,omitempty`); project in `mapUsageRecord` (row.Stream / valueString(row.StreamTerminatedReason)
   / formatTS(row.RequestedAt)); plumb the same three from `GetUsageRecordByRequestIDRow` through
   `mapGenerationUsageRecord` so the `/v1/generation` path is consistent.
2. `handler_test.go` — discriminating projection test: a row with Stream=true, StreamTerminatedReason set,
   RequestedAt set; assert all three surface with correct values (decode is `map[string]any`).
3. `docs/openapi/openapi.yaml` — add the 3 properties to `MeUsageRecord` (additionalProperties:false).
4. `cmd/gateway/openapi_consistency_test.go` — add a `MeUsageRecord` schema-sync guard reusing the existing
   `yamlSchemaBlock` helper so additionalProperties:false drift on these fields is caught.

## Success criteria
- build + vet clean; codebudget green; cmd/gateway gate green.
- Projection test passes; mutation (drop a projection assignment / drop a schema property) goes RED, verified
  with `-count=1` (the openapi-reading guard is runtime-file-read, so cache must be bypassed).
- No leak: stream/stream_terminated_reason/requested_at are request-shape metadata already on the admin view;
  the existing forbidden-field leak guards (cost/identity/account internals) remain intact.

## Blast radius
Single non-collision package (`meusagehttp`; not in proxies avoidance list; no active parallel branch) + OpenAPI
doc + one cmd/gateway guard test. Zero db/schema/money/auth/quota. Additive fields = backward-compatible.

## What could go wrong / mitigations
- `stream` always-present (no omitempty): correct for a mode flag (false = non-streaming is real info); matches
  the admin projection which always includes the key.
- `mapGenerationUsageRecord` constructs a partial row — must plumb the 3 source fields or the generation path
  would show defaults; both source row types carry them, so plumbed through.

## Owner decision points
None — additive self-service metadata already on the admin view, no gated risk class. Standard autonomous slice.
