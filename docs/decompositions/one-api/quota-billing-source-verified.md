# one-api Quota + Billing Source Verification

| Field | Value |
| --- | --- |
| Status | Specifier-lane source verification |
| Author | Codex |
| Date | 2026-04-28 |
| Reference | one-api at commit `8df4a2670b98266bd287c698243fff327d9748cf` |
| Lane | Specifier-lane source verification |
| Scope | Re-verify `E-OAI-DEEP-001` through `E-OAI-DEEP-016` against local source under `.omc/reference-src/one-api/` |

Clean-room note: one-api is MIT in `.omc/reference-src/one-api/LICENSE`. This file records behavior evidence and source line citations only; it does not copy upstream implementation.

## 1. Verification Matrix

Verdict meanings used here:

- `STILL-VALID`: current source supports the ledger claim.
- `DRIFT`: current source supports only a narrower or materially different behavior than the ledger wording.
- `FALSE`: current source refutes the ledger wording.
- `UNCONFIRMABLE`: no supporting or refuting path was found.

| Evidence ID | Ledger Claim Checked | Verdict | Current Source Evidence | Correction / Notes |
| --- | --- | --- | --- | --- |
| `E-OAI-DEEP-001` | Request routes to a Channel from the User's Pool supporting the model, preferring Channels not recently failed. | `FALSE` | `.omc/reference-src/one-api/middleware/distributor.go:23-59`; `.omc/reference-src/one-api/model/ability.go:22-50`; `.omc/reference-src/one-api/model/cache.go:227-254`; `.omc/reference-src/one-api/monitor/metric.go:68-78` | Current routing uses user group + requested model eligibility, then priority bucket/random selection. I found no source path where recent failure metrics feed selection preference. Metrics may later disable a Channel, but selection itself does not prefer "not recently failed". |
| `E-OAI-DEEP-002` | Retry triggers on rate-limit and 5xx; retry skips forced Channel and client misconfiguration. | `STILL-VALID` | `.omc/reference-src/one-api/controller/relay.go:65-71`; `.omc/reference-src/one-api/controller/relay.go:105-121`; `.omc/reference-src/one-api/middleware/auth.go:135-146`; `.omc/reference-src/one-api/middleware/distributor.go:28-43` | Retry is disabled when a specific Channel is set. `429` and `5xx` retry; `400` and `2xx` do not; other non-2xx/non-400 statuses retry. |
| `E-OAI-DEEP-003` | On retry, the failed Channel is excluded and same payload is resent to a different Channel from the same Pool. | `DRIFT` | `.omc/reference-src/one-api/controller/relay.go:70-83`; `.omc/reference-src/one-api/controller/relay.go:87-90`; `.omc/reference-src/one-api/model/cache.go:227-254` | Current code skips only the immediately last failed Channel ID. It does not maintain an exclusion set of all Channels failed within the request, so a later retry can revisit an earlier failed Channel. Payload rewind is supported. |
| `E-OAI-DEEP-004` | No explicit quota reservation occurs before upstream call. | `FALSE` | `.omc/reference-src/one-api/relay/controller/text.go:46-52`; `.omc/reference-src/one-api/relay/controller/helper.go:68-94`; `.omc/reference-src/one-api/relay/controller/audio.go:69-92`; `.omc/reference-src/one-api/relay/controller/image.go:174-203` | Current text and audio paths do pre-call pre-consumption/reservation. Image generation does not reserve; it cache-checks before call and charges after successful response. The ledger row must be split by endpoint family. |
| `E-OAI-DEEP-005` | Duplicate-billing prevention across retry attempts is implicit; no visible cross-attempt deduplication. | `STILL-VALID` | `.omc/reference-src/one-api/controller/relay.go:70-83`; `.omc/reference-src/one-api/relay/controller/text.go:73-86`; `.omc/reference-src/one-api/relay/billing/billing.go:11-20`; `rg "Idempot|fingerprint|claim|dedup" .omc/reference-src/one-api` found no request-claim gate in quota/billing code | Each attempt runs its own pre-consume / refund / settle path. There is refund-based mitigation, but no durable per-request fingerprint or idempotent billing claim spanning retries. |
| `E-OAI-DEEP-006` | Channel may auto-disable when the failure pattern indicates permanent unavailability rather than transient. | `STILL-VALID` | `.omc/reference-src/one-api/controller/relay.go:124-130`; `.omc/reference-src/one-api/monitor/manage.go:11-44`; `.omc/reference-src/one-api/monitor/channel.go:30-60`; `.omc/reference-src/one-api/controller/channel-test.go:246-258` | Permanent-ish auth/quota/permission/balance signals and slow health tests can auto-disable Channels when enabled by config. |
| `E-OAI-DEEP-007` | API Key quota check is two-stage: estimate/pre-deduction then post-deduction adjusts to actual. | `STILL-VALID` | `.omc/reference-src/one-api/relay/controller/helper.go:60-94`; `.omc/reference-src/one-api/relay/controller/helper.go:97-140`; `.omc/reference-src/one-api/model/token.go:217-300` | Valid for text and audio token/account quota paths. Image path does not use the same pre-deduction stage. |
| `E-OAI-DEEP-008` | Validation check and DB deduction are not atomic; concurrent requests can pass check then both deduct. | `STILL-VALID` | `.omc/reference-src/one-api/model/token.go:217-233`; `.omc/reference-src/one-api/model/token.go:272-279`; `.omc/reference-src/one-api/model/user.go:390-403`; `.omc/reference-src/one-api/model/utils.go:38-75` | The source checks token/user quota, then performs separate quota mutations. Updates use arithmetic expressions, but the check + mutation is not one compare-and-swap or locked transaction. Batch mode can defer writes further. |
| `E-OAI-DEEP-009` | API Key state is cached in memory; freshness depends on invalidation. | `DRIFT` | `.omc/reference-src/one-api/model/cache.go:20-55`; `.omc/reference-src/one-api/common/redis.go:63-80`; `.omc/reference-src/one-api/controller/token.go:170-184`; `.omc/reference-src/one-api/controller/token.go:188-255`; `.omc/reference-src/one-api/model/token.go:132-147` | Token cache is Redis-backed when Redis is enabled, not process memory. The stale-cache risk still exists: update/delete paths do not visibly delete `token:<key>`; freshness depends on TTL or Redis being disabled. |
| `E-OAI-DEEP-010` | Channel selection resolves by User Group and Model; highest-priority bucket wins; random within equal priority; retry can skip first priority bucket. | `STILL-VALID` | `.omc/reference-src/one-api/middleware/distributor.go:23-59`; `.omc/reference-src/one-api/model/ability.go:22-50`; `.omc/reference-src/one-api/model/cache.go:203-254`; `.omc/reference-src/one-api/controller/relay.go:70-83` | Valid. DB and memory-cache paths differ internally but share group/model eligibility, priority preference, and random choice. Retry passes an `ignoreFirstPriority` equivalent flag after the first retry selection. |
| `E-OAI-DEEP-011` | Caller may force a specific Channel; forced path bypasses normal selection and most retry/fallback; disabled Channels rejected. | `STILL-VALID` | `.omc/reference-src/one-api/middleware/auth.go:135-146`; `.omc/reference-src/one-api/middleware/distributor.go:28-43`; `.omc/reference-src/one-api/controller/relay.go:105-107` | Admin/specific-channel path bypasses normal random selection, rejects disabled Channels, and disables retry. |
| `E-OAI-DEEP-012` | Retry triggers on rate limit, server errors, and most non-success / non-client-validation responses; body rewound; no meaningful exponential backoff. | `STILL-VALID` | `.omc/reference-src/one-api/controller/relay.go:65-90`; `.omc/reference-src/one-api/controller/relay.go:105-121` | Valid. The loop immediately reselects and rewinds the request body; no sleep/backoff is visible in the retry loop. |
| `E-OAI-DEEP-013` | Duplicate billing prevention is reservation/refund based; estimated quota preconsumed, failed attempts return it, success reconciles; no strong fingerprint claim gate. | `STILL-VALID` | `.omc/reference-src/one-api/relay/controller/text.go:49-86`; `.omc/reference-src/one-api/relay/billing/billing.go:11-20`; `.omc/reference-src/one-api/relay/controller/helper.go:116-140`; `.omc/reference-src/one-api/model/log.go:80-91`; `rg "Idempot|fingerprint|claim|dedup" .omc/reference-src/one-api` | Valid for text path. Refund and post-settle run outside a durable claim gate; crash or ambiguous streaming can still split quota, usage record, and retry accounting. |
| `E-OAI-DEEP-014` | Streaming forwards event lines, emits terminal marker if missing, uses upstream usage chunks when present, infers completion usage from accumulated text; cancellation propagates via request context. | `DRIFT` | `.omc/reference-src/one-api/relay/adaptor/openai/main.go:27-97`; `.omc/reference-src/one-api/relay/adaptor/openai/adaptor.go:109-119`; `.omc/reference-src/one-api/relay/adaptor/openai/helper.go:11-17`; `.omc/reference-src/one-api/relay/adaptor/common.go:21-51`; `.omc/reference-src/one-api/relay/adaptor/replicate/chat.go:118-127` | Forwarding, terminal marker, usage chunks, and token-count fallback are supported for OpenAI-style streams. Cancellation propagation is not uniform: generic OpenAI requests are built without `c.Request.Context()`, while some adaptors use request context directly. |
| `E-OAI-DEEP-015` | Quota reservation formula uses prompt + reserve + requested max-output times model/group ratio; cache decremented first; DB writes may be batched; success adjusts to actual. | `STILL-VALID` | `.omc/reference-src/one-api/relay/controller/helper.go:60-94`; `.omc/reference-src/one-api/relay/controller/helper.go:97-140`; `.omc/reference-src/one-api/model/cache.go:88-123`; `.omc/reference-src/one-api/model/user.go:374-403`; `.omc/reference-src/one-api/model/token.go:173-300`; `.omc/reference-src/one-api/model/utils.go:29-75`; `.omc/reference-src/one-api/main.go:86-89` | Valid with caveat: text path can skip pre-consumption for very high user quota (`userQuota > 100 * estimate`). That skip is an additional exposure window. |
| `E-OAI-DEEP-016` | Channel tests run manually or on schedule with global non-overlap guard; auth/quota/balance/permission failures and slow responses can disable; successful tests may auto-re-enable. | `STILL-VALID` | `.omc/reference-src/one-api/controller/channel-test.go:216-304`; `.omc/reference-src/one-api/monitor/manage.go:11-56`; `.omc/reference-src/one-api/main.go:79-85`; `.omc/reference-src/one-api/controller/channel-billing.go:409-431` | Valid. Manual route and scheduled loop both call the test path; a process-local lock prevents overlapping all-channel tests. Auto-enable/disable depends on config. |

Matrix conclusion:

- Rows still valid without material correction: `E-OAI-DEEP-002`, `006`, `007`, `008`, `010`, `011`, `012`, `013`, `015`, `016`.
- Rows requiring ledger correction: `E-OAI-DEEP-001`, `003`, `004`, `009`, `014`.
- The most important correction is `E-OAI-DEEP-004`: current source does have pre-call reservation for text/audio, but not an atomic money-grade claim gate, and image remains weaker.

### 1.1 CL-011 Source-Verification Notes

This pass used direct local source reads, not prior prose decomposition, for every reference behavior claim above.

Verification commands / searches used:

- Commit pin: `git -C .omc/reference-src/one-api log -1 --format=%H`.
- Ledger extraction: `rg -n "E-OAI-DEEP-|E-OAI-" docs/07_REFERENCE_EVIDENCE_LEDGER.md`.
- Idempotency search: `rg "Idempot|fingerprint|claim|dedup" .omc/reference-src/one-api`.
- Retry/source routing search: `rg -n "SpecificChannelId|SetupContextForSelectedChannel|CacheGetRandomSatisfiedChannel|shouldRetry" .omc/reference-src/one-api`.
- Quota/billing search: `rg -n "PreConsumeTokenQuota|PostConsumeTokenQuota|CacheDecreaseUserQuota|ReturnPreConsumedQuota|quotaDelta" .omc/reference-src/one-api`.

Interpretation guardrails:

- I treated "Pool" in old `E-OAI-DEEP-*` rows as closest to one-api's user group / Channel eligibility model, because current source has no stronger Pool object in the checked path.
- I did not infer absence from memory alone. For negative findings, I paired source line reads with repository searches for the claimed mechanism.
- I avoided turning HUAKAI improvements into source claims. All improvement-only recommendations below are explicitly labeled as HUAKAI design, not one-api behavior.
- I did not update the ledger in this task, per Owner instruction. Ledger corrections are listed here for PM follow-up.

Source path correction:

- Claude v2 cross-reference mentioned `relay/channeltype/select.go`; that path is absent in the current one-api clone.
- Current routing evidence lives in `middleware/distributor.go`, `model/ability.go`, `model/cache.go`, and `controller/relay.go`.
- This matters because a CL-011 reviewer cannot verify a citation to a nonexistent file.

## 2. Newly Discovered Gaps Not Already Cleanly Captured

### 2.1 Endpoint-family charging behavior is inconsistent

Text:

- Pre-call estimate is computed and validated before the upstream call: `.omc/reference-src/one-api/relay/controller/helper.go:60-94`.
- Upstream error or response conversion error returns the pre-consumed quota: `.omc/reference-src/one-api/relay/controller/text.go:73-83`.
- Success reconciles actual usage asynchronously after response handling: `.omc/reference-src/one-api/relay/controller/text.go:85-86`; `.omc/reference-src/one-api/relay/controller/helper.go:116-140`.

Image:

- User quota is checked from cache before upstream call, but no token/account pre-consumption is performed before the upstream call: `.omc/reference-src/one-api/relay/controller/image.go:171-187`.
- Charge happens only after the upstream response status is success-like: `.omc/reference-src/one-api/relay/controller/image.go:196-228`.

Audio:

- Speech uses input length as pre-consumed quota; transcription/translation use configured reserve: `.omc/reference-src/one-api/relay/controller/audio.go:60-68`.
- Audio pre-consumes before call and rolls back on failure: `.omc/reference-src/one-api/relay/controller/audio.go:69-109`.
- Success settles asynchronously: `.omc/reference-src/one-api/relay/controller/audio.go:216-220`.

HUAKAI implication: one unified Quota + Billing contract must not inherit endpoint-specific timing differences unless they are explicit product policy. Text, image, and audio should all pass through the same reservation / settle / refund state machine.

### 2.2 Cache-first admission can diverge from DB truth

Evidence:

- Quota read path can return Redis value directly: `.omc/reference-src/one-api/model/cache.go:88-104`.
- Cache is decremented before DB pre-consumption: `.omc/reference-src/one-api/relay/controller/helper.go:71-89`.
- Redis decrement is a standalone operation: `.omc/reference-src/one-api/model/cache.go:119-124`.

Gap:

- Redis cache is used as an admission input, then DB mutation follows separately. A Redis/DB split-brain, stale cache, or process crash can create observable quota disagreement.

### 2.3 High-balance users can bypass pre-consumption

Evidence:

- Text path sets pre-consumed quota to zero when cached user quota is more than 100x the estimate: `.omc/reference-src/one-api/relay/controller/helper.go:82-89`.
- Audio path has the same skip condition: `.omc/reference-src/one-api/relay/controller/audio.go:82-88`.

Gap:

- The largest-balance users are exactly the ones allowed to run without pre-call DB reservation in these cases. Under high concurrency, exposure is bounded only by later settlement and account quota.

### 2.4 Refund and settlement are asynchronous best-effort operations

Evidence:

- Failed text attempts refund pre-consumed quota in a goroutine: `.omc/reference-src/one-api/relay/billing/billing.go:11-20`.
- Audio failure rollback also uses nested asynchronous work: `.omc/reference-src/one-api/relay/controller/audio.go:93-109`.
- Text success post-consumption is launched asynchronously: `.omc/reference-src/one-api/relay/controller/text.go:85-86`.
- Audio success post-consumption is launched asynchronously: `.omc/reference-src/one-api/relay/controller/audio.go:216-220`.

Gap:

- A process crash, context cancellation, DB outage, or retry interleaving can leave reservation, refund, usage log, and channel usage counters out of sync.

### 2.5 Retry exclusion is last-failed only, not all-failed

Evidence:

- Retry loop stores only one last failed Channel ID and skips only that ID: `.omc/reference-src/one-api/controller/relay.go:59-90`.

Gap:

- With three or more retry attempts, selection can revisit a Channel that failed earlier in the same request. This weakens retry isolation and can amplify an incident.

### 2.6 Token cache invalidation is not visible on update/delete

Evidence:

- Token cache is populated under `token:<key>` in Redis: `.omc/reference-src/one-api/model/cache.go:28-55`.
- Token update and delete call DB update/delete paths: `.omc/reference-src/one-api/controller/token.go:170-184`; `.omc/reference-src/one-api/controller/token.go:188-255`; `.omc/reference-src/one-api/model/token.go:132-147`.
- `RedisDel` exists but no token update/delete path found calling it for `token:<key>`: `.omc/reference-src/one-api/common/redis.go:73-75`.

Gap:

- Disabled, deleted, expired, quota-changed, model-restricted, or subnet-restricted keys may remain effective until cache expiry in Redis-enabled deployments.

### 2.7 Generic OpenAI upstream request lacks request-context cancellation

Evidence:

- Generic adaptor creates upstream request without binding `c.Request.Context()`: `.omc/reference-src/one-api/relay/adaptor/common.go:21-51`.
- Other adaptor paths do use request context directly, for example Replicate stream: `.omc/reference-src/one-api/relay/adaptor/replicate/chat.go:118-127`.

Gap:

- Cancellation behavior differs by upstream family. A client disconnect may not cancel generic OpenAI-compatible upstream work, while some provider-specific paths do cancel.

### 2.8 No durable request-level billing idempotency primitive

Evidence:

- Billing mutation paths are token/user quota mutation functions plus usage logs: `.omc/reference-src/one-api/model/token.go:217-300`; `.omc/reference-src/one-api/relay/controller/helper.go:116-140`; `.omc/reference-src/one-api/model/log.go:80-91`.
- Source search did not find a quota/billing request fingerprint, claim row, or dedup gate: `rg "Idempot|fingerprint|claim|dedup" .omc/reference-src/one-api`.

Gap:

- Retries, slow-success ambiguity, duplicate client submissions, and crash recovery cannot be reconciled against one durable request claim.

## 3. KEEP / IMPROVE / AVOID for HUAKAI Quota + Billing Design

### KEEP

These are verified one-api behaviors HUAKAI can inherit as product-level behavior, redesigned in HUAKAI terms:

- Group/model/channel eligibility as a simple baseline routing mode: `.omc/reference-src/one-api/middleware/distributor.go:23-59`; `.omc/reference-src/one-api/model/ability.go:22-50`.
- Operator priority with randomized tie-break as a low-complexity fallback strategy: `.omc/reference-src/one-api/model/ability.go:33-43`; `.omc/reference-src/one-api/model/cache.go:238-254`.
- Specific Channel override for privileged operators, with disabled Channel rejection and retry disabled: `.omc/reference-src/one-api/middleware/auth.go:135-146`; `.omc/reference-src/one-api/middleware/distributor.go:28-43`; `.omc/reference-src/one-api/controller/relay.go:105-107`.
- Estimate-then-reconcile charging concept for text/audio workloads: `.omc/reference-src/one-api/relay/controller/helper.go:60-140`; `.omc/reference-src/one-api/relay/controller/audio.go:60-220`.
- Usage-based streaming fallback when upstream omits usage: `.omc/reference-src/one-api/relay/adaptor/openai/main.go:27-97`; `.omc/reference-src/one-api/relay/adaptor/openai/adaptor.go:109-119`; `.omc/reference-src/one-api/relay/adaptor/openai/helper.go:11-17`.
- Auto-disable and auto-enable as operator-configurable channel operations: `.omc/reference-src/one-api/monitor/manage.go:11-56`; `.omc/reference-src/one-api/controller/channel-test.go:246-258`.

### IMPROVE

These are HUAKAI design improvements, not one-api source behavior:

- `(HUAKAI design - not in source)` Use a durable idempotent Billing Claim for every logical request. Retries, stream settlement, refunds, and duplicate submissions must share the same claim.
- `(HUAKAI design - not in source)` Make quota reservation atomic at the DB authority layer using row lock / compare-and-swap semantics, not cache-first admission.
- `(HUAKAI design - not in source)` Treat cache as a read-through hint only. Cache may accelerate display and preflight UX, but cannot be the authority that allows spend.
- `(HUAKAI design - not in source)` Use one endpoint-neutral reservation lifecycle for text, image, audio, embeddings, and future media jobs.
- `(HUAKAI design - not in source)` Make refund and final settlement synchronous within Tx2 or a durable outbox that retries until exactly-once effect is reached.
- `(HUAKAI design - not in source)` Track retry attempt IDs under one request claim and store per-attempt upstream result, selected Provider Account, and refund state.
- `(HUAKAI design - not in source)` Use a full per-request failed-Provider exclusion set during retry, not last-failed-only skip.
- `(HUAKAI design - not in source)` Add explicit token/key cache invalidation events for update/delete/disable/quota/model/subnet changes.
- `(HUAKAI design - not in source)` Standardize cancellation policy per endpoint family: either detach for billing-preserving drain or propagate cancellation, but record which policy was applied.

### AVOID

These are verified one-api anti-patterns HUAKAI must not inherit:

- Cache-first quota admission as spend authority: `.omc/reference-src/one-api/model/cache.go:88-124`; `.omc/reference-src/one-api/relay/controller/helper.go:71-89`.
- Non-atomic check-then-deduct quota mutation: `.omc/reference-src/one-api/model/token.go:217-279`; `.omc/reference-src/one-api/model/user.go:390-403`.
- Batch-delayed money mutations for quota/accounting authority: `.omc/reference-src/one-api/model/utils.go:29-75`; `.omc/reference-src/one-api/main.go:86-89`.
- Endpoint-specific charging windows that are not visible as product policy: `.omc/reference-src/one-api/relay/controller/text.go:46-86`; `.omc/reference-src/one-api/relay/controller/image.go:171-228`; `.omc/reference-src/one-api/relay/controller/audio.go:60-220`.
- High-balance reservation bypass: `.omc/reference-src/one-api/relay/controller/helper.go:82-89`; `.omc/reference-src/one-api/relay/controller/audio.go:82-88`.
- Asynchronous best-effort refund/settlement without a durable outbox: `.omc/reference-src/one-api/relay/billing/billing.go:11-20`; `.omc/reference-src/one-api/relay/controller/text.go:85-86`; `.omc/reference-src/one-api/relay/controller/audio.go:93-109`; `.omc/reference-src/one-api/relay/controller/audio.go:216-220`.
- Last-failed-only retry exclusion: `.omc/reference-src/one-api/controller/relay.go:59-90`.
- Token cache without explicit invalidation on update/delete: `.omc/reference-src/one-api/model/cache.go:28-55`; `.omc/reference-src/one-api/controller/token.go:170-255`.
- Mixed cancellation semantics across provider families: `.omc/reference-src/one-api/relay/adaptor/common.go:21-51`; `.omc/reference-src/one-api/relay/adaptor/replicate/chat.go:118-127`.

## 4. Cross-Check Against Claude v2 Passes

Files found:

- `docs/decompositions/_cross-cutting/pool-selection-claude-v2.md`
- `docs/decompositions/_cross-cutting/streaming-forwarder-claude-v2.md`

The requested filenames were not present under `docs/decompositions/one-api/`; the cross-cutting directory contains the actual v2 files.

### 4.1 `pool-selection-claude-v2.md`

Required correction:

- The one-api cross-reference cites `relay/channeltype/select.go`, but that file does not exist in the current clone. The correct one-api selection citations are `.omc/reference-src/one-api/middleware/distributor.go:23-59`, `.omc/reference-src/one-api/model/ability.go:22-50`, `.omc/reference-src/one-api/model/cache.go:227-254`, and `.omc/reference-src/one-api/controller/relay.go:70-90`.

Required nuance:

- Its statement that one-api failover "re-pick[s] excluding the failed channel" should be narrowed. Current source excludes only the immediately last failed Channel ID, not all Channels failed during this logical request: `.omc/reference-src/one-api/controller/relay.go:59-90`.

Required quota correction:

- Its statement that `E-OAI-DEEP-004/005/008` still hold is too broad. `E-OAI-DEEP-005` and `E-OAI-DEEP-008` still hold. `E-OAI-DEEP-004` is false for text/audio in the current source because pre-call pre-consumption exists: `.omc/reference-src/one-api/relay/controller/helper.go:68-94`; `.omc/reference-src/one-api/relay/controller/audio.go:69-92`. The image path remains a no-reservation family and should be cited separately: `.omc/reference-src/one-api/relay/controller/image.go:171-228`.

No change to the high-level HUAKAI design direction:

- The pass's recommendation to couple Pool selection with Quota+Billing Tx1/Tx2 remains correct. This verification strengthens that point because one-api's refund/settlement is asynchronous and not claim-gated.

### 4.2 `streaming-forwarder-claude-v2.md`

Confirmed addition for TODO-4:

- one-api's OpenAI-style stream handler is simpler than Sub2API's forwarder. It scans upstream event lines, forwards lines, emits a terminal marker if upstream did not, captures usage chunks when present, and falls back to token-count usage from accumulated response text: `.omc/reference-src/one-api/relay/adaptor/openai/main.go:27-97`; `.omc/reference-src/one-api/relay/adaptor/openai/adaptor.go:109-119`; `.omc/reference-src/one-api/relay/adaptor/openai/helper.go:11-17`.

Required nuance:

- one-api cancellation behavior is not a clean "request context propagates" pattern. Generic OpenAI-compatible upstream requests are built without `c.Request.Context()`: `.omc/reference-src/one-api/relay/adaptor/common.go:21-51`. Some provider-specific paths do use request context, such as Replicate stream: `.omc/reference-src/one-api/relay/adaptor/replicate/chat.go:118-127`.

No change to the high-level HUAKAI design direction:

- Claude v2's HUAKAI improvements remain appropriate: bounded drain, usage source taxonomy, explicit ambiguous-usage policy, and atomic Tx2. one-api has none of those as durable source behavior.

## 5. Owner Summary

本次我重新核对了 `docs/07_REFERENCE_EVIDENCE_LEDGER.md` 中 `E-OAI-DEEP-001` 到 `E-OAI-DEEP-016` 的 one-api 深读证据，并固定当前参考源码 commit 为 `8df4a2670b98266bd287c698243fff327d9748cf`。结果是：大部分 Codex 后续补充的 010-016 仍然成立；Claude 早期的 001、003、004、009、014 需要修正，其中最关键的是 `E-OAI-DEEP-004`，当前 one-api text/audio 路径已经有 pre-call pre-consumption，不能再写成“完全没有预留”；真正风险是它不是原子 claim gate，image 路径仍然只是 cache check 后成功再扣费。

对 HUAKAI 设计没有功能缩水要求，反而更明确：必须保留 one-api 的可用行为，例如按 group/model/priority 选择、特权 Channel override、estimate-then-reconcile、stream usage fallback、Channel auto-disable/enable；同时必须改进为 DB 权威的原子 reservation、durable idempotent Billing Claim、endpoint-neutral charging lifecycle、同步或 durable-outbox settlement/refund、全请求失败账户排除集、显式 token cache invalidation。Clean-room 风险可控：本文只记录行为和本地 source line citation，没有复制实现代码；安全风险集中在不能继承 cache-first admission、异步 best-effort refund、token cache stale、mixed cancellation semantics。需要 Owner 确认的是是否由 PM 同步修正 ledger 中 001/003/004/009/014 这些已漂移或错误的行，以及是否要求 Claude v2 两个 cross-cutting pass 立即补丁更新 one-api 引用路径和 quota 结论。
