# F-POOL-001 Provider Account Selection Algorithm (Codex pass)

| Field | Value |
| --- | --- |
| Status | Draft |
| Lane mode | Option C strict carve-out |
| Author | Codex specifier lane |
| Date | 2026-04-28 |
| Feature ID | F-POOL-001 |
| Sources read | Sub2API behavior at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`; one-api behavior at commit `8df4a2670b98266bd287c698243fff327d9748cf`. Source paths, implementation identifiers, schema names, comments, and code structure are intentionally omitted. |
| Consistency target | [quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md): Pool selection invokes Quota reservation, every authority boundary carries `tenant_id`, and the final Usage Record carries `routing_reason`. |

## Evidence Scope and Clean-Room Boundary

This file is a specifier-lane decomposition, not implementer guidance copied from reference code. The references were used only to learn observable product behavior: affinity layers, candidate gates, score signals, wait behavior, retry behavior, accounting gaps, and operator diagnostics. All HUAKAI design below is stated in HUAKAI vocabulary and must be implemented from HUAKAI domain contracts, not from upstream structures.

The cross-source conclusion is simple: Sub2API is stronger on layered Provider Account affinity, live signal scoring, bounded wait behavior, and diagnostic categories; one-api is stronger as a minimal Channel routing baseline with operator priority, group/model eligibility, forced administrative override, and retry across eligible Channels. Neither reference gives HUAKAI a complete money-grade selection algorithm. The missing piece is the strict coordination point where Provider Account selection, Quota reservation, Billing Ledger claim, concurrency admission, retry accounting, and Usage Record creation meet under one tenant-scoped authority model.

## Sub2API Decomposition

### 1. WHY

Sub2API treats Provider Account selection as a stateful relay problem rather than a simple random pick. That matters for HUAKAI because the product is a relay-station platform: Users buy a logical service, while operators manage many Provider Accounts with different capabilities, limits, latency, health, and current load. The selector must preserve continuity when provider-side state exists, avoid Accounts that are temporarily unhealthy, distribute load across the Pool, and explain routing decisions after the fact.

Its strongest behavioral lesson is the three-layer affinity model. A request can prefer the Provider Account used by a previous provider-side conversation, then a sticky session binding, then a fresh pooled choice. Each layer is only a hint until revalidated. The design therefore protects continuity without letting stale affinity override health, capability, operator policy, or per-request failover exclusions.

### 2. WHAT step-by-step in HUAKAI vocabulary

The first layer tries continuation affinity. If the request carries a continuation marker that maps to a prior Provider Account, the selector checks whether that Account still belongs to the right tenant-visible routing context, is usable for the requested model family, supports the required capability class, is not excluded by this request's retry history, and can accept an in-flight request. If those checks pass, the request keeps the same Provider Account and records that continuity was preserved. If any check fails, the failure becomes a routing reason and the selector continues.

The second layer tries sticky session affinity. A stable session key can map the User's conversation to a Provider Account for a bounded time. The mapping is not authority by itself. The candidate is rechecked for lifecycle state, model support, capability support, transport compatibility, group policy, current health, current load, and per-request exclusion. If the sticky candidate is healthy but busy, the selector can return a bounded wait intent rather than immediately breaking affinity. If the sticky candidate is invalid, the binding can be broken with a fixed reason taxonomy.

The third layer performs fresh Pool selection. The selector builds an eligible candidate set from Route, Channel, Pooling Group, model, capability, lifecycle, operator policy, request exclusion, and health state. It then evaluates signal scores, keeps a strong-candidate band, and randomizes within that band so a single best-looking Provider Account does not receive every request. Before final use, the selected candidate is rechecked against authoritative state so a stale snapshot cannot become the final route.

Sub2API also shows a capability shift pattern: when a request asks for a capability that can be served through a safe compatible mode, the selector may fall back from an exact-native capability to a safe equivalent if policy allows it. HUAKAI should keep the product behavior, but not make it implicit. The shift must be visible in `routing_reason`, governed by Route policy, and testable as either exact capability, safe equivalent, or reject.

### 3. INPUTS exhaustive

Inputs include `tenant_id`, User, API Key, Route, Channel, Pooling Group, requested model family, normalized model after Route policy, request endpoint family, capability requirements, session key, continuation marker, per-request exclusion set, retry attempt number, streaming mode, estimated request size, estimated maximum provider cost, operator priority, Provider Account lifecycle state, credential health, model support, capability support, group access policy, current concurrency usage, wait queue depth, recent provider error signal, recent first-response latency signal, Provider Account quota headroom, User and API Key quota headroom, snapshot freshness, route policy version, current time, and stable randomness.

Mutated state includes concurrency lease, wait queue entry, sticky binding, continuation binding, Billing Ledger claim, Quota reservation, Provider Account quota reserve, Usage Record draft, retry attempt record, Audit Event, health signal updates, and post-commit cache invalidation. The important distinction is that live cache or snapshot state can rank candidates, but cannot authorize spend.

### 4. FAILURES HANDLED

The reference behavior handles stale continuation by falling through to sticky or fresh selection. It handles stale sticky by revalidating before use and by clearing invalid bindings. It handles overloaded affinity by waiting within a bounded budget instead of breaking context immediately. It handles per-request retry loops by excluding failed Provider Accounts from later attempts. It handles temporarily unhealthy Accounts through health and cooldown signals. It avoids strict top-one stampedes through strong-candidate randomization. It gives operators diagnostic categories for why candidates were excluded, such as disabled, unsupported, temporarily unhealthy, model-ineligible, or over current operational limits.

### 5. FAILURES NOT HANDLED

The largest gap is that Provider Account selection is not the same atomic authority as Quota reservation. A selector can reserve a concurrency slot while User, API Key, Provider Account quota, and Billing Ledger state are still outside the final money-grade transaction. That leaves windows where a request is admitted operationally but later cannot be reserved financially, or where retry changes Provider Account without one shared billing claim.

The wait model also has split authority. Queue counters and actual slots are separate primitives, so a queued request can observe one state and later hit another. A slot release failure can survive until lease expiry. Candidate snapshots are rechecked, which is good, but the recheck still needs to be folded into HUAKAI's reservation boundary.

The diagnostic model is useful but incomplete for HUAKAI. It explains why selection failed, but the product needs a persistent `routing_reason` on the Usage Record and Audit Event, including sticky break reason, capability shift, scoring signal summary, Quota reservation identity, and retry path.

### 6. KEEP / IMPROVE / AVOID specific to HUAKAI

Keep the three-layer selection order: continuation affinity, sticky session affinity, fresh pooled selection. Keep revalidation at every layer. Keep per-request exclusion during retry. Keep bounded wait behavior for affinity, but make it explicit that queued requests are not final routes. Keep scoring with randomized strong-candidate selection.

Improve by making every final selection invoke tenant-scoped Quota reservation and Billing Ledger claim before any provider call. Improve by turning capability fallback into a declared Route policy outcome. Improve by making diagnostics durable, queryable, and tied to Usage Record and Audit Event. Improve by treating cache as signal only; authority lives in transactional reservation and reconciliation.

Avoid any fast path where affinity skips revalidation, any concurrency-only admission without quota reserve, any generated non-stable request identity for Billing Ledger, and any silent capability downgrade.

### 7. ATTRIBUTION

Reference: Sub2API, LGPL-3.0, verified at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` on 2026-04-28. Used as behavioral evidence only. This file does not reproduce source code, file paths, function names, field names, schema names, comments, tests, or implementation structure.

## one-api Decomposition

### 1. WHY

one-api represents the simpler Channel-routing baseline HUAKAI must exceed. It shows the minimum operator expectation: API Key authentication, User status checks, group/model eligibility, priority-based Channel choice, administrative override, retry to another Channel after eligible failures, and usage accounting after the provider response. It is valuable because it exposes which commercial relay behaviors appear in a lean product, and which money-grade gaps appear when routing, quota, and billing remain separate.

### 2. WHAT step-by-step in HUAKAI vocabulary

A request authenticates through an API Key, resolves the owning User, checks User and API Key status, checks API Key model scope when present, checks network policy when present, and may allow an administrative forced Channel. The normal route resolves the User's Pooling Group and chooses a Channel that is eligible for the requested model. Selection is priority-first and then randomized within the best eligible priority band. On retry, the selector can look beyond the first priority band so a failing top-priority Channel does not trap the request.

After Channel choice, the relay attaches provider connection settings, maps the requested model if policy requires it, sends the request, and handles conversion. If the provider response is retryable and the request was not forced to a single Channel, the relay can choose another eligible Channel, avoid repeating the immediately failed Channel, and try again. Usage is then charged or refunded through separate quota and usage paths depending on endpoint family.

### 3. INPUTS exhaustive

Inputs include `tenant_id` equivalent deployment context, API Key, User, User status, API Key status, API Key quota snapshot, API Key model scope, API Key network policy, User Pooling Group, requested model, mapped model, endpoint family, Channel eligibility, Channel priority, Channel lifecycle state, administrative override, retry count, last failed Channel, request size estimate, requested output budget, model billing ratio, group billing ratio, current User quota, cached quota signal, response status, response usage, and provider health feedback.

### 4. FAILURES HANDLED

The behavior rejects invalid API Keys, disabled Users, API Keys outside model scope, network-disallowed requests, disabled forced Channels, and requests without eligible Channels. It can retry another eligible Channel after retryable provider failure. It can record Channel health feedback and later make unhealthy Channels unavailable. It can pre-check User quota for some endpoint families and refund some failed attempts.

### 5. FAILURES NOT HANDLED

The selector does not score live concurrency, queue depth, first-response latency, error trend, quota headroom, or capability confidence. It mostly relies on priority and randomization within a priority bucket. It does not reserve Provider Account quota as part of selection. Text-like requests may reserve some User quota before the provider call, but the reservation is not a single tenant-scoped claim covering User, API Key, Provider Account, retry, Usage Record, and Billing Ledger. Some endpoint families perform only a check before the provider call and charge after success, which leaves a larger concurrent overspend window.

Forced Channel is operationally useful but dangerous if copied without guardrails. If override bypasses normal selection, HUAKAI must still require lifecycle, tenant, authorization, Quota reservation, Billing Ledger claim, and Usage Record attribution. Retry exclusion is also too weak for HUAKAI because avoiding only the last failed route can repeat other failed candidates in a longer attempt chain.

### 6. KEEP / IMPROVE / AVOID specific to HUAKAI

Keep group/model eligibility, operator priority, randomized selection inside a priority band, administrative forced route as a break-glass capability, and retry across eligible routes after provider failure. Improve priority-only selection with live signal scoring and durable diagnostics. Improve retry by tracking the full per-request exclusion set. Improve forced routing by making it auditable and non-exempt from Quota and Billing gates.

Avoid trusted high-balance shortcuts, cache-authorized spend, endpoint-family-specific charging gaps, post-success-only accounting for commercial requests, and any routing path that produces a Usage Record without `routing_reason`.

### 7. ATTRIBUTION

Reference: one-api, MIT, verified at commit `8df4a2670b98266bd287c698243fff327d9748cf` on 2026-04-28. Used as behavioral evidence only. This file does not reproduce source code, file paths, function names, field names, schema names, comments, tests, or implementation structure.

## HUAKAI Algorithm Design (Option C strict)

HUAKAI should treat Provider Account selection as an admission-and-reservation protocol, not as a pure picker. The public behavior is: preserve continuity when safe, pick a healthy and capable Provider Account when continuity cannot be preserved, reserve quota before provider spend, record why the route was chosen, and make retry share one logical Billing Ledger claim.

The algorithm has four phases.

Phase A builds candidate intent. The request resolves `tenant_id`, User, API Key, Route, Channel, Pooling Group, requested model, endpoint family, capability requirement, session key, continuation marker, stable request identity, and attempt number. Hard gates remove candidates that cannot legally serve the request: wrong tenant, disabled lifecycle state, disallowed Route or Channel, model not supported, capability impossible, credential unavailable, request excluded by this retry, or policy override not authorized. This phase may read cache and snapshots, but all outputs are provisional.

Phase B scores provisional candidates. The scoring function is policy-driven, not hardcoded:

`score = sum(policy_weight(signal_name) * normalized_signal_value)`

Signals include operator priority, continuity affinity, capability confidence, Provider Account quota headroom, User and API Key reserve fit, current concurrency load, wait queue depth, recent error trend, recent first-response latency, health probe state, fairness debt, and snapshot freshness. Hard gates are not represented as negative scores; they remove candidates. Policy weights are tenant/Route/Pooling Group knobs with bounded ranges and versioned defaults. The scorer returns a ranked band plus a signal explanation map. The final candidate is randomized among a strong-candidate band to prevent stampede and to preserve long-run fairness.

Phase C performs atomic admission. HUAKAI must not call a provider after selection until the Quota and Billing authority has accepted the request. The reservation boundary locks or atomically claims, scoped by `tenant_id`, the logical request identity, API Key, User quota, subscription or quota windows if present, Provider Account quota, Provider Account concurrency lease, and Billing Ledger claim. The same boundary creates a Usage Record draft or attempt record with initial `routing_reason`. If a Provider Account candidate loses authority during this boundary, the selector releases any provisional lease and attempts the next candidate from the ranked band. If no candidate can be reserved, the request fails with a diagnostic selection outcome, not a provider call.

Phase D reconciles. On provider success, the reservation is reconciled to actual usage, Provider Account quota is finalized, the Billing Ledger claim is marked applied, and the Usage Record becomes final in the same transaction described by the quota-billing synthesis. On provider failure, the attempt records the failure, releases Provider Account concurrency, and either retries through the same logical Billing Ledger claim with a new candidate reservation or rolls back according to failure class. Retry must never create a second customer charge for the same logical request. A crash sweeper uses reservation lease time, heartbeat state, and claim status to close orphaned attempts.

Atomic primitives are explicit. Money state uses database transactions and row-level or equivalent tenant-scoped locks. Concurrency state uses a lease keyed by `tenant_id`, Provider Account, logical request identity, and attempt identity, with heartbeat for long streams and deterministic release on reconcile. Wait queue state is a leased intent, not a final route and not a final quota reservation. A request waiting for sticky affinity may hold a queue position within a bounded budget, but when the wait ends it must re-enter admission and reserve authority against current state. This closes the queue-versus-slot race and prevents quota from being held by long-waiting requests.

Race-window closures are part of the spec. Stale continuation and sticky mappings are revalidated before use and their break reason is recorded. Candidate snapshots are rechecked during reservation. A slot acquired without quota must be released immediately if reservation fails. A quota reservation without provider completion must be expired or reconciled by sweeper. A retry that switches Provider Account must keep the same Billing Ledger claim and create a new attempt-level route reason. Cache invalidation happens after commit, but cache is never authority. `tenant_id` is present in every key, lock, claim, reservation, Usage Record, Audit Event, and diagnostic query.

`routing_reason` is a required structured field on Usage Record and should also appear in Audit Event for failures without final usage. It contains the selection layer, affinity key class without secret material, sticky break reason if any, capability outcome, candidate counts by exclusion category, selected Route/Channel/Pooling Group, selected Provider Account identity, scoring policy version, signal contributions, wait action, retry attempt number, full per-request exclusion summary, Quota reservation identity, Billing Ledger claim identity, and override actor when forced routing was used. It must not include provider credentials, API Key secrets, raw prompts, or provider response bodies.

Operator diagnostics must be first-class. For every "no route" or "route changed" outcome, the system should answer: how many candidates existed, how many were removed by tenant/policy/model/capability/health/quota/concurrency/retry exclusion, whether sticky was broken, whether a safe capability equivalent was used, whether a wait budget expired, which policy version scored the candidates, and whether the final failure was selection, reservation, provider response, or reconciliation. This gives operators enough information to tune Pools without reading logs.

Test strategy follows the money-grade boundary. Unit tests cover hard gates, score normalization, policy weight validation, strong-band randomization, capability exact/safe-equivalent/reject outcomes, and `routing_reason` taxonomy. Integration tests run concurrent requests against limited User, API Key, and Provider Account quota and assert that accepted reservations never exceed authority. Affinity tests cover continuation hit, sticky hit, stale sticky break, capability mismatch, disabled Provider Account, retry exclusion, and busy sticky wait. Queue tests assert that queued requests do not hold final quota, time out cleanly, and re-enter admission before provider call. Retry tests assert one logical request identity, one Billing Ledger claim, multiple attempt reasons, and no double charge across Provider Account switches. Crash tests stop execution after lease, after reservation, after provider success, and before reconcile, then verify sweeper recovery. Tenant-isolation tests reuse the same external request identity in two tenants and prove the claims and quotas do not collide. Diagnostic tests assert operator-visible categories for every failed gate.

Open questions for Owner and planner: whether forced routing is tenant-operator-visible or platform-admin-only; whether sticky wait budget is configured per Route or per Pooling Group; whether Provider Account quota reserve lives in the same physical table family as User/API Key quota or in a separate Provider Account capacity ledger; and whether capability safe-equivalent policy is enabled by default or opt-in per Route.

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending fresh reviewer-lane session |
| Review date | Pending |
| Checks passed | Pending CL-001..010 review |
| Notes | Specifier-lane draft only. Implementer-lane agents must use a reviewed HUAKAI spec derived from this decomposition, not this reference-mining document directly. |
