# LiteLLM - Cooldown Handler and Retry Policy Hierarchy

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | LiteLLM, MIT, E-LIC-005 |
| Feature in HUAKAI matrix | F-CH-002 (L2) + F-GW-004 (L1) |
| Evidence ledger row | E-LM-DEEP-001 through E-LM-DEEP-008, E-LM-DEEP-014 |
| Specifier session | Codex specifier-lane Round 2 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | Official repository region: `<cooldown decision region>`; official repository region: `<cooldown state cache region>`; official repository region: `<retry policy resolution region>`; official repository region: `<gateway routing and fallback orchestration region>`; official repository region: `<fallback event region>`; official repository region: `<router type and policy model region>`; official docs region: `<routing and reliability docs>`; public issue regions: `<retry delay ignored>`, `<usage-route retry delay ignored>`, `<temporary provider outage mapping>` |

## 1. WHY (motivation / context)

This feature exists because a gateway that calls many Provider Accounts cannot treat every failure as either a final client error or a global outage.

One Provider Account can be exhausted while another is healthy.

One Channel can hit a Provider-side per-minute limit while the Route still has other capacity.

One error can be transient and worth retrying, while another proves the Channel is misconfigured and should be removed from selection.

The reference design tries to solve three pressures at once.

First, it keeps client requests from failing immediately when the selected Channel has a temporary problem.

Second, it keeps the gateway from repeatedly hammering a Channel that has already shown recent failure signals.

Third, it lets operators tune retry and cooldown behavior without changing application code.

The official reliability documentation says fallback happens after retry exhaustion for the original Model candidate set, and the routing documentation describes cooldown as a way to remove recently failing upstream targets from selection.

Source evidence shows the behavior is deeper than the documentation summary.

The critic's C-001 claim is corroborated by the source pattern at `<cooldown decision region>`: the decision uses Channel availability, HTTP-like status classification, per-error failure allowance, failure percentage for the current minute, traffic-volume floors, and a single-Channel protection path.

This confirms D-002 as well: the documentation phrase "failures in a minute" is incomplete, because the source also has immediate status-triggered cooldown and percentage-triggered cooldown.

The official issue history adds a second reason for this decomposition.

The retry delay path has had reported drift between promised behavior and observed behavior.

Issue evidence for C-003 and D-001 is corroborated by public reports where configured retry delay or backoff was not honored on some gateway paths.

HUAKAI must therefore specify retry semantics as a product contract, not as an informal best-effort setting.

HUAKAI has stricter local constraints than the reference.

DR-001 makes tenant isolation mandatory.

DR-002 requires Personal Edition and SaaS Edition behavior to be explicit.

DR-006 makes PostgreSQL the durability baseline for request, attempt, health, usage, and audit state.

For that reason, HUAKAI should keep the useful feature outcome while replacing ambiguous local-memory and callback-driven pieces with tenant-scoped durable event and health state.

This is not feature shrinkage.

It is a Safe Equivalent / Implemented Better path for F-CH-002 and F-GW-004.

## 2. WHAT (algorithm in HUAKAI vocabulary)

### S-1: Route candidate selection excludes cooled Channels

Trigger condition: a Gateway Request reaches Route matching and the requested Model maps to one or more Channels.

State transitions: the request reads Route rules, candidate Channels, Channel status, Provider Account availability, and active cooldown records; no cooldown state is written in this step.

Concurrency interaction: simultaneous requests may read the same active cooldown snapshot; if one request writes a cooldown while another is selecting, the second may still use the Channel unless selection and attempt creation are guarded by a fresh health check.

HUAKAI should record the selected Channel and Provider Account on a Gateway Attempt before the upstream call starts.

### S-2: Specific Channel request path

Trigger condition: a caller or internal fallback path requests a particular Channel rather than normal weighted Route selection.

State transitions: normal candidate scoring is reduced or bypassed; the request still reads Channel enabled state and should read health state.

Concurrency interaction: if an operator cools the Channel while a specific-Channel request is in flight, HUAKAI must decide whether the attempt may continue; the safer rule is that already-started attempts continue, but new specific-Channel attempts fail closed unless an audited emergency override exists.

The critic's C-008 claim is corroborated by the official fallback documentation and source pattern at `<gateway routing and fallback orchestration region>`: a convenience path can bypass normal cooldown checking for a specific target.

HUAKAI must not copy that default.

### S-3: Failure callback recovers Channel identity

Trigger condition: a Provider call fails after a Channel was selected.

State transitions: failure handling reads request metadata, selected Channel information, Provider Account information, exception class, status code, and any Provider delay hint; it then may write an Attempt outcome and may enqueue cooldown evaluation.

Concurrency interaction: if the failure path loses Channel identity, no precise health mutation can be made; simultaneous failures on other replicas can then continue to route to the same bad Provider Account.

The critic's C-005 claim is corroborated by the source pattern at `<gateway routing and callback metadata region>`: health mutation depends on recovered Channel identity, and missing identity suppresses cooldown rather than cooling an unknown target.

HUAKAI must make Channel identity mandatory on every Gateway Attempt.

### S-4: Cooldown disabled gate

Trigger condition: a failure reaches cooldown evaluation while operator policy disables automatic cooldown.

State transitions: an Attempt failure is still recorded; Channel health state is not changed by cooldown automation.

Concurrency interaction: all replicas must observe the same policy version; otherwise one process may cool the Channel while another does not.

HUAKAI should store policy version on the Attempt outcome so operators can explain why a failure did or did not change Channel health.

### S-5: Connection-error class skips cooldown

Trigger condition: failure text or normalized error class indicates a gateway-to-Provider connection problem rather than a Provider API rejection.

State transitions: Gateway Attempt status becomes failed or retryable; cooldown state is not written merely because the connection failed.

Concurrency interaction: during network incidents, many concurrent requests may fail; skipping immediate cooldown avoids blacklisting all Channels, but retry budgets and circuit breakers still need to prevent local amplification.

The critic's C-007 and N-004 claims are partly corroborated by `<cooldown decision region>` and public issue evidence: raw status and mapped exception class are used heavily, and temporary Provider errors can land in inconsistent classes.

HUAKAI must normalize Provider errors before both retry and cooldown decisions.

### S-6: Immediate cooldown on rate-limit status

Trigger condition: Provider response is classified as rate-limited and the Model group has more than one usable Channel.

State transitions: a cooldown record is inserted for the failing Channel or Provider Account, with reason, status class, first failure time, last failure time, and expiry.

Concurrency interaction: two requests can race to cool the same Channel; HUAKAI should make the write idempotent by `(tenant_id, channel_id, provider_account_id, model, reason_class)` and extend `last_seen` rather than create duplicate active states.

The source corroborates E-LM-DEEP-001 and E-LM-DEEP-004: rate-limit failures can remove a Channel from healthy selection and later requests choose remaining Channels.

### S-7: Failure-rate threshold with traffic-volume floor

Trigger condition: the Channel has failures and successes in the current short window, automatic failure-rate logic is active, and the failure percentage exceeds the configured threshold.

State transitions: per-Channel per-window counters are read; if the threshold is crossed, active cooldown state is written.

Concurrency interaction: if counters live only inside one process, two replicas each see half the traffic and may not cross the threshold; if counters are central but updated without atomicity, failure percentage can flap.

The critic's C-002 claim is corroborated by the source pattern at `<gateway initialization and local counter region>` plus `<cooldown decision region>`: some pre-cooldown failure counting is local to a router process even when a distributed cache exists for active cooldown state.

HUAKAI must use PostgreSQL or Redis-backed atomic windows for SaaS Edition.

### S-8: Single-Channel protection

Trigger condition: the requested Model has only one candidate Channel in its group and the failure pattern is percentage-based rather than a hard non-retryable condition.

State transitions: failure counters may update, but cooldown may be skipped to avoid self-inflicted total outage.

Concurrency interaction: many simultaneous failures can continue to hit the only Channel; HUAKAI needs a degraded-open mode with operator alerting rather than silently continuing forever.

The critic's C-001 and F-001 claims are confirmed: Model candidate-set cardinality changes the health action.

HUAKAI should expose this as "last Channel degraded" in Admin Ops.

### S-9: Per-error allowed-failure policy

Trigger condition: operator has configured a failure allowance by normalized error class.

State transitions: the relevant failure counter increments; if the count exceeds the class-specific allowance in its TTL window, cooldown becomes active.

Concurrency interaction: if this counter is local memory, alternating failures across replicas avoid the threshold; if central, concurrent increments must be atomic and tenant-scoped.

This confirms C-001 and N-001: a simple "failed N times" description misses the per-error policy branch and the local-memory risk.

HUAKAI must scope the allowance by tenant, Route, Channel, Provider Account, Model, and error class.

### S-10: Cooldown duration ladder

Trigger condition: the system decides to cool a Channel.

State transitions: cooldown expiry is calculated from the most specific available source: failure-policy override, Provider delay hint, Channel policy, Route policy, tenant policy, then platform default.

Concurrency interaction: if two failures with different suggested durations arrive concurrently, HUAKAI should keep the longer active expiry for hard errors and allow policy to define whether shorter rate-limit windows can reduce it.

The critic's C-004 and F-002 claims are confirmed by `<retry delay and cooldown duration regions>`: Provider hints and operator minimum waits participate in runtime behavior.

HUAKAI must write the selected duration source into the health record.

### S-11: Retry budget hierarchy

Trigger condition: an Attempt fails before the Gateway Request is settled and retry is eligible.

State transitions: the request reads per-request retry override, tenant policy, Route policy, Channel or Provider Account policy, global platform default, and exception-type retry policy; the effective remaining budget is decremented on each retry attempt.

Concurrency interaction: retry budget is request-local but its attempts mutate shared Channel health and Usage Records; concurrent retries from many requests must respect per-Provider Account concurrency and spend caps.

The critic's C-004 and F-003 claims are confirmed: the hierarchy is wider than platform default versus Model candidate-set policy.

HUAKAI should define one Gateway Request retry budget and one per-Channel attempt budget.

### S-12: Same-Route retry before cross-Route fallback

Trigger condition: a selected Channel fails but the original Route still has another eligible Channel for the same Model.

State transitions: the failed Attempt is recorded; the next Attempt selects a different healthy Channel when possible; fallback depth does not advance until same-Route retry budget is exhausted.

Concurrency interaction: the first failed Channel may be cooled while the second attempt starts; if the second also fails, both health records may update in one Gateway Request.

The official docs and source corroborate E-LM-DEEP-007: retry for the current Model candidate set is exhausted before fallback to another Model target.

HUAKAI must reflect this in operator-visible attempt timelines.

### S-13: Retry delay selection

Trigger condition: retry is eligible and the system must wait before another Attempt.

State transitions: the request reads Provider delay hint, configured minimum wait, exception class, and whether another healthy Channel is immediately available; it writes the next-attempt scheduled time if delayed.

Concurrency interaction: if many requests share the same retry-after timestamp, they can stampede; HUAKAI should jitter retry scheduling per tenant and Channel.

The critic's C-003, D-001, and F-002 claims are confirmed by docs and public issues: behavior is endpoint-path sensitive and has drifted.

HUAKAI must test retry delay on chat, streaming, embedding, pass-through, and usage-based routing paths.

### S-14: Fallback mutates the effective target

Trigger condition: retry budget is exhausted or the error class maps directly to a fallback category.

State transitions: the request changes effective Model or Route target, increments fallback depth, records the original failure reason, and starts a new Attempt sequence under the fallback target.

Concurrency interaction: fallback target health can change between fallback resolution and attempt start; selection must re-read health state.

The critic's C-004 and F-003 claims are confirmed by `<fallback event region>`: fallback changes the effective Model target and recursively invokes gateway handling with depth limits.

HUAKAI must define whether retry budget is consumed per original Gateway Request, per target Model, or per Channel.

### S-15: Cooldown recovery by expiry

Trigger condition: active cooldown TTL expires or the current time passes `expires_at`.

State transitions: the Channel leaves the active cooldown set; no proof of Provider recovery is required by the reference pattern.

Concurrency interaction: many replicas may reintroduce traffic at the same moment after expiry, causing re-flap.

The critic's C-006, D-003, F-005, and S-007 claims are confirmed: recovery is TTL expiry, not active health reconciliation.

HUAKAI should distinguish `expired_without_probe`, `probe_passed`, `manual_restored`, and `forced_enabled`.

### S-16: No healthy Channel outcome

Trigger condition: all candidate Channels are cooled, filtered by policy, over budget, or otherwise unavailable.

State transitions: the Gateway Request settles as failed; no new upstream Attempt is made; operator-visible error should include Route, Model, and health summary.

Concurrency interaction: simultaneous requests can all fail fast while health is active; retries should not spin if the reason is "no eligible Channel".

HUAKAI should return a stable gateway error class and avoid converting this into a Provider error.

## 2-bis. REQUEST LIFECYCLES

### Happy-path lifecycle

A Gateway Request enters with API Key, User, tenant, Model, payload, and optional retry override.

The gateway authenticates, reserves quota, matches a Route, reads active Channel health, and selects a healthy Channel.

It creates Attempt 1 with tenant, Route, Channel, Provider Account, trace id, policy version, and idempotency key.

The Provider call succeeds.

Success counters update for the Channel.

No cooldown is written.

The Usage Record is appended.

Quota is reconciled.

The response settles successfully.

Concurrent requests may update the same success window, so HUAKAI must use atomic counter increments or append-only attempt aggregation.

### Partial-failure lifecycle

Attempt 1 reaches a Provider Account and receives a rate-limit response.

The gateway records Attempt 1 as failed with normalized reason `provider_rate_limited`.

The cooldown evaluator sees the status class and writes active cooldown for that Channel.

Retry policy still has budget.

The gateway chooses a second healthy Channel in the same Route and starts Attempt 2 with the same Gateway Request id and idempotency key.

Attempt 2 succeeds.

The request settles successfully, but the timeline contains both the failed and successful attempts.

State that survives: Attempt 1 failure, active cooldown, Attempt 2 success, Usage Record tied to the successful Provider Account, and Audit or operator log event for the fallback/retry reason.

Concurrent requests that selected the first Channel before cooldown was written may still fail; later requests should avoid it.

### Full-failure lifecycle

The request enters and selects a Channel.

Attempt 1 fails with a non-retryable Provider authorization or missing-resource class.

Cooldown evaluation records the Channel as unhealthy because the error suggests misconfiguration rather than transient overload.

Retry policy either returns zero attempts for this error class or retry budget is exhausted.

Fallback is absent, exhausted, or blocked by health state.

The Gateway Request settles as failed.

Cleanup obligations: release or reconcile quota reservation, append failed Usage Record or failed request record according to HUAKAI accounting rules, keep the cooldown health state, emit operator-visible signal, and preserve full Attempt evidence.

No client-visible success is produced.

Concurrent requests must stop choosing the cooled Channel once the health write commits.

## 3. INPUTS (signals consumed, state mutated)

### Per-Request data

Fields read: tenant id, API Key id, User id, User Group id, requested Model, request payload, streaming flag, gateway endpoint type, request timeout, request retry override, client-supplied fallback candidates if allowed, idempotency key, trace id, tags, source IP, and headers used by Route matching.

Fields written: Gateway Request status, selected Route id, current fallback depth, effective Model after fallback, total retry attempts consumed, final error class, final response metadata, settlement timestamp, and quota reconciliation status.

Attempt fields read: previous Attempt outcomes, excluded Channels for this request, remaining retry budget, and scheduled retry time.

Attempt fields written: Attempt number, Channel id, Provider Account id, upstream model name, start time, end time, status, normalized error class, Provider status, Provider retry hint, response token counts, cost context, and whether this Attempt changed health state.

### Per-Account and per-Channel data

Provider Account state read: lifecycle state, upstream credential status, Provider type, balance or quota metadata, rate-limit policy, spend cap, concurrency cap, and last health state.

Provider Account state mutated: health snapshot, cooldown reason, failure counters, success counters, last failure time, last success time, and optional probe results.

Channel state read: enabled / paused / degraded status, Route eligibility, allowed Model list, Provider Account selection policy, Channel-level retry policy, Channel-level cooldown policy, and per-Channel traffic windows.

Channel state mutated: active cooldown state, degraded state, operator alert status, flapping counters, and recovery source.

Lifetime: Attempt records and Usage Records are durable; short-window counters may be materialized from events; active cooldown state must survive process restarts in SaaS Edition.

### Per-Tenant data

Isolation boundary: every health decision must include tenant id unless the Provider Account is explicitly platform-shared and policy says health is global.

Tenant policy read: retry maximum, fallback permission, emergency override permission, cooldown duration bounds, jitter configuration, Admin Ops visibility, and edition capability.

Tenant state mutated: tenant-scoped health record, tenant-scoped alert, tenant Usage Record, and tenant audit trail.

The critic's F-004 and S-006 claims are confirmed as HUAKAI risks: if health is keyed only by public model or Channel name, one tenant's Provider failure can poison another tenant's routing.

### Per-Process data

Read: local routing cache, local client cache, in-memory counters if present, callback registration state, scheduler state, and active request goroutine-local or task-local metadata.

Mutated: local success/failure windows, local cooldown read-through cache, in-flight request maps, and callback side effects.

Lifetime: process-local state disappears on restart and is invisible to other replicas.

HUAKAI may use per-process caches only as acceleration, never as authoritative health or billing state.

This addresses critic S-001 and S-002: local cooldown or global callback mutation is unsafe as the sole health pipeline.

### Persistent data

Tables required by HUAKAI design: Gateway Requests, Gateway Attempts, Usage Records, Provider Accounts, Channels, Routes, Channel Health Events, Channel Health Snapshots, Retry Policies, Cooldown Policies, Audit Events, and optional Probe Results.

Indexes required: active health by `(tenant_id, channel_id, provider_account_id, model, reason_class, expires_at)`, attempts by `(tenant_id, request_id, attempt_number)`, attempts by `(tenant_id, channel_id, started_at)`, and idempotency by `(tenant_id, idempotency_key)`.

Transaction boundaries: quota reservation happens before upstream spend; Attempt start is inserted before Provider call; Attempt settlement and health event append happen after outcome; Usage Record and quota reconciliation happen in one explicit settlement path.

DR-006 requires PostgreSQL and explicit transaction design for these records.

## 4. FAILURE MODES HANDLED

### FM-1: Provider rate limit

Trigger: Provider returns a rate-limit class or equivalent normalized reason.

Observable outcome: current Attempt fails; retry may move to another Channel; failing Channel may enter cooldown.

Operator-visible signal: Channel health event with reason `provider_rate_limited`, retry hint if present, and affected tenant / Route / Model.

Recovery action: wait for cooldown expiry, probe, or operator restore.

Blast radius: usually single Provider Account or Channel; can be tenant-wide if the Provider Account is shared.

### FM-2: Provider authentication or credential failure

Trigger: Provider rejects upstream credential or account authorization.

Observable outcome: cooldown or disable candidate; retry should avoid same Provider Account.

Operator-visible signal: urgent Provider Account alert, credential status degraded.

Recovery action: rotate credential, refresh OAuth, or disable account.

Blast radius: single Provider Account; potentially many tenants if shared.

### FM-3: Provider missing model or invalid mapping

Trigger: Provider indicates the upstream Model mapping or target is not found.

Observable outcome: retry should not repeat the same mapping; Channel should be marked misconfigured.

Operator-visible signal: configuration error tied to Channel and Model Registry mapping.

Recovery action: fix mapping or remove Channel from Route.

Blast radius: Channel and Model combination.

### FM-4: Timeout before response

Trigger: request exceeds gateway or Provider timeout before any final response.

Observable outcome: retry may be allowed depending on idempotency and endpoint type.

Operator-visible signal: Attempt timeout metric with Provider Account and latency bucket.

Recovery action: tune timeout, route away, or investigate Provider latency.

Blast radius: single request through cluster-wide if Provider is degraded.

### FM-5: Connection error

Trigger: gateway cannot connect, DNS fails, TLS fails, or network resets before Provider classification.

Observable outcome: reference skips cooldown for recognized connection class; HUAKAI should retry cautiously and record network failure.

Operator-visible signal: network error class separated from Provider rejection.

Recovery action: inspect egress, DNS, proxy, regional network, or Provider endpoint.

Blast radius: single process, node, region, or cluster depending on network scope.

### FM-6: All Channels cooled or filtered

Trigger: every candidate Channel is cooled, over budget, disabled, or blocked by pre-call policy.

Observable outcome: Gateway Request fails without upstream call.

Operator-visible signal: Route has zero healthy candidates, with cooldown list and expiry summary.

Recovery action: add Channel capacity, restore Channel, lower traffic, or adjust policy.

Blast radius: Route / tenant / Model.

### FM-7: Retry delay ignored on a path

Trigger: endpoint path or routing strategy bypasses configured delay or Provider retry hint.

Observable outcome: immediate repeated attempts, fast exhaustion, possible Provider pressure amplification.

Operator-visible signal: attempts have near-zero spacing despite nonzero policy.

Recovery action: enforce shared retry scheduler and add acceptance tests for each endpoint path.

Blast radius: process-wide or tenant-wide under load.

### FM-8: Missing Channel identity in failure path

Trigger: callback or streaming path loses selected Channel metadata.

Observable outcome: request fails but no cooldown or health mutation occurs.

Operator-visible signal: Attempt failure with null Channel should be impossible; emit invariant violation.

Recovery action: make Attempt id mandatory in Provider call context.

Blast radius: single request, but repeated on an endpoint class.

### FM-9: Streaming failure after partial output

Trigger: Provider stream fails after bytes have been sent to client.

Observable outcome: normal retry may be unsafe because the client already observed partial content.

Operator-visible signal: partial stream failure with emitted-byte marker and no automatic reroute unless protocol supports resume.

Recovery action: settle as partial failure, record partial usage, avoid double billing.

Blast radius: single request.

### FM-10: Distributed cache outage

Trigger: shared cache used for active cooldown reads is unavailable.

Observable outcome: reference may fall back to local state or fail inconsistently; HUAKAI must choose fail-open or fail-closed by edition and policy.

Operator-visible signal: health-state backend degraded alert.

Recovery action: restore Redis/PostgreSQL, degrade to conservative per-process caps, or pause risky Routes.

Blast radius: single process to cluster-wide.

## 5. FAILURE MODES NOT HANDLED (gaps)

The reference does not provide tenant-scoped health as a first-class invariant.

That confirms N-001 and S-006: local or target-only health keys are insufficient for HUAKAI DR-001.

The reference does not prove recovery before reintroducing traffic.

That confirms N-005 and S-007: TTL expiry is not health proof.

The reference does not make retry delay uniform across all endpoint paths.

That confirms D-001 and C-003.

The reference does not fully isolate retry policy layers from fallback mutation.

That confirms F-003: after fallback changes the target, the effective policy can be hard to predict.

The reference exposes a convenience path where a specific target can skip cooldown filtering.

That confirms D-005 and S-005.

The reference relies on callbacks and metadata recovery for health mutation.

That confirms N-006 and C-005: missing metadata can suppress cooldown.

The reference has status-code and exception-class drift.

That confirms D-004 and issue evidence for temporary Provider outage classification.

The reference does not solve double accounting for failed / retried / fallback attempts.

HUAKAI must bind all Attempts to one idempotent Gateway Request settlement path.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- **KEEP**: Keep automatic exclusion of unhealthy Channels from normal Route selection.
- **KEEP**: Keep a traffic-volume floor before percentage-based cooldown so low-traffic Channels do not flap after one failure.
- **KEEP**: Keep exception-type retry policy as an operator control, but express it in HUAKAI normalized error classes.
- **KEEP**: Keep fallback after same-Route retry exhaustion, because clients expect transparent recovery when another target can serve the request.
- **KEEP**: Keep Provider retry hints as one input to delay selection.

- **IMPROVE**: DR-001 requires health keys to include tenant id, Channel id, Provider Account id, Model, and reason class.
- **IMPROVE**: DR-002 requires Personal Edition to work with local single-process defaults while SaaS Edition uses shared durable health and alerting.
- **IMPROVE**: DR-006 requires Gateway Attempts, health events, Usage Records, and Audit Events to be PostgreSQL-backed, append-oriented, and queryable.
- **IMPROVE**: Replace status-code-only logic with Provider-normalized classes: rate limited, quota exhausted, auth invalid, transient upstream, context too large, safety blocked, tenant budget blocked, gateway overload, and network failure.
- **IMPROVE**: Add recovery source: expired without probe, probe passed, manual restored, forced enabled.
- **IMPROVE**: Add jittered retry and cooldown reentry to prevent stampedes.
- **IMPROVE**: Make every retry and fallback reason visible in Admin Ops and on internal request traces.
- **IMPROVE**: Require acceptance tests for two replicas alternating failures against the same Channel.

- **AVOID**: Do not copy local in-memory failure counters as authoritative health gates.
- **AVOID**: Do not copy implicit specific-Channel cooldown bypass; use audited emergency override only.
- **AVOID**: Do not copy mixed SDK/proxy retry semantics; HUAKAI needs one Gateway Request retry contract.
- **AVOID**: Do not copy TTL expiry as proof of recovery.
- **AVOID**: Do not copy global callback mutation as the health pipeline.
- **AVOID**: Do not copy one cooldown constant for every Provider and error class.
- **AVOID**: Do not let one tenant poison another tenant's Provider Account health unless platform policy explicitly defines shared health.

HUAKAI-specific risk 1: blindly copying target-only cooldown keys would violate DR-001 because tenant A could remove tenant B's capacity.

HUAKAI-specific risk 2: blindly copying local counters would pass in Personal Edition but fail in SaaS Edition under two gateway replicas.

HUAKAI-specific risk 3: blindly copying callback-driven health mutation would make DR-006 PostgreSQL audit and replay impossible.

HUAKAI-specific risk 4: blindly copying status-code taxonomy would misclassify Provider-specific overload, quota, and safety responses.

HUAKAI-specific risk 5: blindly copying specific target bypass would let an operator or client route into known-bad capacity without an Audit Event.

HUAKAI-specific risk 6: blindly copying TTL recovery would show a Channel as available without proof, creating repeated flapping and confusing Admin Ops.

HUAKAI-specific risk 7: blindly copying retry policy precedence would make dual-edition behavior hard to document and test.

## 7. ATTRIBUTION

- Source files read: official public repository and official documentation regions listed in §10 with redacted source-region names per CL-002 / CL-005.
- Specifier-lane session: Codex specifier-lane Round 2, 2026-04-29.
- Reviewer-lane session: pending.
- Verified clean-room compliance: no source code, function names, struct field names, upstream file paths, package names, comments, schemas, or line-by-line translations are included.
- License posture: LiteLLM is anchored at E-LIC-005 as MIT; this decomposition remains behavior-only and does not read enterprise-only code.

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Round 2 specifier output; must receive independent reviewer-lane approval before Released status. |

## 8. OPEN QUESTIONS

Should HUAKAI count retry budget per original Gateway Request, per target Model after fallback, or per Channel?

Recommended answer: one total Gateway Request budget plus per-Channel attempt caps.

Should Personal Edition require Redis for health state?

Recommended answer: no, but mark local health as single-process only; SaaS Edition must use shared state.

Should cooldown ever fail open when health backend is unavailable?

Recommended answer: Personal Edition may fail open with warnings; SaaS Edition should use policy-specific degraded mode and hard alerts.

Should streaming partial-output failures retry?

Recommended answer: no automatic retry after bytes have reached the client unless a future resumable protocol exists.

Should an operator be able to force a cooled Channel?

Recommended answer: yes only through audited emergency override with expiry and reason.

## 9. IMPLEMENTATION NOTES FOR HUAKAI

Implement Gateway Attempt as the unit that binds retry, fallback, health mutation, usage, and billing.

Every Attempt must carry tenant id, request id, attempt number, Route id, Channel id, Provider Account id, Model, normalized error class, and policy version.

Implement health state as derived from append-only Attempt outcomes plus explicit operator actions.

Cache active cooldowns for fast routing, but rebuild them from durable state.

Treat Provider delay hints as advisory input bounded by tenant and Route policy.

Log retry decisions even when no retry occurs, because "zero retry due to policy" is operational evidence.

Expose Admin Ops views for active cooldowns, upcoming expiry, recovery source, last failure, and affected tenants.

Acceptance tests should cover normal retry, no retry for non-retryable class, fallback after retry exhaustion, same-Route retry before fallback, two-replica counters, Redis outage, streaming partial failure, specific-Channel emergency override, TTL expiry without probe, and probe-based restore.

This decomposition maps F-CH-002 to Implemented Better and F-GW-004 to Implemented / Implemented Better depending on slice scope.

No feature is dropped.

Risky upstream behavior is converted into Safe Equivalent, Feature Flag, or audited Manual First behavior.

## 10. Source Coverage Proof

| Source region read | What it contributed |
| --- | --- |
| Official repository URL redacted as `<cooldown decision region>` | Confirmed cooldown decision branches: disabled gate, missing identity gate, connection-error exclusion, rate-limit immediate cooldown, failure-rate threshold, traffic-volume floor, single-Channel protection, and per-error allowed-failure branch. |
| Official repository URL redacted as `<cooldown state cache region>` | Confirmed active cooldown is stored with expiry-oriented state and queried to filter Channel candidates; recovery is expiry-based rather than probe-confirmed. |
| Official repository URL redacted as `<retry policy resolution region>` | Confirmed retry counts can be selected by exception class and Model candidate-set policy rather than only a single global retry number. |
| Official repository URL redacted as `<gateway routing and fallback orchestration region>` | Confirmed gateway initialization has global retry count, request-compatible overrides, retry delay setting, cooldown settings, fallback depth, and distributed/local cache split. |
| Official repository URL redacted as `<fallback event region>` | Confirmed fallback mutates the effective target, tracks depth, logs success/failure events, and invokes gateway handling recursively. |
| Official repository URL redacted as `<policy surface region>` | Confirmed policy surfaces for allowed failures and retry counts by exception family, and confirmed no-Channel error carries cooldown-related context. |
| Official docs URL redacted as `<routing and reliability docs>` | Confirmed documented behavior that fallback follows retry exhaustion and that fallback order is operator-configured. |
| Public issue URL redacted as `<retry delay ignored>` | Confirmed operational drift where configured delay was reported as not honored. |
| Public issue URL redacted as `<usage-route retry delay ignored>` | Confirmed endpoint/routing-strategy sensitivity for retry delay. |
| Public issue URL redacted as `<temporary provider outage mapping>` | Confirmed error taxonomy drift where a temporary Provider outage can be mapped into a non-fallback path. |

## 11. Round-2 critic-finding addressed table

| Critic finding ID | This round's status | Where addressed in this file |
|---|---|---|
| C-001 | CONFIRMED | §1, §2 S-7 through S-9 |
| C-002 | CONFIRMED | §2 S-7, §3 Per-Process |
| C-003 | CONFIRMED | §1, §2 S-13, §4 FM-7 |
| C-004 | CONFIRMED | §2 S-11 through S-14 |
| C-005 | CONFIRMED | §2 S-3, §4 FM-8 |
| C-006 | CONFIRMED | §2 S-15, §5 |
| C-007 | CONFIRMED | §2 S-5, §5, §6 |
| C-008 | CONFIRMED | §2 S-2, §6 |
| F-001 | CONFIRMED | §2 S-8, §6 |
| F-002 | CONFIRMED | §2 S-10, §2 S-13 |
| F-003 | CONFIRMED | §2 S-11, §2 S-14, §8 |
| F-004 | CONFIRMED | §3 Per-Tenant, §6 |
| F-005 | CONFIRMED | §2 S-15, §6 |
| D-001 | CONFIRMED | §1, §2 S-13, §4 FM-7 |
| D-002 | CONFIRMED | §1, §2 S-6 through S-9 |
| D-003 | CONFIRMED | §2 S-15, §5 |
| D-004 | CONFIRMED | §5, §6 |
| D-005 | CONFIRMED | §2 S-2, §6 |
| N-001 | CONFIRMED | §2 S-9, §5, §6 |
| N-002 | CONFIRMED | §2 S-2, §6 |
| N-003 | CONFIRMED | §2 S-11, §6 |
| N-004 | CONFIRMED | §2 S-5, §6 |
| N-005 | CONFIRMED | §2 S-15, §6 |
| N-006 | CONFIRMED | §2 S-3, §5, §6 |
| N-007 | CONFIRMED | §2 S-10, §6 |
| S-001 | CONFIRMED | §3 Per-Process, §4 FM-10 |
| S-002 | CONFIRMED | §3 Per-Process, §5 |
| S-003 | CONFIRMED | §2 S-5, §5 |
| S-004 | CONFIRMED | §2 S-10, §6 |
| S-005 | CONFIRMED | §2 S-2, §6 |
| S-006 | CONFIRMED | §3 Per-Tenant, §6 |
| S-007 | CONFIRMED | §2 S-15, §6 |

中文总结：本轮按 Round 2 要求把 LiteLLM 的 cooldown handler 与 retry policy hierarchy 拆到请求生命周期、16 个子行为、完整输入状态、10 类失败模式、HUAKAI 风险和 source coverage proof；critic 的 32 条编号 finding 全部在文内处置，状态均为 CONFIRMED，没有发现可从已读源证据反驳的条目。相比 round-1 浅版，本文件不再把 cooldown 简化成失败计数，也不把 retry 简化成全局配置，而是明确了失败率阈值、流量地板、单 Channel 保护、连接错误跳过、TTL 恢复、fallback 目标突变、特定 Channel 绕过、跨进程 split-brain、Provider 错误归一化和 PostgreSQL 持久化要求。HUAKAI 应吸收的是“自动隔离坏 Channel + 分层 retry/fallback”的能力，避免照搬本地计数、隐式绕过、TTL 当恢复证明、callback 当健康流水线，以及未按 tenant/account/channel 分层的状态模型。
