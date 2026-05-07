# 16 Security / body / log guards

## Sub2API behavior summary

Sub2API applies multiple layers of body and log guards. Upstream response size caps and upstream error log caps are configurable with defaults. Non-streaming upstream response reads are bounded by a size limit. Webhook body is capped and logged body is truncated. Ops request and error bodies are capped, sanitized, and redacted before storage or display. Tests confirm that access tokens, refresh tokens, session tokens, and API keys are redacted from ops logs. A response header filter compiled from configuration exists. Request body limit handling extracts the max-bytes error type for safe error responses.

## Entity / fields

Guards include request body size caps, webhook caps, upstream response caps, upstream error truncation, JSON redaction rules, response header filtering, and safe error display.

## Request chain

Inbound body bounded -> stored/logged body sanitized -> upstream response/error body bounded -> response headers filtered -> ops/debug views get redacted data.

## State machine

`raw_body -> limit_checked -> parsed_if_json -> secret_redacted -> size_trimmed -> stored/displayed`.

## Failure modes

- Credential/request body leaks into ops logs.
- Upstream huge response exhausts memory.
- Webhook failure logs full signed payload.
- open-question: decompression bomb guard not confirmed in this pass.

## Sub2API capability

Sub2API has multiple body/log guards and redaction tests, but decompression bomb behavior remains open-question.

## HUAKAI current capability

Earlier backlog mentioned request body and safe log guards, but account-to-API does not yet force them as P0.

## HUAKAI gap

`MISSED_BY_HUAKAI`: `F-REQ-BODY-001` and `F-LOG-SAFE-001` must run in parallel with credential injection.

## HUAKAI stronger design

Add `SafePayload` library: raw size cap, decompression cap, JSON redaction, upstream error truncation, response header allow/deny and adapter redaction tests.

## Suggested Feature ID / level

- `F-REQ-BODY-001`: L1
- `F-LOG-SAFE-001`: L1
- `F-UPSTREAM-BODY-CAP-001`: L1
- `F-DECOMP-BOMB-001`: L1

## Acceptance tests

- Oversized body returns safe error.
- Gzip body exceeding decompressed cap is rejected.
- Ops log redacts access/refresh/session/API tokens.

## Open questions

- open-question: sub2api decompression bomb guard not source-confirmed.

---
Source files read: sub2api backend/internal/config/config, backend/internal/service/upstream_response_limit, backend/internal/handler/payment_webhook_handler, backend/internal/service/ops_service, backend/internal/service/ops_service_redaction_test, backend/internal/service/response_header_filter, backend/internal/handler/request_body_limit
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
