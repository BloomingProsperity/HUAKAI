# new-api - Cache-aware billing buckets + reasoning-effort handling

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | New API, AGPL-3.0-or-later, E-LIC-002 |
| Feature in HUAKAI matrix | F-BILL-003 (L3) + F-MODEL-001 (L2) |
| Evidence ledger row | E-NAI-001 + E-NAI-004 |
| Specifier session | Codex specifier-lane Round 2, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | <region of source: public README feature claims for cache billing and reasoning controls> |
| Source files read | <region of source: usage data shapes and cross-format usage conversion> |
| Source files read | <region of source: text billing settlement and usage-log enrichment> |
| Source files read | <region of source: tiered billing expression documentation and settlement tests> |
| Source files read | <region of source: wallet/subscription pre-consume, refund, and delta settlement> |
| Source files read | <region of source: streaming scanner and terminal usage assembly> |
| Source files read | <region of source: provider-specific reasoning translation layers> |

## 1. WHY (motivation / context)

This feature exists because modern Provider billing is no longer a two-counter model.

The older mental model was request tokens plus completion tokens, multiplied by a Model price and a User Group price.

The source now shows a more complex reality.

The same logical request can include fresh input, cache-read input, cache-write input, split cache-write windows, image input, audio input, visible output, audio output, image output, tool calls, web-search calls, file-search calls, per-call prices, Provider-reported cost, and thinking-token behavior.

The source also shows that reasoning control is no longer one portable parameter.

OpenAI-compatible requests, Responses-style requests, Claude-style thinking, Gemini thinking budgets, DeepSeek-style thinking suffixes, xAI effort suffixes, and OpenRouter reasoning payloads are all handled through different surfaces.

The user expectation behind E-NAI-001 is practical: repeat prompts may cost less because cached input is billed differently.

The user expectation behind E-NAI-004 is also practical: requesting low, medium, high, minimal, none, maximum, or explicit thinking budget must not silently become a different Provider behavior.

The operator expectation is stronger: if the gateway says it billed cache read at one rate and cache write at another, that claim must be replayable from an immutable Usage Record and a versioned pricing snapshot.

The critic's C-001 claim is corroborated by the source pattern at <region of source: text quota settlement missing/zero usage branch>; specifically, when upstream usage is absent or total tokens become zero, the source can settle actual usage as zero and only log the condition.

HUAKAI must not inherit that fail-open money path.

The critic's C-002 claim is corroborated by <region of source: text quota summary and tiered token normalization>; the source distinguishes normal input, cache read, aggregate cache write, Claude-style split cache write, image, audio, tool-call surcharges, group/model ratios, per-call price, and expression-mode billing.

The critic's C-003 claim is corroborated by <region of source: cross-format usage conversion and usage semantic markers>; the source needs semantic/source hints to avoid subtracting cache buckets against the wrong base.

The critic's C-004 claim is corroborated by <region of source: funding session settlement>; the source updates several ledgers and records a log-only branch when funding movement succeeds but a token-side adjustment fails.

The critic's C-005 claim is corroborated by <region of source: billing preference and funding-source selection>; the source supports subscription-first, wallet-first, only-wallet, and only-subscription behavior with fallback.

The critic's C-006 claim is corroborated by <region of source: tier-expression documentation and settlement fallback>; tier expressions are a second billing engine, with frozen request input and error fallback.

The critic's C-007 claim is corroborated by <region of source: reasoning translation layers>; reasoning effort is normalized through multiple incompatible Provider surfaces.

The critic's C-008 claim is corroborated by <region of source: usage data shapes and Provider response assembly>; requested effort, sent effort, budget, and actual reasoning-token counts are not always present in the same place.

The critic's C-009 claim is corroborated by <region of source: streaming scanner and terminal usage events>; streaming billing depends on terminal events or assembled final usage, and abnormal endings are recorded as stream status.

The critic's C-010 claim is corroborated by <region of source: channel affinity usage observation>; cache-affinity observation is a routing signal, not a billing-grade record.

Inference: upstream evolved under pressure to support many Providers quickly, while preserving backward compatibility with older ratio settings and multiple databases.

HUAKAI has different constraints.

DR-001 requires tenant isolation.

DR-002 requires Personal Edition and SaaS Distribution Edition to share concepts but not expose the same money surfaces.

DR-006 chooses PostgreSQL, which allows stricter transactional ledger guarantees than a lowest-common-denominator database design.

Therefore HUAKAI should keep the behavioral capability and redesign the money path.

## 2. WHAT (algorithm in HUAKAI vocabulary)

HUAKAI should model this as one request-scoped billing state machine with explicit usage buckets, explicit reasoning state, and immutable settlement evidence.

The core invariant is: one accepted request creates exactly one Usage Record, zero or more provisional events, and exactly one final Billing Ledger decision.

The final decision may be committed, provisional, failed-no-charge, or failed-with-provisional-charge.

It must never be silently zero because a Provider omitted usage.

### S-1 Request classification

Trigger condition: a client request reaches the gateway with a Model that is configured for cache-aware billing or reasoning control.

State transitions: the request context records tenant_id, User, API Key, User Group, requested Model, Route candidate, Channel candidate, and requested reasoning controls.

State transitions: no Billing Ledger entry is committed yet.

Concurrency interaction: two requests from the same User may classify concurrently, but both must receive independent request ids and idempotency keys.

Concurrency interaction: classification reads policy snapshots under tenant scope; it must not mutate shared pricing maps.

### S-2 Pricing policy snapshot

Trigger condition: classification resolves a Model and User Group eligible for billing.

State transitions: HUAKAI freezes a pricing_policy_version on the Usage Record draft.

State transitions: the snapshot includes normal input price, output price, cache-read price, cache-write price, split cache-write prices, image/audio prices, tool-call prices, per-call price, group multiplier, tenant override, edition guard, and expression-policy version if used.

Concurrency interaction: policy edits by an operator after request start do not affect the in-flight request.

Concurrency interaction: concurrent requests may bind different versions if the operator publishes a new version between them.

### S-3 Usage bucket initialization

Trigger condition: pre-consume needs an estimate before the Provider call.

State transitions: the request initializes bucket counters to zero and records an estimate source.

State transitions: initial estimated buckets may include estimated fresh input and requested output cap, but cache read/write, reasoning, and tool buckets remain unknown unless reliable pre-call evidence exists.

Concurrency interaction: estimates are request-local; no shared counter is changed except the Quota reservation.

### S-4 Quota reservation

Trigger condition: request is authorized and estimate is non-zero or tenant policy requires a minimum reservation.

State transitions: Quota reservation row is created with tenant_id, User, API Key, request id, estimate, policy version, and funding source preference.

State transitions: Billing Ledger is not finalized; the reservation is provisional.

Concurrency interaction: concurrent requests reserve against the same User balance or subscription benefit through PostgreSQL row locks or an equivalent serializable claim.

Concurrency interaction: duplicate idempotency keys must return the existing reservation instead of creating a second one.

### S-5 Funding-source selection

Trigger condition: a User has wallet balance, subscription benefit, or both.

State transitions: the request records selected funding source, fallback source if any, pre-consumed amount, and funding-source state version.

State transitions: when subscription-first fails for insufficient benefit, wallet fallback may proceed only if tenant policy allows it and the Usage Record records the fallback reason.

Concurrency interaction: two concurrent requests cannot consume the same subscription benefit because the benefit row is locked or claimed by idempotency key.

Concurrency interaction: fallback must be atomic with the failed first choice so that both sources are not charged.

### S-6 Reasoning request normalization

Trigger condition: the request contains explicit reasoning effort, explicit thinking budget, reasoning payload, or a compatibility suffix on the Model.

State transitions: HUAKAI records requested_reasoning_effort, requested_thinking_budget, requested_reasoning_surface, and requested_model_alias.

State transitions: HUAKAI maps the request to a canonical reasoning intent: disabled, minimal, low, medium, high, maximum, adaptive, or explicit budget.

Concurrency interaction: normalization is request-local.

Concurrency interaction: no global Model name rewriting is allowed to leak across requests.

### S-7 Provider-specific reasoning translation

Trigger condition: Route selects a Channel whose Provider requires a specific reasoning surface.

State transitions: HUAKAI records upstream_reasoning_effort, upstream_thinking_budget, upstream_reasoning_payload_kind, upstream_model_name, and any downgrade_or_rewrite flag.

State transitions: unsupported combinations fail closed or require an explicit compatibility policy.

Concurrency interaction: two requests to different Channels can translate the same canonical intent differently without sharing mutable state.

Concurrency interaction: Provider capability cache is read-only for the request and must be tenant-scoped if tenants can configure Providers.

### S-8 Upstream call and stream mode

Trigger condition: the transformed request is sent to the selected Channel and Provider Account.

State transitions: Usage Record moves from reserved to upstream_in_flight.

State transitions: stream_status starts as open when the response is streaming.

Concurrency interaction: concurrent streams must not share stream buffers or terminal usage assembly.

Concurrency interaction: Provider Account concurrency limits are separate from billing; they may reject dispatch before money settlement begins.

### S-9 Non-streaming usage ingestion

Trigger condition: a non-streaming Provider response returns.

State transitions: HUAKAI reads Provider usage into reported usage buckets and then normalizes into billing buckets.

State transitions: source confidence is set to reported, normalized, inferred, estimated, or missing.

Concurrency interaction: ingestion is request-local, but final ledger settlement locks the request claim.

Concurrency interaction: if a duplicate retry returns after the first settlement, it must attach as a duplicate attempt, not create another Billing Ledger entry.

### S-10 Streaming terminal usage ingestion

Trigger condition: a stream emits terminal usage, a final response event, an end marker, timeout, scanner error, ping failure, or client disconnect.

State transitions: stream_status records normal or abnormal ending.

State transitions: usage buckets are finalized from terminal usage if present; otherwise the request enters provisional reconciliation using partial usage and estimates.

Concurrency interaction: stream reader, downstream writer, and usage assembler may run simultaneously but must coordinate through one request-local stream state.

Concurrency interaction: terminal settlement must be idempotent if the stream end and client disconnect race.

### S-11 Cache read bucket

Trigger condition: reported usage states that some input was read from Provider cache.

State transitions: cache_read_input_tokens increases on the Usage Record.

State transitions: fresh_input_tokens is reduced only when the source format reports total input inclusive of cache.

Concurrency interaction: cache-read tokens are evidence from the Provider response, not from routing affinity metrics.

Concurrency interaction: concurrent requests with the same prompt do not share billing evidence; each Usage Record owns its own bucket.

### S-12 Cache write bucket

Trigger condition: reported usage states that new cache content was created.

State transitions: cache_write_input_tokens increases.

State transitions: if Provider semantics split write windows, cache_write_5m_tokens and cache_write_1h_tokens are recorded separately.

Concurrency interaction: two requests creating similar Provider cache content may race upstream, but HUAKAI bills only each Provider-reported or reconciled usage event.

Concurrency interaction: cache write does not update HUAKAI response-cache state unless a separate response-cache feature explicitly does so.

### S-13 Split cache-write normalization

Trigger condition: aggregate cache-write tokens and split cache-write tokens coexist or one is missing.

State transitions: HUAKAI records raw aggregate, raw split buckets, normalized cache-write total, and normalization rule.

State transitions: remainder tokens are assigned to the default cache-write policy only by documented policy.

Concurrency interaction: normalization is deterministic from the usage payload and policy snapshot.

Concurrency interaction: policy-version changes do not affect already-normalized Usage Records.

### S-14 Cross-format semantic guard

Trigger condition: a request or response was converted between OpenAI-compatible, Responses-style, Claude-style, Gemini-style, or another Provider format.

State transitions: Usage Record records final_request_format, usage_semantic, usage_source, and conversion chain.

State transitions: normalizer uses those markers to decide whether prompt input already includes cache/image/audio buckets.

Concurrency interaction: conversion metadata is request-local.

Concurrency interaction: missing markers force provisional billing rather than silent arithmetic.

### S-15 Multimodal and tool surcharge accounting

Trigger condition: response usage includes image/audio tokens, or the request invokes built-in tools with separately priced calls.

State transitions: image_input_tokens, image_output_tokens, audio_input_tokens, audio_output_tokens, web_search_calls, file_search_calls, and tool_surcharge_cost are recorded.

State transitions: if the pricing policy prices a bucket separately, that bucket is removed from generic input/output before calculation.

Concurrency interaction: tool-call counters must be assembled per request.

Concurrency interaction: shared tool price policy is read by version, not live mutable config.

### S-16 Tier-expression billing

Trigger condition: the pricing policy version selects expression-mode billing rather than static ratio billing.

State transitions: HUAKAI evaluates a bounded policy expression against estimated tokens during reservation.

State transitions: HUAKAI stores expression version, expression hash, used variable set, request-rule inputs, and estimated result.

State transitions: after response, HUAKAI evaluates the same expression snapshot against actual normalized buckets.

Concurrency interaction: expression compilation cache may be process-local, but the expression text/hash and version on the Usage Record are authoritative.

Concurrency interaction: expression evaluation failure creates a provisional reconciliation item rather than silently using stale or zero cost.

### S-17 Actual settlement

Trigger condition: normalized usage buckets are available or provisional policy has selected an estimated charge.

State transitions: Billing Ledger appends one final charge or provisional charge entry.

State transitions: Quota reservation is reconciled to actual cost; refund or supplemental charge is appended as separate ledger movement.

State transitions: User balance, subscription benefit, API Key quota, User aggregate, Channel aggregate, and Provider Account aggregate are derived or updated inside a transaction/outbox model.

Concurrency interaction: duplicate settlement attempts use the same request claim and become no-ops or read-only replay.

Concurrency interaction: partial failure after funding movement cannot be resolved by log-only state; it must create an operator-visible reconciliation item.

### S-18 Usage log enrichment

Trigger condition: final or provisional settlement completes.

State transitions: Usage Record becomes visible to User and operator with cost breakdown, cache-hit reason, reasoning metadata, stream status, and pricing snapshot.

State transitions: sensitive Provider payloads are not exposed; only normalized HUAKAI fields are shown.

Concurrency interaction: usage-log rendering reads immutable rows; it never queries live pricing maps to explain historical cost.

Concurrency interaction: a later reconciliation appends a correction and links to the original request instead of mutating history.

### S-19 Channel affinity observation

Trigger condition: Provider usage reports cache-read tokens and the routing layer wants to learn which Channel is cache-warm.

State transitions: route-affinity metrics may record request id, Channel, Model, cache-hit evidence, and TTL.

State transitions: those metrics are marked routing_observation, not billing_evidence.

Concurrency interaction: concurrent requests can update affinity metrics with last-write-wins or aggregate counters.

Concurrency interaction: Billing Ledger never reads affinity cache as proof of billable cache read.

### S-20 Unsupported pass-through governance

Trigger condition: a client supplies a Provider-specific field that can alter cost, privacy, or reasoning behavior.

State transitions: HUAKAI checks tenant policy and Channel capability.

State transitions: if allowed, the Usage Record records pass-through capability id, operator policy version, and audit trace.

State transitions: if denied, request fails with a stable validation error or strips the field only when the tenant has explicitly selected compatibility-strip mode.

Concurrency interaction: pass-through policy is tenant-scoped and versioned.

Concurrency interaction: two tenants cannot influence each other's allowed Provider-specific fields.

## 2-bis. Request lifecycles

### Happy-path lifecycle

1. Client sends a non-streaming request with explicit medium reasoning and a Model that has cache-aware pricing.

2. API Key auth resolves User, User Group, tenant, and edition.

3. HUAKAI binds the current pricing policy version and reasoning policy version.

4. HUAKAI normalizes requested reasoning into canonical intent.

5. Route selects a Channel and Provider Account.

6. The Provider translation layer translates canonical reasoning into the Provider's supported surface and records upstream reasoning metadata.

7. Quota reservation is inserted with idempotency key and estimated cost.

8. Provider returns a response with usage including fresh input, cache read, output, and reasoning tokens.

9. HUAKAI records raw usage, semantic markers, and normalized billing buckets.

10. HUAKAI calculates actual cost from the frozen policy snapshot.

11. Billing Ledger appends final charge and reservation delta.

12. Usage Record becomes settled, with cache-read cost and requested/sent/actual reasoning fields visible.

### Partial-failure lifecycle

1. Client opens a streaming request with high reasoning.

2. HUAKAI reserves estimated Quota and sends the Provider request.

3. The stream emits visible content and partial tool data.

4. Client disconnects before terminal usage arrives.

5. Stream status becomes abnormal with client_gone.

6. HUAKAI preserves partial output counters, first-response time, last event time, and any usage fragments already received.

7. Because terminal usage is missing, HUAKAI does not settle zero.

8. HUAKAI applies tenant policy: provisional estimate, partial-usage charge, or operator approval queue.

9. Billing Ledger records provisional charge or failed-no-charge with pending_reconciliation.

10. Operator sees request id, Channel, Provider Account, stream end reason, estimated cost, and missing buckets.

11. Later reconciliation may append a correction or refund.

### Full-failure lifecycle

1. Client request is accepted and reservation succeeds.

2. Provider call fails before any billable usage or response body is received.

3. HUAKAI marks upstream attempt failed with no usage evidence.

4. Reservation is released through a ledger reversal inside the settlement transaction.

5. Usage Record is finalized as failed-no-charge.

6. Route fallback may retry on a different Channel only if the same idempotency key gates final Billing Ledger settlement.

7. If all attempts fail, no actual usage charge is committed.

8. Cleanup obligations: release Provider Account concurrency slot, update Channel health, close stream/body resources, emit Audit/Event logs if automatic disabling or fallback occurred, and keep the failed Usage Record immutable.

## 3. INPUTS (signals consumed, state mutated)

### Per-Request

Fields read:

- tenant_id.
- request id.
- idempotency key.
- User id.
- API Key id.
- User Group.
- requested Model.
- requested route tags.
- request body.
- request headers used by pricing policy.
- requested stream mode.
- requested output cap.
- requested reasoning effort.
- requested thinking budget.
- requested reasoning payload.
- compatibility Model suffix aliases.
- Provider-specific pass-through fields.
- final request format.
- conversion chain.
- Provider response status.
- Provider response body.
- Provider usage payload.
- stream terminal event.
- stream abnormal end reason.
- Provider-reported cost when available.

Fields written:

- Usage Record id.
- pricing_policy_version.
- reasoning_policy_version.
- requested_model.
- billed_model.
- upstream_model.
- Channel id.
- Provider Account id.
- requested_reasoning_effort.
- canonical_reasoning_intent.
- upstream_reasoning_effort.
- upstream_thinking_budget.
- reasoning_downgrade flag.
- actual_reasoning_tokens.
- visible_reasoning_content flag.
- thinking_to_content flag.
- usage_semantic marker.
- usage_source marker.
- usage_confidence.
- fresh_input_tokens.
- cache_read_input_tokens.
- cache_write_input_tokens.
- cache_write_5m_tokens.
- cache_write_1h_tokens.
- output_tokens.
- image_input_tokens.
- image_output_tokens.
- audio_input_tokens.
- audio_output_tokens.
- tool_call_counts.
- tool_surcharge_cost.
- estimated_cost.
- actual_cost.
- provisional_cost.
- pending_reconciliation flag.
- stream_status.
- final settlement status.

### Per-Account / per-Channel

State read:

- Channel enabled/paused/degraded status.
- Channel allowed Models.
- Channel capability flags for reasoning, cache, image, audio, streaming, and pass-through.
- Provider Account lifecycle state.
- Provider Account concurrency state.
- Provider Account quota or health metadata.
- Channel pricing override if tenant allows it.
- Provider-specific capability cache.

State mutated:

- Channel used quota aggregate.
- Channel request count.
- Channel health observations.
- Provider Account usage aggregate.
- Provider Account concurrency slot.
- routing affinity observation.
- automatic health flags if upstream errors match policy.

Lifetime:

- aggregates live across requests.
- concurrency slots live for one in-flight request.
- affinity observations have short TTL and are not billing evidence.

### Per-Tenant

Isolation boundaries:

- tenant_id is mandatory on Usage Records.
- tenant_id is mandatory on Billing Ledger entries.
- tenant_id is mandatory on pricing policy versions.
- tenant_id is mandatory on reasoning policy versions.
- tenant_id is mandatory on API Key, User, and User Group checks.
- tenant_id is mandatory on pass-through governance.
- tenant_id is mandatory on reconciliation queues.

Tenant state read:

- tenant edition.
- tenant billing mode.
- tenant allowed Providers and Channels.
- tenant reasoning compatibility policy.
- tenant provisional billing policy.
- tenant data-retention policy for reasoning content.

Tenant state mutated:

- tenant usage aggregates.
- tenant billing summaries.
- tenant audit events.
- tenant reconciliation queue.

### Per-Process

In-memory caches and queues:

- compiled pricing expression cache.
- Provider capability cache.
- route selection cache.
- Channel affinity cache.
- stream scanner state.
- request-local usage assembly.
- goroutine-local streaming buffers.
- retry timers.
- notification/outbox worker queue.

Required HUAKAI boundary:

- in-memory caches are performance hints.
- PostgreSQL rows are the source of truth for billing.
- process crash may lose affinity observations but must not lose Usage Records or Billing Ledger movements.

### Persistent

Tables touched conceptually:

- Usage Record.
- Billing Ledger.
- Quota reservation.
- User balance.
- subscription benefit.
- API Key quota.
- User aggregate.
- User Group aggregate.
- Channel aggregate.
- Provider Account aggregate.
- pricing policy version.
- reasoning policy version.
- reconciliation queue.
- transactional outbox.
- Audit Event.

Indexes required conceptually:

- unique tenant_id + idempotency key for request claim.
- tenant_id + request id for Usage Record lookup.
- tenant_id + Usage Record id for Billing Ledger join.
- tenant_id + policy version for replay.
- tenant_id + reconciliation status for operator queue.
- tenant_id + Channel + Model + time for observability.

Transaction boundaries:

- Tx1 reserves Quota and creates request claim before Provider dispatch.
- Tx2 finalizes Usage Record, appends Billing Ledger movement, reconciles reservation, and enqueues outbox events.
- Reconciliation Tx appends correction rows and never rewrites original settlement evidence.

## 4. FAILURE MODES HANDLED

### FM-1 Missing upstream usage

Trigger: Provider response succeeds but usage is absent.

Observable outcome: request has content but no authoritative tokens.

Operator-visible signal: Usage Record status pending_reconciliation with usage_confidence missing.

Recovery action: charge provisional estimate, quarantine Channel if repeated, or route to operator approval.

Blast radius: single-account if isolated to one Provider Account; single-tenant if tenant policy allows that Channel.

### FM-2 Zero total usage

Trigger: usage exists but total tokens resolve to zero.

Observable outcome: billing cannot trust actual zero unless Provider is a known free endpoint.

Operator-visible signal: zero_usage_anomaly counter and request id.

Recovery action: provisional estimate or failed-no-charge only when no content was delivered.

Blast radius: single-request by default; single-Channel if repeated.

### FM-3 Cross-format cache double-count

Trigger: converted usage loses semantic/source marker.

Observable outcome: cache-read or cache-write tokens may be subtracted twice or not at all.

Operator-visible signal: normalization_confidence low and conversion-chain warning.

Recovery action: use conservative normalized policy and queue replay.

Blast radius: single-request; can become single-tenant if a tenant relies on the same conversion path.

### FM-4 Streaming terminal usage missing

Trigger: client disconnect, timeout, scanner error, ping failure, Provider closes stream early, or final usage event absent.

Observable outcome: visible content may have been delivered without final usage.

Operator-visible signal: stream_status with abnormal end reason and pending_reconciliation.

Recovery action: bounded stream drain if policy allows; otherwise provisional charge and operator review.

Blast radius: single-request, unless Provider stream format changes cluster-wide.

### FM-5 Funding settlement partial failure

Trigger: funding source charge succeeds but API Key quota or aggregate update fails.

Observable outcome: money movement and quota state disagree.

Operator-visible signal: reconciliation queue item with failed effect list.

Recovery action: append corrective ledger or retry idempotent aggregate/outbox update.

Blast radius: single-request, but repeated database issue can become cluster-wide.

### FM-6 Subscription/wallet fallback ambiguity

Trigger: preferred source is insufficient and fallback source is attempted.

Observable outcome: request could be charged to unexpected source.

Operator-visible signal: Usage Record lists selected source, failed source, fallback reason, and policy version.

Recovery action: refund wrong source if policy violation; notify User; adjust tenant policy.

Blast radius: single-User or single-tenant.

### FM-7 Tier-expression evaluation error

Trigger: expression compile/runtime error, unsupported variable, invalid request rule, or arithmetic overflow.

Observable outcome: price cannot be evaluated from the configured dynamic policy.

Operator-visible signal: pricing_policy_error event with expression hash and request id.

Recovery action: fail closed, or provisional estimate if tenant has enabled that policy; never zero-charge silently.

Blast radius: all Models using the bad policy version, usually single-tenant.

### FM-8 Unsupported reasoning translation

Trigger: requested effort or budget cannot be represented for selected Provider.

Observable outcome: Provider would reject, ignore, or alter reasoning behavior.

Operator-visible signal: reasoning_translation_failed or reasoning_downgraded event.

Recovery action: fail request, choose alternate Channel, or apply tenant-approved downgrade.

Blast radius: single-request or single-Model.

### FM-9 Pass-through cost/privacy field denied

Trigger: client sends a Provider-specific field that changes service tier, geography, speed, safety identity, obfuscation, or reasoning visibility.

Observable outcome: request validation fails or field is stripped by policy.

Operator-visible signal: policy_denied_pass_through Audit Event when operator visibility is enabled.

Recovery action: tenant admin enables explicit capability or client removes the field.

Blast radius: single-request.

### FM-10 Channel affinity cache loss

Trigger: process restart, Redis outage, cache flush, or TTL expiry.

Observable outcome: routing may lose cache-warm hints.

Operator-visible signal: affinity_cache_miss metrics.

Recovery action: route by normal policy; rebuild observations from future Usage Records.

Blast radius: single-process or cluster-wide for shared cache outage, but not billing.

## 5. FAILURE MODES NOT HANDLED (gaps)

The source does not provide a money-grade answer for missing usage.

It logs zero-token conditions and may settle zero usage after a successful Provider call.

HUAKAI must convert this into provisional billing plus reconciliation.

The source does not make all ledger effects atomic.

The critic's C-004 and S-007 are confirmed: funding can be committed before a token-side adjustment fails, and the failure is logged.

HUAKAI must use append-only ledger rows, idempotency claims, and a transactional outbox.

The source uses mutable counters and global pricing/config maps in several areas.

The critic's S-002 is confirmed: hidden global state includes pricing ratios, cache ratios, billing modes, channel caches, tokenizer caches, and affinity caches.

HUAKAI must bind tenant-scoped policy snapshots to each Usage Record.

The source has an inconsistent error taxonomy.

The critic's S-004 is confirmed: validation, quota, billing, upstream, and settlement errors appear through mixed typed and string-based paths.

HUAKAI needs stable enums for settlement status, usage confidence, reasoning translation result, and stream end class.

The source's dynamic billing expression engine is powerful but needs a stronger safety envelope.

The critic's F-003 is confirmed: expression billing is not just flexible static ratio configuration.

HUAKAI must support validation, dry-run, audit replay, resource limits, versioning, and emergency disable.

The source documents broad cache billing support but implements a Provider-specific patchwork.

The critic's D-001 and F-001 are confirmed: public claims are broader than the uniformity of actual Provider semantics.

HUAKAI should expose a normalized contract and keep Provider-specific behavior behind Provider translation layers.

The source accepts compatibility suffixes as operational inputs.

The critic's N-004 is confirmed: model-name suffixes are useful compatibility shims but should not be the primary HUAKAI public API.

The source has thinking-to-content behavior.

The critic's F-006 is confirmed: moving hidden reasoning into visible transcript content affects privacy, audit, and moderation semantics.

HUAKAI must make it a tenant policy with retention and disclosure controls.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- KEEP: Cache read, cache write, split cache write, multimodal, tool-call, and reasoning usage must be represented as distinct Usage Record buckets.

- KEEP: The Usage Record should explain why a repeated prompt was cheaper or more expensive.

- KEEP: Provider-specific reasoning translation is necessary because Providers expose incompatible reasoning controls.

- KEEP: Stream status must be persisted with end reason, errors, and terminal usage confidence.

- KEEP: Dynamic billing policy can be useful for long-context tiers and request-conditioned pricing.

- IMPROVE: Replace zero-usage fail-open with provisional billing and reconciliation.

- IMPROVE: Replace mutable counters as money truth with append-only Billing Ledger entries.

- IMPROVE: Replace global pricing maps with tenant-scoped, edition-scoped, effective-dated policy snapshots.

- IMPROVE: Store requested, canonical, sent, and actual reasoning fields separately.

- IMPROVE: Store usage_semantic and usage_source markers so converted responses cannot double-charge cache.

- IMPROVE: Stream interruption must produce a final Usage Record with partial usage evidence and no duplicate charge.

- IMPROVE: Tier-expression evaluation must run in a bounded policy sandbox with compile validation, dry run, and audit replay.

- IMPROVE: Subscription/wallet fallback must be explicit in the Usage Record and idempotent across retries.

- IMPROVE: Channel affinity metrics must remain routing observability only.

- AVOID: Do not copy zero actual settlement when Provider usage is missing.

- AVOID: Do not copy mutable in-place wallet, API Key, User, Channel, and subscription counters as source of truth.

- AVOID: Do not copy multi-database compromises; DR-006 allows PostgreSQL constraints and row locks.

- AVOID: Do not copy Model suffixes as the main reasoning contract.

- AVOID: Do not copy pass-through switches as an escape hatch around governance.

- AVOID: Do not expose hidden reasoning as visible content without tenant policy and audit.

- AVOID: Do not let cache-affinity observations become tenant-visible billing evidence.

HUAKAI-specific risk 1 under DR-001: global pricing maps or shared caches can leak tenant policy and cost behavior across tenants.

HUAKAI-specific risk 2 under DR-001: pass-through Provider fields may expose privacy-sensitive identifiers unless tenant policy denies them by default.

HUAKAI-specific risk 3 under DR-002: Personal Edition may run without full subscription/payment surfaces, while SaaS Distribution Edition must preserve money-grade ledger controls; copying one behavior for both editions would be unsafe.

HUAKAI-specific risk 4 under DR-006: copying lowest-common-denominator counter updates would waste PostgreSQL's ability to enforce idempotency, row locking, numeric precision, and transactional outbox semantics.

HUAKAI-specific risk 5 under DR-006: expression-mode billing without PostgreSQL-stored policy snapshots would make historical reprice and audit replay non-deterministic.

HUAKAI-specific risk 6 under DR-001 and DR-002: reasoning content retention can become tenant data exposure if hidden reasoning is moved into visible response text by compatibility behavior.

HUAKAI-specific risk 7 under DR-001: Channel affinity metrics based on cache hits may reveal one tenant's prompt/cache patterns if keys or observations are not tenant-scoped.

## 7. ATTRIBUTION

- Source files read: <region of source: README cache billing and reasoning claims, AGPL source mirror, redacted per CL-002>.

- Source files read: <region of source: usage structures, cache buckets, reasoning-token fields, and semantic markers>.

- Source files read: <region of source: cache-aware text quota calculation, zero-usage branch, log enrichment, tool surcharge>.

- Source files read: <region of source: tier-expression documentation, token normalization, frozen snapshot settlement, fallback tests>.

- Source files read: <region of source: funding-source abstraction, subscription/wallet fallback, pre-consume, refund, settlement delta>.

- Source files read: <region of source: stream scanner, ping/timeout/client-gone status, terminal usage assembly>.

- Source files read: <region of source: Provider translation layers for OpenAI-compatible, Claude-style, Gemini-style, DeepSeek-style, xAI-style, and OpenRouter-style reasoning>.

- Specifier-lane session: Codex specifier-lane Round 2.

- Reviewer-lane session: Pending.

- Verified clean-room compliance: no upstream function names, struct field names, file paths, package names, source code, schemas, or tests are included in this decomposition.

- Verified clean-room compliance: source regions are described by behavior only.

- Verified clean-room compliance: HUAKAI design uses local vocabulary from docs/18_GLOSSARY.md and local DR constraints.

## 8. ACCEPTANCE TEST DIRECTIONS

AT-BILL-003-01: cache-read billing records fresh input, cache-read input, output, pricing policy version, and final cost.

AT-BILL-003-02: cache-write billing records aggregate and split 5m/1h buckets when present.

AT-BILL-003-03: cross-format converted usage preserves semantic/source marker and does not double-subtract cache.

AT-BILL-003-04: missing usage after successful content creates provisional reconciliation, not zero-charge settlement.

AT-BILL-003-05: zero total usage with delivered content creates anomaly signal and provisional policy path.

AT-BILL-003-06: tier-expression policy uses the frozen pre-consume snapshot during settlement.

AT-BILL-003-07: expression evaluation error fails closed or creates provisional charge according to tenant policy.

AT-BILL-003-08: wallet-first fallback to subscription records the failed source and selected source.

AT-BILL-003-09: subscription-first fallback to wallet is idempotent and never charges both sources.

AT-BILL-003-10: duplicate request id after successful settlement returns the existing Billing Ledger result.

AT-BILL-003-11: stream client disconnect before terminal usage creates pending reconciliation and no duplicate charge.

AT-BILL-003-12: Channel affinity cache hit is recorded as routing observation and is absent from Billing Ledger evidence.

AT-MODEL-001-01: explicit low/medium/high reasoning records requested, canonical, and upstream reasoning fields.

AT-MODEL-001-02: explicit budget records requested budget and sent Provider budget.

AT-MODEL-001-03: compatibility suffix maps to canonical intent but public Usage Record records the original requested Model alias.

AT-MODEL-001-04: unsupported Provider reasoning combination fails closed unless tenant policy allows downgrade.

AT-MODEL-001-05: actual reasoning tokens are recorded when Provider reports them.

AT-MODEL-001-06: missing actual reasoning tokens does not erase requested/sent reasoning metadata.

AT-MODEL-001-07: thinking-to-content requires tenant policy and creates audit signal.

AT-MODEL-001-08: pass-through cost/privacy fields are denied by default and recorded when allowed.

## 9. REVIEW SIGN-OFF

```markdown
## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending reviewer-lane |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Round 2 draft deliberately uses redacted source regions and HUAKAI terms only. |
```

Before release to implementer-lane, reviewer must verify:

- CL-001: no AGPL source code copied.

- CL-002: source attribution is behavior-only and redacted.

- CL-003: no upstream file paths or package names appear.

- CL-004: no upstream schema names are copied.

- CL-005: no line-by-line algorithm translation is present.

- CL-006: no upstream tests are copied.

- CL-007: lane mode is Option C for billing ledger aspects.

- CL-008: HUAKAI DR-001, DR-002, and DR-006 divergences are explicit.

- CL-009: no feature is dropped due to license or security risk.

- CL-010: critic findings are all addressed.

## 10. Source Coverage Proof

1. <region of source: public README feature claim area> contributed the public evidence for E-NAI-001 and E-NAI-004: cache-aware billing is advertised across several Provider families, and reasoning effort is advertised through explicit effort and Model aliases.

2. <region of source: usage data shape and response conversion area> contributed the data-shape evidence: usage can contain normal input/output, cached input, cache creation, split cache creation, image/audio details, reasoning tokens, Provider cost, usage semantic, and usage source.

3. <region of source: text quota settlement area> contributed the main billing behavior: cache read is priced separately, cache creation is priced separately, split cache creation windows have separate ratios, image/audio/tool surcharges exist, total-zero usage can settle to zero, and logs are enriched with billing details.

4. <region of source: cross-format Claude/OpenAI conversion area> contributed the semantic drift evidence: Claude-style input excludes cache buckets while OpenAI-compatible usage may include them, so semantic/source markers determine whether subtraction is safe.

5. <region of source: tier-expression documentation and tests> contributed the expression-engine evidence: expression billing freezes request input, detects used variables, evaluates estimated and actual usage, supports request functions, records matched tier, and falls back when evaluation fails.

6. <region of source: funding-source and billing-session area> contributed the settlement edge-case evidence: wallet and subscription funding are separate, preference fallback exists, pre-consume and settle use deltas, refund may run asynchronously, and some partial settlement errors are log-only.

7. <region of source: streaming scanner and Provider stream handlers> contributed the lifecycle evidence: stream end can be normal, EOF, timeout, scanner error, ping failure, panic, or client-gone; terminal usage is assembled from final events when present.

8. <region of source: Provider reasoning translation layers> contributed the reasoning evidence: explicit effort, reasoning payload, thinking budget, Model suffix, no-thinking aliases, adaptive thinking, and Provider-specific rejection workarounds are all present.

9. <region of source: channel affinity observation area> contributed the separation evidence: cache-hit observations affect routing affinity, but they are not sufficiently immutable to act as billing proof.

10. <region of source: pass-through governance area> contributed the security evidence: some cost-sensitive or privacy-sensitive Provider fields are filtered by default and can be enabled by Channel settings.

## 11. Round-2 critic-finding addressed table

| Critic finding ID | This round's status | Where addressed in this file |
|---|---|---|
| C-001 | CONFIRMED | §1, §4 FM-1, §5, §6 |
| C-002 | CONFIRMED | §1, §2 S-11..S-16, §3 |
| C-003 | CONFIRMED | §1, §2 S-14, §4 FM-3 |
| C-004 | CONFIRMED | §1, §2 S-17, §4 FM-5, §5 |
| C-005 | CONFIRMED | §1, §2 S-5, §4 FM-6 |
| C-006 | CONFIRMED | §1, §2 S-16, §4 FM-7 |
| C-007 | CONFIRMED | §1, §2 S-6..S-7, §8 |
| C-008 | CONFIRMED | §1, §2 S-6..S-7, §3, §8 |
| C-009 | CONFIRMED | §1, §2 S-10, §2-bis, §4 FM-4 |
| C-010 | CONFIRMED | §1, §2 S-19, §4 FM-10, §6 |
| F-001 | CONFIRMED | §5, §6 |
| F-002 | CONFIRMED | §2 S-6..S-7, §8 |
| F-003 | CONFIRMED | §2 S-16, §5, §6 |
| F-004 | CONFIRMED | §4 FM-1..FM-2, §6 |
| F-005 | CONFIRMED | §6 HUAKAI risks, §3 Persistent |
| F-006 | CONFIRMED | §5, §6, §8 |
| D-001 | CONFIRMED | §5, §6 |
| D-002 | CONFIRMED | §2 S-6..S-7, §10 |
| D-003 | CONFIRMED | §2 S-6, §10 |
| D-004 | CONFIRMED | §6 HUAKAI risks |
| D-005 | CONFIRMED | §2 S-20, §4 FM-9, §6 |
| N-001 | CONFIRMED / AVOID | §6 |
| N-002 | CONFIRMED / AVOID | §6 |
| N-003 | CONFIRMED / AVOID | §2 S-2, §6 |
| N-004 | CONFIRMED / AVOID | §2 S-6, §6 |
| N-005 | CONFIRMED / AVOID | §2 S-7, §3 |
| N-006 | CONFIRMED / AVOID | §2 S-20, §6 |
| N-007 | CONFIRMED / AVOID | §3 Persistent, §6 |
| N-008 | CONFIRMED / AVOID | §2 S-19, §6 |
| S-001 | CONFIRMED | §1, §4 FM-1..FM-2 |
| S-002 | CONFIRMED | §5, §6 |
| S-003 | CONFIRMED | §5, §6 |
| S-004 | CONFIRMED | §5, §8 |
| S-005 | CONFIRMED | §3 Per-Process, §4 FM-10 |
| S-006 | CONFIRMED | §2 S-20, §6 |
| S-007 | CONFIRMED | §4 FM-5, §5 |
| S-008 | CONFIRMED | §3, §4 FM-6, §6 |

中文总结：本轮按 Owner 要求把 F-BILL-003 + F-MODEL-001 拆到请求状态机、20 个子行为、3 条生命周期、完整输入/状态面、10 类失败模式、HUAKAI 风险和 source coverage proof；critic 的 37 条编号 finding 全部在表中处置，结论均为源码确认或确认后规避，没有留下未回应项；相比 round-1 浅版，关键差异是明确了缺失 usage 不能零结算、cache read/write/split/write/audio/image/tool/reasoning 必须分桶、requested/canonical/sent/actual reasoning 必须分离、tier expression 是第二计费引擎、wallet/subscription 需要幂等账本；HUAKAI 应吸收的是行为能力和运营风险清单，拒绝复制 AGPL 实现形状、全局配置、可变计数器和 fail-open money path。
