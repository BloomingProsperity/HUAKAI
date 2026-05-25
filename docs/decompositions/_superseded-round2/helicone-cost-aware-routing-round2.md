# helicone - Cost-aware routing chains, cost forecast, and tier vectors

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Helicone AI Gateway; license metadata inconsistent: GitHub repository chrome shows GPL-3.0 while visible workspace manifest / README text show Apache-2.0; treat as non-MIT source under CL-002 |
| Feature in HUAKAI matrix | F-ROUTE-001 (L2) + F-CONFIG-001 (L2) |
| Evidence ledger row | E-HLC-001, E-HLC-006; deep source evidence to be added as E-HLC-DEEP-ROUTE-001 after reviewer-lane approval |
| Specifier session | Codex specifier-lane ROUND 2 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | Redacted source regions only: public routing docs; public cost optimization docs; repository README source region; workspace manifest source region; embedded Provider/Model catalog source region |

## 1. WHY (motivation / context)

The upstream product is solving a real operations problem: a client wants to call one logical Model while the actual Provider can change because of outage, rate limit, cost, regional availability, or account-credit preference.

The public routing docs state four pressures directly: Provider outages, Provider rate limits, regional restrictions, and vendor lock-in. That is corroborated by the source pattern at the repository README region, where the gateway is positioned as a single OpenAI-compatible gateway with routing, rate limit, cache, tracing, and fallbacks.

The cost pressure is separate from availability. The cost docs say the Model Registry exposes current prices across Providers and the gateway sorts by cost to choose the cheapest option. That supports E-HLC-001 but also shows why Round 1 was too shallow: cheapest routing is a policy chain, not one comparison.

The critic's F-001 claim is corroborated by the source pattern at the provider-routing and cost-optimization regions: cheapest selection consumes at least candidate discovery, tenant-owned Provider Account status, platform-managed Provider Account eligibility, price catalog state, failover taxonomy, and tie distribution.

The critic's F-002 claim is corroborated by the docs' "zero configuration" examples, but HUAKAI cannot adopt that phrase literally. In HUAKAI, a SaaS Edition tenant must have tenant isolation, Provider Account authorization, Route policy, Quota policy, Billing Ledger treatment, and data-residency policy before any automatic fallback is safe.

The critic's S-006 claim is corroborated by source drift: the repository README, npm-visible text, docs, and GitHub repository chrome do not expose a single stable routing contract. This decomposition therefore separates observed behavior from HUAKAI requirements and does not transfer upstream code shapes.

Inference: the upstream optimizes for low-friction adoption. HUAKAI should optimize for auditable multi-tenant operations, because DR-001 makes "where did this prompt go and why" a product requirement rather than an implementation detail.

## 2. WHAT (algorithm in HUAKAI vocabulary)

HUAKAI should absorb the user outcome, not the upstream grammar or implementation. The desired local behavior is: a request enters with a logical Model, optional request routing hints, a User, an API Key, and tenant context; the gateway resolves a typed Route policy; it builds a tenant-authorized candidate set of Channels and Provider Accounts; it ranks candidates by effective cost, policy, health, and tier weights; it sends the request to one Provider Account; it records the decision and settles Quota, Usage Record, and Billing Ledger state.

### S-1: Logical Model candidate discovery

Trigger condition: request has a logical Model and no Provider lock or deployment lock.

State transitions: request-local routing context gains a candidate list; no persistent mutation yet. Candidate entries must include logical Model, Channel, Provider, Provider Account eligibility class, model mapping version, and source of eligibility.

Concurrency interaction: two simultaneous requests may read the same Model Registry snapshot. HUAKAI must stamp the snapshot version into both Usage Records so operators can explain different winners if a registry reload happens between them.

Critic C-001 is confirmed: the source docs say the gateway finds all Providers for a requested model before applying cheapest selection.

### S-2: Tenant-owned Provider Account priority

Trigger condition: tenant has one or more active Provider Accounts or tenant-scoped deployments that can serve the requested logical Model.

State transitions: candidate list is partitioned so tenant-owned Provider Accounts are evaluated before platform-managed Provider Accounts, unless a stronger admin policy forbids the tenant-owned candidate. No Quota is spent yet; a preflight cost estimate is attached.

Concurrency interaction: two requests using the same Provider Account must share rate and concurrency counters. HUAKAI should reserve per-Provider Account concurrency before the upstream call, not only after selection.

Critic C-001 and F-002 are confirmed: upstream docs explicitly prioritize user-owned keys before managed credits; HUAKAI must add tenant policy gates before that priority is allowed to execute.

### S-3: Platform-managed Provider Account fallback

Trigger condition: tenant-owned candidates are absent, exhausted, not authorized for the request class, or fail with a retryable class, and tenant policy permits managed Provider Account use.

State transitions: candidate list expands to include platform-managed Provider Accounts; routing reason records "managed fallback considered" with the policy id that allowed it. Usage Record must later record whether managed fallback was used.

Concurrency interaction: multiple tenants can contend for the same platform-managed Provider Account pool. SaaS Edition must enforce tenant quotas before shared Provider Account capacity is consumed.

Critic S-002 and N-006 are confirmed: HUAKAI must not expose "no rate limits" behavior. Provider routing can reduce upstream rate-limit pain but never bypasses Quota or Billing Ledger policy.

### S-4: Provider lock

Trigger condition: request hint or Route policy requires exactly one Provider or one tenant deployment.

State transitions: candidate list is reduced to the locked Provider or deployment. Failover policy is marked disabled or compliance-limited. If no candidate survives, the request fails closed before any upstream call.

Concurrency interaction: simultaneous locked requests can hot-spot one Provider Account. HUAKAI must still apply Provider Account concurrency limits, even when failover is disabled.

Critic C-003 and D-003 are confirmed: upstream docs state locking to one Provider means no automatic failover, which contradicts broad "always succeeds" language. HUAKAI must make that conditional in API and UI surfaces.

### S-5: Manual fallback chain

Trigger condition: request hint or Route policy provides an ordered list of desired candidates.

State transitions: candidate list becomes an ordered policy object. Each attempt appends an attempt record to the request-local decision trace and eventually to the Usage Record. Terminal failure records the final attempted candidate and why remaining candidates were skipped.

Concurrency interaction: two requests with the same chain must not share mutable per-request attempt state. Shared health state may affect both, but the order itself is request-local.

Critic F-003 is confirmed: a manual chain is not just a list. HUAKAI must define idempotency, streaming retry, tool-call side effects, tenant budget exhaustion, and compliance override behavior.

### S-6: Provider exclusion

Trigger condition: request hint excludes one or more Providers, or an admin/tenant policy denies Providers for a User Group, data class, region, or compliance reason.

State transitions: candidate list removes excluded Providers and records the exclusion source. Admin deny rules must be stronger than request hints. If every candidate is removed, fail closed with an operator-visible reason.

Concurrency interaction: exclusion evaluation is pure per request, but policy changes during evaluation require snapshot semantics. Usage Record should carry policy version to explain why two concurrent requests differed.

Critic C-003, N-002, and S-004 are confirmed: exclusion conflicts create compliance and leakage risk. HUAKAI must never let a user request hint weaken an admin deny rule.

### S-7: Unknown logical Model handling

Trigger condition: requested logical Model is absent from the Model Registry snapshot.

State transitions: automatic platform-managed candidate discovery is disabled. Only tenant-owned deployments explicitly configured for that tenant and request class may remain. If none exists, fail closed before upstream call.

Concurrency interaction: a registry reload can make a previously unknown Model known. HUAKAI must use the same snapshot for discovery and audit within one request, even if another request starts after the reload.

Critic C-004 is confirmed: upstream docs state unknown models route only through user-owned deployments. HUAKAI should preserve this as a strict fail-closed boundary.

### S-8: Estimated effective-cost ranking

Trigger condition: candidate list has more than one eligible candidate after policy filtering.

State transitions: each candidate receives an estimated effective cost for the request shape. Inputs include prompt token estimate, requested max completion tokens if present, modality, cache eligibility, batch mode, tenant contract override, Provider region, Provider Account tier, currency conversion version, and quota headroom.

Concurrency interaction: price catalog updates can race with request evaluation. HUAKAI should snapshot price version at selection time and reconcile actual cost after response.

Critic C-005, F-004, and N-004 are confirmed: static advertised price is not enough. Equal public price does not mean equal tenant cost.

### S-9: Equal-cost distribution

Trigger condition: two or more candidates have the same effective-cost rank after rounding rules.

State transitions: the request-local candidate order is resolved by a deterministic tie policy, such as tenant-scoped weighted rotation using shared state. The decision trace records tie group size and tie-break reason.

Concurrency interaction: per-process rotation will diverge across gateway replicas. SaaS Edition should store tie-break counters in PostgreSQL or a shared coordination layer; Personal Edition may use in-process state with a warning.

Critic C-001 and C-007 are confirmed: upstream docs claim equal-cost candidates are load balanced, and workspace source regions show dedicated weighted and latency routing components. HUAKAI must make state ownership explicit.

### S-10: Tier-driven weight vectors

Trigger condition: tenant policy or Route policy defines different weights by User Group, Provider Account tier, Provider reliability tier, edition, or request class.

State transitions: candidate score is adjusted by a typed vector. The vector is not a model-string grammar; it is a persisted Route policy with policy version, owner, and audit history.

Concurrency interaction: policy edits during traffic require atomic versioning. Requests already evaluated use their captured version; new requests use the new version after commit.

Critic N-001 and N-007 are confirmed: HUAKAI should not copy compact model-string grammar as the source of truth. Request hints can be parsed into typed policy input, but persisted policy wins.

### S-11: Retryable failover after upstream response

Trigger condition: first upstream attempt returns a retryable Provider response or timeout before a response is committed to the client.

State transitions: attempt record is marked failed with failure class; Provider Account health may be degraded; candidate list advances to the next eligible candidate; Quota reservation remains held until final settlement.

Concurrency interaction: many failures can stampede the next Provider Account. HUAKAI needs cooldown and concurrency gates shared across replicas, plus rate-limited operator signals.

Critic C-002 is confirmed but refined: upstream docs list 429, 401, 400 context length, 408, and 500+ as failover triggers. HUAKAI must split these into retryable capacity failure, retryable timeout, terminal credential failure, terminal client/request failure, and context-window mismatch.

### S-12: Credential failure fail-closed

Trigger condition: upstream returns an authentication-like failure for a tenant-owned Provider Account, or local credential validation says the Provider Account is expired, disabled, or under investigation.

State transitions: Provider Account health changes to credential-suspect or disabled-pending-review; request fails closed unless tenant policy explicitly allows substitution with another tenant-authorized Provider Account. Audit Event is created for automatic state change if HUAKAI mutates Provider Account status.

Concurrency interaction: concurrent requests might all discover the bad credential. Use a compare-and-set style status transition and suppress duplicate alerts.

Critic D-004, N-003, and S-001 are confirmed: blindly failing over on 401 can hide a tenant credential problem and move data outside the intended account boundary.

### S-13: Context-window mismatch handling

Trigger condition: upstream returns a context-length failure or local estimator predicts the chosen Provider cannot accept the request.

State transitions: if another candidate has a larger verified context window and is tenant-authorized, the request may advance; otherwise terminal client error is returned with no cross-boundary fallback. Usage Record records estimated tokens, chosen candidate context limit, and failure reason.

Concurrency interaction: no shared mutation required except optional health signal. Do not degrade Provider health for deterministic oversized requests.

Critic D-005 is confirmed: 400 context-length failover only helps when candidate context windows differ and the router knows them.

### S-14: Streaming response boundary

Trigger condition: request asks for streaming response and upstream attempt has begun.

State transitions: before the first client-visible content event, retry may be allowed if idempotency policy permits. After client-visible content, failover is disabled by default; Usage Record marks partial stream failure and Billing Ledger/Quota reconciliation uses actual tokens known plus Provider-reported metadata if available.

Concurrency interaction: retry state is per request, but streaming failures can affect shared health. Health degradation must not double-charge or double-release Quota.

Critic C-005 and F-003 are confirmed: cost is uncertain until completion, and streaming partial output changes retry obligations.

### S-15: Post-call actual-cost reconciliation

Trigger condition: upstream attempt completes successfully or terminally with usage metadata.

State transitions: Usage Record is finalized with selected Provider Account, Channel, Route, estimated cost snapshot, actual usage, actual cost, reconciliation delta, and routing reason. Quota reservation is reconciled. Billing Ledger entry is appended if the request is billable.

Concurrency interaction: Quota reservation and settlement must be atomic per Usage Record. Reconciliation may run asynchronously but must be idempotent.

Critic C-005 is confirmed: selection uses deterministic estimated cost, while actual cost must be reconciled after the call.

### S-16: Degraded registry, price, or health data

Trigger condition: Model Registry, price catalog, Provider health feed, or Provider Account status data is stale or unavailable.

State transitions: router enters degraded mode for affected tenants or Providers. Candidate ranking can fall back to last-known-good snapshots only if policy permits. Decision trace records stale data age and fallback source.

Concurrency interaction: degraded state must be shared in SaaS Edition. One gateway replica must not silently choose a different policy because its local cache is older.

Critic C-006 and S-003 are confirmed: operators need a visible explanation when a non-cheapest Provider wins or when routing data is stale.

### 2-bis. Three request lifecycles

Happy-path lifecycle: request enters with API Key and logical Model; User and User Group resolve; Quota is reserved; Route policy is loaded by tenant and version; candidate discovery finds tenant-owned and managed candidates; tenant-owned candidates are evaluated first; effective cost ranking selects the cheapest eligible Provider Account; tie-break, if any, is recorded; upstream call succeeds; Usage Record is finalized with estimated and actual cost; Quota is reconciled; Billing Ledger is appended if applicable; response returns with no upstream credential exposure.

Partial-failure lifecycle: request enters and selects a tenant-owned Provider Account; upstream returns a retryable rate-limit or timeout before any streaming content is sent; the attempt is marked failed; Provider Account health/cooldown is updated; Quota reservation remains active; next tenant-authorized candidate is selected by the remaining chain; second attempt succeeds; Usage Record records both attempts, final Provider Account, failover reason, estimated-vs-actual cost delta, and survivor state. Operator sees a degraded Provider Account signal, not a silent success.

Full-failure lifecycle: request enters with a Provider lock or unknown Model. Candidate discovery finds no tenant-authorized Provider Account, or the only locked Provider Account fails with terminal credential or compliance class. No managed pool fallback is allowed. Gateway returns a fail-closed error; Usage Record is created with terminal routing status if the request passed auth; Quota reservation is released; no Billing Ledger charge is appended except any configured minimum failed-request fee; Audit Event is created only if Provider Account state changed.

## 3. INPUTS (signals consumed, state mutated)

### Per-Request fields read and written

Read: tenant id, request id, API Key id, User id, User Group id, logical Model, request routing hint string if present, gateway endpoint, message shape, requested maximum output tokens, streaming flag, tool-call or side-effect indicator, headers used for properties or session, client timeout, request body byte size, estimated prompt tokens, modality, cache directive, and idempotency key if present.

Written: normalized routing attributes, parsed request hint, Route policy version, Model Registry version, candidate list, candidate exclusion reasons, estimated cost snapshot, selected Channel, selected Provider Account, attempt list, failover class, final status, actual token usage, actual cost, Quota reservation id, Usage Record id, Billing Ledger reference, and operator-visible routing reason.

### Per-Account / per-Channel state read and mutated

Read: Provider Account lifecycle state, upstream credential availability, Provider slug, region, tenant ownership, tier, balance metadata, daily limit, account-specific rate limits, account-specific concurrency limit, Channel enabled/paused/degraded status, Channel model allow-list, Channel mapping to upstream model names, Channel fallback rules, and Channel cost override.

Mutated: per-Provider Account concurrency reservation, health status, cooldown marker, credential-suspect marker, last failure class, last success timestamp, rolling error counters, and optional balance/quota exhaustion status. Channel state changes should only occur through operator action or controlled health automation with Audit Event.

Lifetime: Provider Account and Channel records are durable in SaaS Edition; Personal Edition may load local configuration at startup but still needs an auditable local decision log.

### Per-Tenant isolation boundaries

Every candidate must be scoped to the tenant before ranking. A tenant can never inherit another tenant's Provider Account because it is cheaper or healthier. Request hints are tenant-local suggestions, not global authority. Platform-managed Provider Accounts are eligible only when the tenant policy explicitly permits them for the Model, User Group, data class, and region.

The tenant boundary is crossed only by shared platform-managed capacity, and even then the Usage Record, Quota, Billing Ledger, and audit trail remain tenant-specific.

### Per-Process state

The feature can touch in-memory caches for Model Registry snapshots, price catalog snapshots, Provider health snapshots, parsed policy cache, and local tie-break counters. It can also use goroutine-local or task-local request routing context, upstream attempt context, timeout state, and streaming state.

HUAKAI risk: in SaaS Edition, any in-memory health, latency, cooldown, or equal-cost rotation state that affects routing must be either externalized or clearly marked local-degraded. DR-001 and DR-006 push shared decision state toward PostgreSQL or a shared coordination service.

### Persistent state and transaction boundaries

Persistent tables expected in HUAKAI: tenants, Users, User Groups, API Keys, Provider Accounts, Channels, Routes, Model Registry entries, price catalog snapshots, tenant policy, Provider health snapshots, Quota reservations, Usage Records, Billing Ledger, Audit Events, route decision logs, and policy reload records.

Indexes expected: tenant id plus logical Model for route lookup; tenant id plus Provider Account state for candidate lookup; Route policy version for audit; Usage Record request id unique index; Provider Account health by tenant/provider/status; Quota reservation by request id; Billing Ledger by Usage Record id; Audit Event by target id and timestamp.

Transaction boundary: Quota reservation and request admission must commit before upstream spend. Usage Record creation should occur before first upstream attempt when feasible. Final Usage Record, Quota reconciliation, and Billing Ledger append must be idempotent and tied to request id. Provider Account health mutation can be separate but must carry the same request id.

## 4. FAILURE MODES HANDLED

1. Rate-limit failure. Trigger: upstream returns capacity/rate failure or local Provider Account rate gate rejects. Observable outcome: failover to next tenant-authorized candidate if response not committed. Operator signal: Provider Account cooldown and failover counter. Recovery: wait for refill or increase account capacity. Blast radius: single Provider Account, potentially single tenant if tenant-owned.

2. Authentication failure. Trigger: upstream rejects credential or local Provider Account credential is expired/disabled. Observable outcome: fail closed by default. Operator signal: credential-suspect Provider Account status and alert. Recovery: rotate upstream credential or re-enable after verification. Blast radius: single Provider Account; can affect tenant if only candidate.

3. Context-window failure. Trigger: request exceeds selected Provider context limit. Observable outcome: failover only to verified larger-context candidate; otherwise terminal request error. Operator signal: routing log says context-window mismatch, not Provider outage. Recovery: choose larger Model, trim prompt, or configure deployment. Blast radius: single request pattern or Model.

4. Timeout failure. Trigger: upstream does not respond before policy timeout. Observable outcome: retry next candidate before streaming content. Operator signal: latency/timeout health degradation. Recovery: cooldown, adjust timeout, or remove slow Provider Account. Blast radius: Provider Account or Provider region.

5. Server failure. Trigger: upstream 5xx or connection failure. Observable outcome: failover according to chain and policy. Operator signal: rolling error rate and route decision logs. Recovery: automatic cooldown, operator pause, or Provider recovery. Blast radius: Provider Account, Provider, or region.

6. All candidates excluded. Trigger: request hint plus admin policy removes every candidate. Observable outcome: fail closed before upstream call. Operator signal: no-eligible-candidate reason with exclusion sources. Recovery: edit Route policy or request hint; admin deny remains stronger. Blast radius: single request or tenant policy.

7. Unknown Model without tenant deployment. Trigger: Model absent from registry and no tenant-owned deployment configured. Observable outcome: fail closed. Operator signal: unknown-model no-deployment decision. Recovery: add Model Registry entry or tenant deployment. Blast radius: single Model/tenant.

8. Stale price or registry data. Trigger: price catalog or Model Registry snapshot age exceeds tenant policy. Observable outcome: degraded routing or fail closed depending on policy. Operator signal: stale-snapshot warning with age/version. Recovery: refresh catalog, rollback to known-good snapshot, or enable temporary policy. Blast radius: single process if local cache; cluster-wide if central data broken.

9. Streaming partial failure. Trigger: upstream fails after client-visible stream content. Observable outcome: no automatic failover by default; client receives stream error and Usage Record marks partial. Operator signal: partial-stream failure count. Recovery: client retry with idempotency/context handling; Provider health review. Blast radius: single request, with possible Provider health impact.

10. Billing/Quota reconciliation failure. Trigger: upstream succeeds but local settlement write fails. Observable outcome: response may already be delivered; reconciliation job must retry. Operator signal: unsettled Usage Record queue. Recovery: idempotent replay using request id. Blast radius: single request or tenant if database incident.

## 5. FAILURE MODES NOT HANDLED (gaps)

The upstream public docs do not define tenant isolation, so HUAKAI must add it.

The upstream public docs do not define how equal-cost load balancing is coordinated across replicas.

The upstream public docs do not define whether a 401 from a tenant-owned Provider Account should disable that Provider Account or failover.

The upstream public docs do not define how streaming partial responses interact with manual fallback chains.

The upstream public docs do not define a conflict resolver for Provider lock plus Provider exclusion.

The upstream public docs do not define typed parser behavior for whitespace, duplicate candidates, unknown tokens, maliciously long strings, or tenant-specific aliases.

The upstream public docs do not define actual-cost reconciliation against an earlier estimated-cost selection.

The upstream public docs do not define operator override semantics for health thresholds, cooldowns, or stale price catalogs.

The upstream source regions visible in browser confirm workspace routing components and Provider catalogs, but not a stable PostgreSQL-backed policy model. HUAKAI must design its own durable model under DR-006.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- KEEP: preserve automatic candidate discovery for a logical Model where the tenant policy permits it.
- KEEP: preserve tenant-owned Provider Account priority before platform-managed fallback, because it supports tenant credits and direct Provider relationships.
- KEEP: preserve manual fallback chains as a user outcome, but represent them as typed Route policy.
- KEEP: preserve Provider lock for compliance and testing, with explicit no-failover semantics.
- KEEP: preserve unknown Model pass-through only for tenant-owned configured deployments.
- KEEP: preserve cost-optimized selection, equal-cost distribution, and post-error fallback as operator-visible routing outcomes.

- IMPROVE: rank by effective cost, not static public price.
- IMPROVE: require deterministic estimated-cost selection plus post-call actual-cost reconciliation.
- IMPROVE: split error taxonomy into retryable, terminal client, terminal credential, compliance, capacity, and settlement classes.
- IMPROVE: write the full routing reason into Usage Records and route decision logs.
- IMPROVE: make all Route, Channel, Provider Account, health, and price inputs tenant-scoped and policy-versioned.
- IMPROVE: persist or externalize health, cooldown, and equal-cost load-balance state in SaaS Edition.
- IMPROVE: define Personal Edition safe defaults separately from SaaS Edition durable policy controls under DR-002.

- AVOID: do not copy the compact upstream model-string grammar as HUAKAI's authoritative policy model.
- AVOID: do not let request-level hints override admin or tenant compliance policy.
- AVOID: do not fail over on Provider Account authentication failure by default.
- AVOID: do not fall back from unknown Model to a platform-managed Provider Account.
- AVOID: do not claim "no rate limits"; preserve Quota, Billing Ledger, and Provider Account limits.
- AVOID: do not use float money math; DR-006 and project rules require PostgreSQL numeric-style settlement semantics.
- AVOID: do not copy upstream source names, structures, file paths, comments, or schema details.

HUAKAI-specific risk 1: DR-001 multi-tenancy means automatic managed fallback can leak prompts across account, region, or contract boundaries if every candidate is not tenant-authorized before ranking.

HUAKAI-specific risk 2: DR-002 dual editions mean Personal Edition can tolerate local config and local rotation, but SaaS Edition needs durable policy versions, shared health state, and tenant audit.

HUAKAI-specific risk 3: DR-006 PostgreSQL means route policy, price snapshots, Usage Records, Billing Ledger entries, and Audit Events must be transactionally explainable; in-memory-only routing is insufficient.

HUAKAI-specific risk 4: blindly copying failover on 401 would hide broken tenant credentials and might charge a platform-managed Provider Account against the wrong policy.

HUAKAI-specific risk 5: blindly copying slash/comma/exclamation request grammar as policy would make the request body a compliance control surface, conflicting with admin-owned Route policy.

HUAKAI-specific risk 6: blindly copying "cheapest" based on public price would ignore enterprise contracts, regional SKUs, cache effects, and tenant budget state.

HUAKAI-specific risk 7: blindly copying broad "automatic failover" messaging would conflict with locked Provider, unknown Model, credential failure, and streaming partial-response boundaries.

## 7. ATTRIBUTION

- Source files read: public source and docs regions only; URLs redacted in this file per CL-002 because the repository UI shows GPL-3.0 while the visible manifest shows Apache-2.0.
- Specifier-lane session: Codex specifier-lane ROUND 2, 2026-04-29.
- Reviewer-lane session: pending.
- Verified clean-room compliance: behavior-only notes; no upstream function names, struct fields, package names, source file paths, schemas, comments, tests, or distinctive implementation layout transferred.
- CL-001: no upstream config object copied into HUAKAI spec.
- CL-002: source URLs and implementation regions are redacted as regions.
- CL-003: no upstream schema copied.
- CL-004: no upstream UI copied.
- CL-005: no line-by-line translation or pseudocode.
- CL-006: existing E-HLC anchors reused.
- CL-007: Option C carve-out respected for routing/failover/account-health behavior.
- CL-008: F-ROUTE-001 and F-CONFIG-001 cited.
- CL-009: ambiguous runtime internals marked as gaps or open questions.
- CL-010: implementer-lane should receive this HUAKAI behavior contract, not source links.

## 8. Open questions

1. Should HUAKAI allow request-level manual fallback hints at all in SaaS Edition, or only allow clients to select named admin-approved Route policies?

2. Should Provider Account authentication failure ever fall back automatically when both candidates are tenant-owned, same region, same data policy, and tenant marks them replaceable?

3. What is the minimum cost estimate precision before admission: prompt-only, prompt plus requested maximum output, or prompt plus tenant historical output estimate?

4. Should equal-cost load balancing use PostgreSQL counters, Redis-style shared counters, or deterministic hash over tenant/request id for L2?

5. Should Personal Edition store route decision logs in local files or embedded database until PostgreSQL is configured?

6. Which operator action owns price catalog refresh: scheduled import, manual approval, or hybrid with pending-diff review?

7. Does HUAKAI expose request hint parsing errors to clients as syntax errors, policy errors, or normalized no-eligible-route errors?

## 9. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending reviewer-lane |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Round-2 draft intentionally deep; requires independent clean-room review before implementer-lane use |

## 10. Source Coverage Proof

1. Public provider-routing documentation region: contributed candidate discovery, tenant-owned key priority, platform-managed fallback, cheapest-first selection, equal-cost load balancing, lock semantics, deployment semantics, manual chain semantics, exclusion syntax, unknown Model behavior, and documented failover triggers.

2. Public cost-tracking and optimization documentation region: contributed cost calculation distinction, Model Registry price comparison, automatic cost sorting, tenant-owned credit priority, and next-cheapest fallback behavior.

3. Repository README source region: contributed gateway positioning, smart provider selection claims, strategy families, rate limit and spending-control claims, config-as-code plus UI wizard claim, and self-hosted custom configuration examples. This corroborates E-HLC-001 and E-HLC-006 but is not treated as a full runtime contract.

4. Workspace manifest source region: contributed evidence that the source tree separates weighted routing, dynamic routing, and latency routing concerns, plus showed license metadata divergence and dependency choices including decimal money support. This supports critic C-007 and D-001 without transferring code design.

5. Embedded Provider/Model catalog source region: contributed evidence that the gateway has a built-in Provider-to-Model catalog with Provider base locations and Provider-specific model availability. This supports candidate discovery and unknown Model boundary analysis.

6. Public gateway overview documentation region: contributed unified API, fallback, cost optimization, load balancing, Provider switching, and observability context. This supports motivation but is weaker than the routing-specific docs.

7. Public organization/repository metadata region: contributed license drift evidence: repository listing shows GPL-3.0 while README/workspace text shows Apache-2.0. This is treated as clean-room risk, not product behavior.

## 11. Round-2 critic-finding addressed table

| Critic finding ID | This round's status | Where addressed in this file |
|---|---|---|
| C-001 | CONFIRMED | §2 S-1, S-2, S-3, S-9 |
| C-002 | CONFIRMED | §2 S-11, S-12, S-13; §4 |
| C-003 | CONFIRMED | §2 S-4, S-6; §5 |
| C-004 | CONFIRMED | §2 S-7; §4 |
| C-005 | CONFIRMED | §2 S-8, S-14, S-15; §3 |
| C-006 | CONFIRMED | §2 S-16; §4 |
| C-007 | CONFIRMED | §2 S-9; §3 Per-Process; §10 |
| C-008 | CONFIRMED | §5; §6 AVOID; §8 |
| F-001 | CONFIRMED | §1; §2 overview |
| F-002 | CONFIRMED | §1; §2 S-2, S-3 |
| F-003 | CONFIRMED | §2 S-5, S-14 |
| F-004 | CONFIRMED | §2 S-8; §6 |
| F-005 | CONFIRMED | §2 S-10; §6 AVOID |
| D-001 | CONFIRMED | §1; §7; §10 |
| D-002 | CONFIRMED | §1; §10 |
| D-003 | CONFIRMED | §2 S-4; §6 |
| D-004 | CONFIRMED | §2 S-12; §4 |
| D-005 | CONFIRMED | §2 S-13; §4 |
| N-001 | CONFIRMED | §2 S-10; §6 AVOID |
| N-002 | CONFIRMED | §2 S-6; §6 |
| N-003 | CONFIRMED | §2 S-12; §6 |
| N-004 | CONFIRMED | §2 S-8; §6 |
| N-005 | CONFIRMED | §2 S-9, S-16; §3 |
| N-006 | CONFIRMED | §2 S-3; §6 |
| N-007 | CONFIRMED | §2 S-10; §6 |
| S-001 | CONFIRMED | §2 S-11, S-12, S-13; §4 |
| S-002 | CONFIRMED | §2 S-3, S-7; §6 |
| S-003 | CONFIRMED | §2 S-16; §3 |
| S-004 | CONFIRMED | §2 S-6; §6 |
| S-005 | CONFIRMED | §5; §8 |
| S-006 | CONFIRMED | §1; §10 |

Owner 中文总结：本轮按 Round 2 深度重新拆解了 Helicone 成本感知路由、手动/自定义规则链、成本预估与 tier 权重向量，覆盖 16 个子行为、3 条请求生命周期、完整输入/状态面、10 类失败模式、HUAKAI 在 DR-001/DR-002/DR-006 下的 7 个特有风险，并把 critic 的 31 条 finding 全部逐项 CONFIRMED 后映射到正文位置；相对 round-1 浅版，关键差异是把“cheapest”拆成候选发现、租户自有 Provider Account 优先、平台托管回退、有效成本估算、等价成本分配、错误分类、未知 Model fail-closed、流式边界和事后结算；HUAKAI 应吸收的是可审计、租户隔离、PostgreSQL 持久化的行为合同，而不是上游紧凑字符串语法、宽泛 failover 语义或任何源码结构。
