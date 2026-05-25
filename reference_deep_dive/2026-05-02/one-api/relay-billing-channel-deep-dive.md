# one-api relay, billing, and channel operations deep dive

Date: 2026-05-02
Reference repo: `.omc/reference-src/one-api`
Snapshot: `main`, commit `8df4a2670b98`, tag/describe `8df4a26`
Status: clean
Tracked files: 564

## Scope

This pass checks one-api's compact production behaviors: relay middleware, channel routing/retry, quota pre-consumption, post-settlement, redemption codes, batch updates, and auto-disable/enable workflows.

Read files:

- `router/relay.go`
- `middleware/gzip.go`
- `middleware/auth.go`
- `middleware/distributor.go`
- `middleware/recover.go`
- `controller/relay.go`
- `relay/billing/billing.go`
- `model/ability.go`
- `model/cache.go`
- `model/token.go`
- `model/user.go`
- `model/redemption.go`
- `model/utils.go`
- `monitor/manage.go`
- `monitor/channel.go`
- `controller/channel-test.go`
- `controller/channel-billing.go`

## Relay path and request handling

Source-confirmed behavior:

- The relay router installs a gzip decoding middleware before relay auth/distribution. Evidence: `.omc/reference-src/one-api/router/relay.go:12`, `.omc/reference-src/one-api/middleware/gzip.go:10`.
- Gzip decoding replaces `c.Request.Body` with the decompressed stream, but the middleware does not enforce a decompressed-size limit. Evidence: `.omc/reference-src/one-api/middleware/gzip.go:12`, `.omc/reference-src/one-api/middleware/gzip.go:20`.
- Token auth strips `Bearer ` and `sk-`, supports token subnet restriction, checks user enabled/banned state, extracts request model for model-gated endpoints, enforces token model whitelist, and supports admin-only specific-channel routing. Evidence: `.omc/reference-src/one-api/middleware/auth.go:91`, `.omc/reference-src/one-api/middleware/auth.go:104`, `.omc/reference-src/one-api/middleware/auth.go:110`, `.omc/reference-src/one-api/middleware/auth.go:119`, `.omc/reference-src/one-api/middleware/auth.go:125`, `.omc/reference-src/one-api/middleware/auth.go:135`.
- Channel distribution selects a channel from group/model ability, or validates an explicitly requested channel, then mutates request context and `Authorization` header for the upstream. Evidence: `.omc/reference-src/one-api/middleware/distributor.go:20`, `.omc/reference-src/one-api/middleware/distributor.go:28`, `.omc/reference-src/one-api/middleware/distributor.go:47`, `.omc/reference-src/one-api/middleware/distributor.go:64`, `.omc/reference-src/one-api/middleware/distributor.go:73`.
- Retry is attempted for 429, 5xx, and most non-2xx/non-400 responses unless a specific channel was requested. It selects another channel, resets the original request body, and tries again. Evidence: `.omc/reference-src/one-api/controller/relay.go:65`, `.omc/reference-src/one-api/controller/relay.go:70`, `.omc/reference-src/one-api/controller/relay.go:80`, `.omc/reference-src/one-api/controller/relay.go:105`.
- Relay errors can auto-disable channels or emit metrics depending on error type/status. Evidence: `.omc/reference-src/one-api/controller/relay.go:124`, `.omc/reference-src/one-api/monitor/manage.go:11`.
- Panic recovery logs panic, stacktrace, request method/path, and request body, then returns a synthetic error. Evidence: `.omc/reference-src/one-api/middleware/recover.go:12`, `.omc/reference-src/one-api/middleware/recover.go:17`, `.omc/reference-src/one-api/middleware/recover.go:20`.

HUAKAI delta:

- `F-REQ-BODY-001` should explicitly include content-encoding handling with max decompressed bytes, max ratio, streaming cap, and rejected encodings. one-api confirms gzip support is useful, but also shows the decompression-bomb hole if implemented without guardrails.
- `F-UPSTREAM-RETRY-001` should include body rewind rules, retryable status matrix, specific-channel no-retry rule, and failed-channel exclusion.
- `F-LOG-SAFE-001` should forbid logging raw request bodies in panic/recover paths; log request ID, route, model, token ID hash, channel ID, and truncated/scrubbed error only.
- Recommended level: request body guard and safe panic logging are L1. Retry matrix and channel exclusion are L2.

## Channel ability and selection

Source-confirmed behavior:

- one-api models channel capability as `(group, model, channel_id)` with enabled flag and priority. Evidence: `.omc/reference-src/one-api/model/ability.go:14`.
- DB channel selection chooses max priority unless retry asks to ignore first priority, then randomizes among candidates. Evidence: `.omc/reference-src/one-api/model/ability.go:22`, `.omc/reference-src/one-api/model/ability.go:33`, `.omc/reference-src/one-api/model/ability.go:36`, `.omc/reference-src/one-api/model/ability.go:39`.
- Memory-cache channel selection uses `group2model2channels`, preserves priority buckets, randomizes inside a bucket, and can skip the top priority bucket on retry. Evidence: `.omc/reference-src/one-api/model/cache.go:227`, `.omc/reference-src/one-api/model/cache.go:237`, `.omc/reference-src/one-api/model/cache.go:249`.
- Updating a channel rebuilds ability rows by deleting and recreating all channel abilities. Evidence: `.omc/reference-src/one-api/model/ability.go:53`, `.omc/reference-src/one-api/model/ability.go:73`, `.omc/reference-src/one-api/model/ability.go:77`.

HUAKAI delta:

- HUAKAI's provider/channel plan should separate "capability index" from "account health". one-api's ability table is small and operationally useful, but it does not carry deep health signals.
- Suggested feature IDs:
  - `F-ROUTE-CAP-001`: provider/account capability index by group and model.
  - `F-ROUTE-PRIORITY-001`: priority bucket routing with retry skipping previous top bucket.
  - `F-ROUTE-CACHE-001`: route cache invalidation rules on channel update/delete/status change.
- Recommended level: L1 for capability index; L2 for cache invalidation and retry-aware priority buckets.

## Quota pre-consume, settlement, and redemption

Source-confirmed behavior:

- Pre-consumption checks token remaining quota, user quota, low/no quota warning, token unlimited flag, and deducts token/user quota before the request. Evidence: `.omc/reference-src/one-api/model/token.go:217`, `.omc/reference-src/one-api/model/token.go:225`, `.omc/reference-src/one-api/model/token.go:228`, `.omc/reference-src/one-api/model/token.go:235`, `.omc/reference-src/one-api/model/token.go:272`.
- Post-consumption adjusts user and token quota up or down according to the delta after actual usage is known. Evidence: `.omc/reference-src/one-api/model/token.go:282`, `.omc/reference-src/one-api/model/token.go:287`, `.omc/reference-src/one-api/model/token.go:292`.
- Relay billing returns pre-consumed quota on early failure and records final consume logs, user used quota, request count, and channel used quota. Evidence: `.omc/reference-src/one-api/relay/billing/billing.go:11`, `.omc/reference-src/one-api/relay/billing/billing.go:23`, `.omc/reference-src/one-api/relay/billing/billing.go:34`, `.omc/reference-src/one-api/relay/billing/billing.go:46`.
- User quota cache is refreshed from DB when Redis is absent/missed or cached quota is below pre-consume threshold. Evidence: `.omc/reference-src/one-api/model/cache.go:76`, `.omc/reference-src/one-api/model/cache.go:88`, `.omc/reference-src/one-api/model/cache.go:100`.
- Batch updates aggregate quota, used quota, request count, and channel used quota into in-memory maps flushed on interval. Evidence: `.omc/reference-src/one-api/model/utils.go:10`, `.omc/reference-src/one-api/model/utils.go:29`, `.omc/reference-src/one-api/model/utils.go:38`, `.omc/reference-src/one-api/model/utils.go:48`.
- Redemption code redemption locks the row, verifies enabled status, increments user quota, marks redeemed time/status, and records top-up log. Evidence: `.omc/reference-src/one-api/model/redemption.go:54`, `.omc/reference-src/one-api/model/redemption.go:68`, `.omc/reference-src/one-api/model/redemption.go:73`, `.omc/reference-src/one-api/model/redemption.go:76`, `.omc/reference-src/one-api/model/redemption.go:80`.

HUAKAI delta:

- HUAKAI needs a formal "pre-consume and settle" contract rather than only "usage billing".
- Suggested feature IDs:
  - `F-BILL-PRE-001`: pre-consume reservation with token quota, user balance, unlimited-token behavior, and low-balance warning.
  - `F-BILL-SETTLE-001`: post-settlement delta, refund of unused reservation, consume log, user/channel used counters.
  - `F-BILL-BATCH-001`: batched counter updates or durable queue for high-throughput usage writes.
  - `F-REDEEM-001`: single-use recharge codes with row locking and audit log.
- Recommended level: L1 for pre-consume/settle correctness; L2 for batch update and redemption.

## Auto-disable and operator feedback

Source-confirmed behavior:

- Automatic disablement is gated by configuration, then checks unauthorized, known OpenAI error types, invalid key/deactivated codes, and message substrings such as low credit/balance/permission. Evidence: `.omc/reference-src/one-api/monitor/manage.go:11`, `.omc/reference-src/one-api/monitor/manage.go:18`, `.omc/reference-src/one-api/monitor/manage.go:21`, `.omc/reference-src/one-api/monitor/manage.go:25`, `.omc/reference-src/one-api/monitor/manage.go:29`.
- Disabling/enabling channels updates channel status, logs system events, and notifies root user by pusher or email. Evidence: `.omc/reference-src/one-api/monitor/channel.go:30`, `.omc/reference-src/one-api/monitor/channel.go:47`, `.omc/reference-src/one-api/monitor/channel.go:63`.
- Bulk channel tests use a single-running lock, response-time threshold, automatic disable, optional notification, auto-enable, response-time update, and request interval. Evidence: `.omc/reference-src/one-api/controller/channel-test.go:223`, `.omc/reference-src/one-api/controller/channel-test.go:234`, `.omc/reference-src/one-api/controller/channel-test.go:246`, `.omc/reference-src/one-api/controller/channel-test.go:254`, `.omc/reference-src/one-api/controller/channel-test.go:257`, `.omc/reference-src/one-api/controller/channel-test.go:260`.
- Channel balance checks disable enabled OpenAI/custom channels when balance is nil/zero. Evidence: `.omc/reference-src/one-api/controller/channel-billing.go:409`, `.omc/reference-src/one-api/controller/channel-billing.go:414`, `.omc/reference-src/one-api/controller/channel-billing.go:422`, `.omc/reference-src/one-api/controller/channel-billing.go:426`.

HUAKAI delta:

- `F-ACC-HEALTH-001` should include auto-disable reason taxonomy: auth failure, balance exhausted, timeout/degraded, explicit admin disable, and monitor failure.
- Suggested feature IDs:
  - `F-ACC-AUTODISABLE-001`: auto-disable rules with reason code, notification, and manual/admin override.
  - `F-ACC-AUTOENABLE-001`: guarded auto-enable on clean test, with audit.
  - `F-ACC-BALANCE-001`: provider balance probe and account temporary offline behavior.
- Recommended level: L2. Keep message-substring matching as a fallback only; prefer provider-normalized error classes.

## Clean-room notes

- Do not copy one-api's exact error substring list or billing math. Use it as evidence that production systems need a normalized error taxonomy and reservation/settlement contract.
- The gzip middleware is a useful negative example: HUAKAI should implement gzip support only with decompression-bomb protection.
- The recover middleware is a useful negative example: request bodies must be scrubbed or omitted in crash logs.

## Open questions

- Need a deeper read of one-api's model-pricing code if HUAKAI wants ratio-compatible billing with existing one-api deployments.
- Need to inspect frontend workflows for token self-service and admin channel testing UX.
- Need to compare one-api's batch update loss model against HUAKAI's durability requirements. In-memory aggregation may be acceptable for counters, but not for money-grade ledger operations.
