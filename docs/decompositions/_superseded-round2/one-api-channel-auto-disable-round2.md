# one-api - Channel auto-disable on permanent-error pattern

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | one-api, MIT, E-LIC-004 |
| Feature in HUAKAI matrix | F-CH-002 (L2) |
| Evidence ledger row | E-OAI-009, E-OAI-013, E-OAI-DEEP-006, E-OAI-DEEP-010, E-OAI-DEEP-011, E-OAI-DEEP-012, E-OAI-DEEP-016 |
| Specifier session | Codex specifier-lane Round 2 |
| Specifier date | 2026-04-29 |
| Reviewer session | TBD, must be different from this specifier session |
| Reviewer date | TBD |
| Source files read | Public source regions redacted per clean-room rule: `<configuration defaults region>`, `<relay error and retry region>`, `<permanent-error classification region>`, `<rolling metric consumer region>`, `<Channel status mutation and notification region>`, `<manual and scheduled Channel test region>`, `<Channel availability/cache region>`, `<Channel balance probe region>`, `<operator option/UI setting region>`, `<public environment-option documentation region>` |

## 1. WHY (motivation / context)

This feature exists because a gateway that keeps routing traffic to a dead Provider Account or broken Channel turns one upstream fault into repeated User-visible failures, wasted retries, avoidable latency, and misleading operator dashboards. For a relay-station product, Channel health is not a passive metric; it is part of the routing control plane. Once the system has enough evidence that a Channel cannot serve requests, the Channel must be removed from selection quickly enough to protect Users and the operator's upstream inventory.

The source shows that the reference project solves this pressure with several overlapping mechanisms rather than one clean state machine. First, live relay failures can trigger immediate Channel disable when an operator option is enabled and the error looks permanent. Second, when live failures are not classified as permanent, they may be fed into a rolling success-rate window controlled by environment settings. Third, manual or scheduled Channel tests can disable a slow or failing Channel and may also re-enable a previously auto-disabled Channel after a clean test if another operator option is enabled. Fourth, a balance-check path can disable a Channel when an upstream balance probe confirms exhaustion. The critic's claim C-001 is therefore CONFIRMED from source: the permanent-error behavior is not always-on and not standalone; it is gated separately from metric-based success-rate disabling.

The real-world pressure behind this layering is understandable. Immediate permanent-error disable catches bad upstream credentials, exhausted account credit, revoked organization access, or permission loss without waiting for a metric window. Rolling success-rate disable catches noisy failures that do not match the narrow permanent classifier. Scheduled tests catch failures even during low traffic. Balance probes catch account exhaustion before the next relay request. The design pressure is sound, but the implementation shape is risky for HUAKAI because it mixes request-path side effects, process-local memory, asynchronous status mutation, cache freshness, and operator notification in one loosely ordered flow.

For HUAKAI, F-CH-002 must preserve the user outcome: unhealthy Channels stop receiving normal traffic, operators can see why, and recovery is possible. But HUAKAI must not copy the reference project's process-local health window, global blast radius, narrow error classifier, or alert-only evidence model. DR-001 requires tenant-aware isolation from day 1; DR-002 requires one codebase that can run as Personal Edition and later SaaS Edition; DR-006 requires PostgreSQL as the correctness boundary for health evidence, state transitions, and auditability. The HUAKAI version should therefore turn this feature from "best-effort disable on selected symptoms" into a durable, tenant-scoped Channel health state machine.

The critic's F-001 is CONFIRMED from the request-forwarding and health-control source regions: "disable on permanent error" is a decision chain, not a boolean. A request must be authenticated, matched to a User Group, routed to a Channel, sent upstream, normalized into a gateway error, checked against retry rules, optionally excluded from retry selection, asynchronously sent to the Channel-health path, and then converted into either immediate disable or metric event. The chain also depends on whether the caller forced a specific Channel. HUAKAI should model the whole chain because treating it as one boolean hides blast-radius and retry-accounting risks.

The critic's D-003 is CONFIRMED from public docs and source regions: operator-facing descriptions compress multiple disable paths into a simpler success-rate mental model, while source behavior includes immediate permanent-error disable, response-time disable during tests, rolling metric disable, and balance-exhaustion disable. HUAKAI should not repeat that drift. The Admin Ops UI and operator docs must name each disable source separately.

## 2. WHAT (algorithm in HUAKAI vocabulary)

The source behavior can be decomposed into the following sub-behaviors. The terms below use HUAKAI vocabulary: Channel, Provider Account, Route, User, User Group, Model, API Key, Usage Record, Audit Event, Quota, and Billing Ledger. Where the reference source uses a lower-level concept, this file describes the behavior at HUAKAI's domain boundary rather than naming upstream source artifacts.

### S-1. Route-time Channel choice

Trigger condition: an incoming client request has passed API Key auth, resolved a User and User Group, and names a Model without forcing a specific Channel.

State transitions: the gateway reads the User Group, Model, Channel availability, Channel priority, Channel model allow-list, and Channel credential metadata. It writes request-local selected Channel data into the request context. It does not yet mutate Channel health state.

Concurrency interaction: concurrent requests can select the same Channel from the same in-memory or database-backed eligible set. In the reference, if memory cache is enabled, that eligible set can remain stale until the next sync. If one concurrent request disables the Channel, another request may still select it before cache or database reads observe the new status. HUAKAI must make disabled or quarantined Channel state fail closed in routing.

### S-2. Forced Channel path

Trigger condition: an incoming request includes an authorized operator or caller hint that pins the request to one specific Channel.

State transitions: the gateway reads the requested Channel directly, rejects it if already disabled, and writes that Channel into request-local state. Normal random selection is bypassed.

Concurrency interaction: concurrent forced requests can all target the same Channel. The critic's C-004 is CONFIRMED from the forced-path and retry source regions: this path does not retry through the normal pool, but a failure can still enter the same shared Channel auto-disable path. HUAKAI must scope pinned-request fallout: a tenant's deliberate pin must not globally punish shared Channel capacity unless independent validation confirms a shared Provider Account fault.

### S-3. Successful live relay observation

Trigger condition: a live upstream Provider call completes successfully on the initially selected Channel.

State transitions: the request completes. If metric collection is enabled, the Channel receives one success observation in the process-local rolling window. No Channel status change occurs. Usage Record and Quota settlement belong to other decompositions, but this feature must receive the final Channel id and success/failure classification.

Concurrency interaction: concurrent successes append to the same Channel's success window through an event queue. In the reference, the event send is launched from a request goroutine. If the queue backs up, many request goroutines can block behind health tracking. HUAKAI should use bounded, non-blocking health-event ingestion with durable fallback accounting.

### S-4. Live relay failure classification

Trigger condition: a live upstream Provider call returns an error after normalization into a gateway error response.

State transitions: the request path records the failed Channel id and error. A separate health side effect is launched. The health side effect reads the operator auto-disable option, HTTP status, normalized error category, error code, and message text. It either writes a Channel disable transition or records a metric failure event.

Concurrency interaction: the reference launches this health side effect asynchronously while retry selection may continue. The critic's C-003 is CONFIRMED: status propagation is not serialized with retry selection. HUAKAI should write a retry-attempt health observation synchronously enough that the current request excludes the failed Channel immediately, while durable Channel status mutation can still be performed idempotently.

### S-5. Immediate permanent-error disable

Trigger condition: a live relay or test path sees a permanent-looking failure and the automatic disable option is enabled. Source evidence shows unauthorized status, a small OpenAI-shaped taxonomy, selected code values, and selected message substrings as disabling signals.

State transitions: Channel availability status changes from enabled to auto-disabled. The Channel's model availability is also marked unavailable. A system log and best-effort notification are emitted. Metric failure accounting is skipped for this event because the event is treated as immediately disabling.

Concurrency interaction: two concurrent permanent failures can attempt the same status change. The reference write is effectively last-write-wins and does not preserve a first-failure reason as an immutable transition. HUAKAI must make the transition idempotent with compare-and-set semantics and an Audit Event recording reason, actor/source, request id, tenant id, prior state, new state, and recovery policy.

### S-6. Narrow permanent-error taxonomy

Trigger condition: a failure is checked against the immediate disable classifier.

State transitions: if the error matches the narrow classifier, the Channel is disabled; otherwise the Channel receives a generic failure metric event.

Concurrency interaction: simultaneous failures of different classes can race: one request may classify as permanent and disable while another records a metric failure. The critic's C-002 is CONFIRMED from the classifier source region. Provider-specific quota, billing, policy, region, organization-blocked, malformed-auth, proxy-rewrite, and safety failures are not modeled as first-class categories; some only match if their text contains broad substrings, and some fall through. HUAKAI must define normalized categories: permanent-auth, credential-expired, quota-exhausted, billing-blocked, policy-blocked, region-blocked, model-permission-denied, tenant-quota-denied, transient-rate-limit, transient-server, timeout, transport, malformed-provider-response, and unknown.

### S-7. Retry decision and failed-Channel exclusion

Trigger condition: a live relay failure occurs and retry count is greater than zero, with the request not using the forced Channel path.

State transitions: the current request records the last failed Channel id and attempts to pick another eligible Channel. If the same Channel is selected again, the retry iteration skips it. The request body is rewound before resending.

Concurrency interaction: this exclusion is request-local. Other requests can still select the failed Channel. If cache is stale, even the same retry loop may pick from a set that still includes a just-disabled Channel, though the loop tries to skip only the immediately failed id. The critic's F-001 and S-007 are CONFIRMED: retry, async disable, cache refresh, and metric emission are not one ordered state machine. HUAKAI must connect retry attempt records with Channel health observations so retry recovery does not corrupt the Channel-health signal.

### S-8. Rolling success-rate accounting

Trigger condition: metric collection is enabled and a live relay event reaches the health metric path as success or non-permanent failure.

State transitions: the process-local rolling window for the Channel appends a boolean success/failure marker. If the window is not yet full, no disable occurs. Once full, the process computes recent success rate; if below threshold, it clears that Channel's in-memory window and triggers a Channel auto-disable.

Concurrency interaction: concurrent event sends enter bounded queues. The source uses one consumer per success/failure queue and one shared map of Channel windows, so consumer serialization reduces direct map races, but the queue send itself is launched from request goroutines and can block. The critic's C-006, C-007, F-002, S-001, and S-004 are CONFIRMED: the window is volatile, per process, defaults to a small sample size, and uses bounded queues that are not an operator-grade backpressure policy. HUAKAI must store health observations durably in PostgreSQL and aggregate by tenant, Channel, Provider Account, Model, source, and time window.

### S-9. Metric-based disable

Trigger condition: the rolling metric window reaches its configured size and the success rate is below threshold.

State transitions: Channel availability status changes to auto-disabled; model availability for that Channel is disabled; system log and best-effort operator notification are emitted. The source clears the in-memory sample window after deciding to disable.

Concurrency interaction: metric-based disable can race with immediate permanent-error disable, scheduled-test disable, manual operator disable, or auto-enable. The reference does not preserve reason precedence. HUAKAI must define transition precedence: manual disable beats automatic enable; provider-account quarantine beats Channel-local degradation; tenant-scoped disable does not imply global disable; global provider quarantine removes all dependent Channels until cleared.

### S-10. Scheduled test disable for slow response

Trigger condition: manual all-Channel test or scheduled background test runs; a currently enabled Channel responds slower than the configured response-time threshold; automatic disable is enabled.

State transitions: Channel response time and test timestamp are updated. If the response is too slow, Channel status changes to auto-disabled and model availability is disabled. If automatic disable is off, the source sends a notification without disabling.

Concurrency interaction: the all-Channel test has a process-local non-overlap guard, but it does not prevent live traffic from using a Channel while the test is running. In a multi-process deployment, each process has its own test loop and guard. HUAKAI should coordinate scheduled tests through PostgreSQL leases or job rows, not process-local booleans.

### S-11. Scheduled test disable for permanent-looking error

Trigger condition: manual or scheduled Channel test returns a provider-shaped error that matches the same permanent-error classifier while the Channel is enabled.

State transitions: Channel status changes to auto-disabled. Response time/test timestamp may be updated. A test log is written asynchronously. A best-effort notification may be emitted.

Concurrency interaction: because test logs are asynchronous and status changes are independent writes, a crash can leave a status transition without matching diagnostic evidence. HUAKAI must write health observation, transition, and Audit Event in one PostgreSQL transaction, then send notifications from an outbox.

### S-12. Scheduled test auto-enable

Trigger condition: manual or scheduled Channel test runs against a Channel that is not enabled; automatic enable option is enabled; the test sees no transport error and no provider error.

State transitions: Channel status changes to enabled; model availability is enabled; system log and best-effort notification are emitted. The reference treats one clean test as enough for re-enable.

Concurrency interaction: a concurrent live request may be disabling the same Channel while the scheduled test re-enables it. The critic's C-009, F-004, N-006, and S-008 are CONFIRMED: scheduled tests and live failures share status writes but not identical inputs, and one clean generic test can re-enable a Provider Account that still fails for a tenant, model, region, or billing state. HUAKAI should require cooldown, reason-specific probes, model-specific validation, and possibly operator confirmation before shared Channel resume.

### S-13. Balance-probe disable

Trigger condition: an operator or scheduled balance update checks enabled Channels for selected Provider types and confirms balance at or below zero.

State transitions: Provider balance fields are updated; if confirmed exhausted, Channel status changes to auto-disabled and availability is removed.

Concurrency interaction: a live request can be routed while the balance probe is running. If the probe disables the Channel after selection, the already-selected request may still spend or fail. HUAKAI should distinguish Provider Account balance state from Channel health state and fail closed at Provider Account acquisition time when the balance state is exhausted.

### S-14. Cache and availability sync

Trigger condition: memory cache is enabled, process starts, or periodic sync runs.

State transitions: a process-local map of eligible Channels by User Group and Model is rebuilt from persistent Channel rows and model availability rows. Channel status writes update the model availability table immediately, but the process-local selection map changes only on sync.

Concurrency interaction: the critic's C-008, F-003, D-002, N-007, and S-007 are CONFIRMED: status update, model availability update, cache rebuild, and selection are separate. In a multi-node deployment, one process can disable while another keeps routing from stale memory. HUAKAI must use database-visible Channel state at selection time or a short-lived cache with explicit invalidation and a fail-closed verification before Provider Account acquisition.

### S-15. Best-effort operator alert

Trigger condition: Channel is disabled, enabled, or all-Channel testing completes.

State transitions: source logs a system message and attempts message-pusher delivery, then email delivery to a root operator address. If delivery fails, the state change still stands.

Concurrency interaction: concurrent disables can send duplicate alerts. Alert failure does not roll back status mutation. The critic's C-010, N-005, S-002, and S-006 are CONFIRMED: alert delivery is not durable operator evidence, and root-global notification can leak Channel identifiers and reasons in a multi-tenant product. HUAKAI must record Audit Events and Ops incidents durably, then send tenant-scoped notifications through a retryable outbox.

## 2-bis. Three request lifecycles

### Happy-path lifecycle

The request enters the gateway with a valid API Key and a Model. The gateway resolves User, User Group, and Route, then selects an enabled Channel whose allowed Model set includes the requested Model. The selected Channel writes request-local routing metadata. The upstream Provider call succeeds. The response returns to the client. The health subsystem receives one success observation if metric collection is enabled. No Channel status changes. The Usage Record and Quota settlement are handled by the billing path, but this feature requires the Usage Record to include the Channel and Provider Account identifiers so later health analytics can join request outcome to Channel health. In HUAKAI, this success should append a durable Channel health observation with tenant id, Channel id, Provider Account id, Model, request id, attempt index, status class, latency, and source `live_relay`.

### Partial-failure lifecycle

The request enters normally and the first selected Channel fails with a rate-limit, server error, or non-permanent provider error. The request-local retry policy allows retry because the caller did not force a Channel. The first failed Channel is recorded as the last failed candidate and a health side effect is emitted. The retry loop selects a different eligible Channel and resends the same logical request. The second Channel succeeds, so the client sees success. In the reference, the first Channel may receive a failure metric event, but the successful retry does not clearly credit the second Channel through the same outer success path once the retry succeeds. The critic's C-005 is CONFIRMED from the relay/metric source pattern: retry can skew accounting because a failed first attempt is counted, while fallback success accounting is weaker and can under-credit recovery. HUAKAI must record every attempt explicitly: failed attempt on Channel A, successful attempt on Channel B, one client-visible successful request, and one Billing Ledger settlement. Channel health should be attempt-based; User billing should be request-idempotent.

### Full-failure lifecycle

The request enters normally, selects a Channel, and receives a permanent-looking error such as bad credential, disabled organization, exhausted upstream credit, or permission denial. Automatic disable is enabled. The health side effect disables the Channel and sends best-effort operator notification. If retry is not allowed, or retry candidates fail as well, the client receives an error with a request id. If retry is allowed but every candidate fails, each failed Channel may produce health side effects. State that survives includes Channel status changes, model availability changes, logs, metric observations, and possibly notifications. State that may be missing in the reference includes durable transition reason, tenant scope, retry attempt correlation, and alert delivery status. HUAKAI cleanup obligations: release or reconcile Quota reservation; append a failed Usage Record or attempt record; write Channel health observations; write Audit Events for automatic status transitions; invalidate routing cache; create Ops incidents; and ensure no shared Channel is globally disabled from a tenant-scoped forced request without validation.

## 3. INPUTS (signals consumed, state mutated)

### Per-request

Fields read: API Key identity, resolved User, User Group, requested Model, request body, request mode, forced Channel hint if present, retry count configuration, request id, current selected Channel, Provider response status, normalized provider error category, provider error code, provider error message, upstream latency, and whether the client-visible request has already produced a final response.

Fields written: request-local selected Channel id/name/provider type, original Model, actual upstream Model mapping, last failed Channel id, retry attempt index, health observation source, client-visible error message with request id, and in HUAKAI the retry-attempt row that links all attempts under one idempotency key.

Concurrency notes: request-local fields must not be mutated by asynchronous health workers after the response path reads them. The source itself contains a race-condition note around relay error mutation after retry; the critic's D-005 is CONFIRMED. HUAKAI must treat request error state as immutable after each attempt and pass copies into health processing.

### Per-Channel / Provider Account

State read: Channel status, Provider Account credential availability, Channel allowed Models, Model mapping, Channel group eligibility, priority, base Provider endpoint, Channel configuration, last response time, last test time, balance, and availability rows used by Route selection.

State mutated: Channel status, model availability for that Channel, response time, test timestamp, balance, used quota outside this feature, health observations, transition history, Ops incident state, notification outbox rows, and manual override state.

Lifetime: Channel configuration is long-lived operator data. Health observations are time-windowed but must be retained long enough for audit and trend analysis. Disable transitions are durable Audit Events. In HUAKAI, Provider Account state is the smallest routable upstream unit; Channel state may pool Provider Accounts, so disabling a Channel must not hide which Provider Account actually failed.

### Per-tenant

Isolation boundaries: every Channel health observation, transition, alert, Ops incident, and manual override must carry tenant id per DR-001. The default Personal Edition tenant can be implicit in UI, but not absent from schema. A tenant-owned Channel can be disabled for that tenant. A shared Provider Account or shared Channel requires scoped policy: tenant-local quarantine, global quarantine, or operator-confirmed global disable.

The critic's N-002 and N-008 are CONFIRMED as HUAKAI-specific risks. Blindly copying global Channel disable and direct pin behavior would let one tenant's request or one pinned diagnostic path remove shared capacity for other tenants. HUAKAI must separate tenant-scoped disable from global Provider Account quarantine.

### Per-process

State read/mutated in the reference: process-local metric success/failure queues, process-local rolling health map, process-local memory cache of eligible Channels by User Group and Model, process-local all-Channel-test running flag, goroutine-local copies of Channel id/reason, and cached root notification target.

HUAKAI posture: these may exist only as performance hints. They cannot be the authority for health state, tenant isolation, or audit. Authoritative health transitions belong in PostgreSQL per DR-006, and background work must use leases or outbox rows so multi-process deployments behave consistently.

### Persistent

Tables and indexes touched by feature-equivalent behavior in HUAKAI: Channel table, Provider Account table, Channel-to-Model availability table, Route eligibility table, Channel health observation table, Channel health transition table, Audit Event table, Ops incident table, notification outbox table, Usage Record table for request/attempt join, Quota reservation/settlement tables indirectly, and Billing Ledger indirectly when a failed request must be reconciled.

Transaction boundaries: HUAKAI must write a health observation and automatic transition in one explicit transaction. A transition must be compare-and-set from expected prior states to avoid auto-enable overwriting manual disable. Cache invalidation should be emitted from the same transaction through an outbox or version counter. Notification delivery must happen after commit. Usage and Billing settlement remain their own money-grade transaction, linked by request id and attempt id rather than bundled with notification.

Persistent indexes required: `(tenant_id, channel_id, observed_at)`, `(tenant_id, provider_account_id, observed_at)`, `(tenant_id, model, observed_at)`, `(tenant_id, channel_id, current_state)`, unique transition idempotency key on `(tenant_id, channel_id, source, request_id, attempt_index, reason_class)`, and partial indexes for unresolved Ops incidents. These are HUAKAI design requirements, not upstream schema copies.

## 4. FAILURE MODES HANDLED

1. Bad upstream credential. Trigger: unauthorized or provider error indicating authentication failure. Observable outcome: Channel auto-disabled if option enabled; otherwise failure may be counted by metrics. Operator-visible signal: log and best-effort alert. Recovery: rotate credential and run validated probe. Blast radius in source: global Channel; HUAKAI target: Provider Account or tenant-scoped Channel.

2. Upstream quota or credit exhaustion. Trigger: provider error category/message or balance probe indicates no available credit. Observable outcome: Channel disabled or metric failure. Operator-visible signal: log, alert, balance field update. Recovery: recharge Provider Account, then model-specific probe and operator resume. Blast radius: single Provider Account, but source may disable whole Channel.

3. Permission or organization restriction. Trigger: provider response indicates permission denied, disabled organization, restricted organization, or model access denial. Observable outcome: immediate disable if matched. Operator-visible signal: reason text in alert/log. Recovery: fix upstream organization/model permission; re-probe affected Models. Blast radius: may be model-specific, but source disables Channel. HUAKAI should narrow to Model/Provider Account when possible.

4. Transient rate limit. Trigger: upstream too-many-requests response. Observable outcome: retry may occur; failure metric may record if not permanent. Operator-visible signal: request error and metric window. Recovery: wait, lower concurrency, or route elsewhere. Blast radius: Provider Account or tenant traffic burst. HUAKAI should not immediately hard-disable without rate-limit category and reset window.

5. Upstream server error. Trigger: upstream 5xx. Observable outcome: retry may occur; failure metric may record. Operator-visible signal: request logs and health metrics. Recovery: retry/failover; monitor provider incident. Blast radius: Provider or region; HUAKAI should quarantine by Provider region when evidence supports it.

6. Slow Channel test response. Trigger: scheduled/manual test latency exceeds configured threshold. Observable outcome: disable if auto-disable option is on; otherwise notification only. Operator-visible signal: response time/test log/alert. Recovery: investigate Provider latency, network path, model-specific load. Blast radius: single Channel in source; HUAKAI should label degraded before disabled when low confidence.

7. Malformed or unparsable test response. Trigger: test path cannot parse expected response shape or sees missing usage. Observable outcome: test failure; possible disable only if classified or latency threshold triggers. Operator-visible signal: test log and error message. Recovery: classify as provider-protocol drift, proxy HTML block, or gateway adaptor bug. Blast radius: Channel or Provider adaptor. HUAKAI should make `malformed_provider_response` a first-class category.

8. Low rolling success rate. Trigger: full metric window and success rate below threshold. Observable outcome: Channel auto-disabled. Operator-visible signal: metric-derived disable log/alert. Recovery: inspect recent observations; require cooldown/probe/manual confirmation. Blast radius: process-local in source, cluster-aware in HUAKAI.

9. Notification delivery failure. Trigger: message pusher and/or email delivery fails. Observable outcome: Channel state remains changed. Operator-visible signal: system error log only. Recovery: retry notification from durable outbox in HUAKAI. Blast radius: operator visibility, not traffic path; source makes it single-process/log dependent.

10. Cache staleness after disable. Trigger: Channel status is changed while route cache still contains old eligibility. Observable outcome: requests may still pick disabled Channel until sync or database read catches up. Operator-visible signal: repeated failures after disable. Recovery: invalidate cache or verify current state before Provider Account acquisition. Blast radius: single process or cluster-wide depending cache topology.

## 5. FAILURE MODES NOT HANDLED (gaps)

The source does not make Channel health a durable cluster-consistent state machine. Metric windows are process-local; scheduled-test locks are process-local; cache sync is periodic; notification is best effort; and state transitions are not idempotent audit records. This is the critic's C-006, C-007, C-008, C-010, F-003, S-001, S-002, S-007, and S-008 family, all CONFIRMED.

The source does not distinguish tenant-local harm from global Channel harm. A forced Channel request can disable a shared Channel. A global root alert can expose Channel names and failure reasons without tenant scoping. This is unacceptable under DR-001 and confirms C-004, N-002, N-008, and S-006.

The source does not define retry-aware metric accounting. A client-visible success after fallback can still leave the first Channel with a failure observation, while the fallback Channel's success may not be symmetrically recorded in the same relay flow. HUAKAI must separate attempt health from request outcome.

The source does not preserve reason precedence. Manual disable, automatic permanent-error disable, metric disable, scheduled-test disable, balance disable, and auto-enable all write similar status changes without a durable transition graph. HUAKAI must define precedence and recovery rules.

The source does not provide a provider-normalized error taxonomy. It mixes status, provider error fields, and broad message matching. Some broad substrings can over-disable; some provider-specific permanent conditions can under-disable. HUAKAI should make taxonomy data-driven and provider-versioned.

The source does not make auto-recovery safe enough for shared infrastructure. One clean generic test can re-enable a Channel even if the real failure is tenant-specific, model-specific, organization-specific, or region-specific.

The source does not guarantee a health transition has a matching test log or alert. Multiple asynchronous writes can split apart under crash or process shutdown. HUAKAI must make the transition/audit record authoritative.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- **KEEP**: Remove clearly broken Channels from normal routing quickly enough that Users are not repeatedly sent to known-bad upstream capacity.
- **KEEP**: Separate immediate permanent-looking failures from rolling low-success-rate failures, because they represent different confidence levels and recovery paths.
- **KEEP**: Allow scheduled and manual Channel tests, because low-traffic Channels still need health evidence.
- **KEEP**: Include response latency in Channel health, because slow Channels can be operationally dead even when they return HTTP success.
- **KEEP**: Notify operators when automatic state changes occur, but treat alert delivery as secondary to durable audit.

- **IMPROVE**: Replace process-local rolling windows with PostgreSQL-backed health observations aggregated by tenant, Channel, Provider Account, Model, source, and time window.
- **IMPROVE**: Use a provider-normalized error taxonomy instead of narrow string matching; categories must include auth, credential expiry, quota, billing, policy, permission, region, model access, rate limit, timeout, transport, malformed response, and unknown.
- **IMPROVE**: Scope transitions by tenant and ownership; tenant-local failures create tenant-local quarantine unless shared Provider Account evidence independently justifies global quarantine.
- **IMPROVE**: Make automatic disable an idempotent compare-and-set transition with reason class, evidence id, actor/source, request id, attempt id, cooldown, and recovery requirements.
- **IMPROVE**: Make retry health accounting attempt-based, while Billing Ledger and client-visible outcome remain request-idempotent.
- **IMPROVE**: Route selection must fail closed against disabled/quarantined Channel state with cache invalidation or final database verification.
- **IMPROVE**: Scheduled tests must use PostgreSQL leases and outbox jobs so multi-process Personal Edition and future SaaS Edition behave consistently.
- **IMPROVE**: Auto-enable must require reason-specific probes, cooldown, and manual approval for shared or high-blast-radius Channels.

- **AVOID**: Do not copy in-memory rolling health windows; this directly violates DR-006's posture that correctness-critical state belongs in PostgreSQL.
- **AVOID**: Do not copy global disable for all tenants; this conflicts with DR-001.
- **AVOID**: Do not copy immediate re-enable after one generic test; it is unsafe for shared Provider Accounts.
- **AVOID**: Do not copy fire-and-forget status mutation without Audit Events and transition history.
- **AVOID**: Do not copy alert delivery as the only operator evidence.
- **AVOID**: Do not copy broad message-substring policy as the permanent-error boundary.
- **AVOID**: Do not copy stale-cache assumptions after disable; fail closed.
- **AVOID**: Do not expose tenant/provider failure details in root-global notifications in SaaS Edition.

HUAKAI-specific risk 1: DR-001 makes tenant isolation mandatory. A global Channel status write based on one tenant's forced request would create cross-tenant denial of service.

HUAKAI-specific risk 2: DR-002 Personal Edition is commercially usable, not a toy. Silent auto-disable without durable Ops incidents can take the Owner's paid API business offline without an accountable recovery trail.

HUAKAI-specific risk 3: DR-002 SaaS Edition later hosts other operators. Root-global notification text containing Channel names, Provider Account clues, or failure reasons can leak one tenant's upstream inventory to another operations context.

HUAKAI-specific risk 4: DR-006 PostgreSQL is chosen specifically to avoid correctness gaps in quota, billing, and concurrent state. Copying process-local metric memory would introduce a second, weaker authority beside PostgreSQL.

HUAKAI-specific risk 5: DR-006 enables row locks, idempotency keys, and outbox patterns. Blindly copying fire-and-forget goroutines would squander the chosen database guarantees and make cluster behavior nondeterministic.

HUAKAI-specific risk 6: dual editions require configuration hygiene. Personal Edition can default to a single tenant, but the schema and health events must still carry tenant id so SaaS activation is not a global migration.

HUAKAI-specific risk 7: HUAKAI's Provider Account is the smallest upstream routable unit. Copying Channel-wide disable may hide account-level failure and remove healthy accounts pooled under the same Channel.

## 7. ATTRIBUTION

- Source files read: one-api public source regions listed in the field table and in Section 10, commit `8df4a2670b98266bd287c698243fff327d9748cf`.
- Specifier-lane session: Codex specifier-lane Round 2, 2026-04-29.
- Reviewer-lane session: TBD.
- Verified clean-room compliance: MIT reference E-LIC-004 is safe to read; no upstream function names, struct field names, package names, file paths, distinctive source layout, or line-by-line translated code are used in this decomposition. Behavior is described in HUAKAI vocabulary.

## 8. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | TBD |
| Review date | TBD |
| Checks passed | TBD |
| Notes | Round 2 source-verified draft; requires reviewer-lane clean-room and coverage review before status can move to Reviewed. |

## 9. Implementation acceptance notes for HUAKAI

Acceptance tests should cover: permanent-auth disable, quota-exhaustion disable, policy/permission quarantine, transient rate-limit not hard-disabled on one event, rolling success-rate disable with durable observations, retry attempt accounting, forced Channel tenant-scoped fallout, scheduled-test disable, scheduled-test auto-enable blocked by cooldown/manual requirement, stale-cache fail-closed, notification outbox retry, and multi-process lease behavior.

The minimum L2 implementation should create the data model and state machine even if the first release only has a default tenant and a small provider taxonomy. The Owner's directive asks for deep decomposition, not shallow parity. Therefore implementer-lane should prefer a smaller number of provider categories implemented correctly over a broad string list implemented as unreviewable ad hoc matching.

No feature is dropped: immediate disable is preserved, metric disable is preserved, scheduled/manual tests are preserved, balance-derived disable is preserved, and auto-recovery is preserved as a safer operator-confirmed or policy-gated workflow. The divergence is method and safety boundary, not functionality reduction.

## 10. Source Coverage Proof

1. `<configuration defaults region>` contributed: automatic disable default is off; automatic enable default is off; retry count default is separately configured; response-time threshold exists; metric enablement and metric window settings are environment-driven. This corroborates C-001 and D-001.

2. `<relay error and retry region>` contributed: live relay success emits a success metric on the first-attempt happy path; failures launch health processing asynchronously; retry skips forced Channel requests; retry can continue while health status mutation happens elsewhere; retry has request-local failed-Channel exclusion. This corroborates C-003, C-004, C-005, F-001, and D-005.

3. `<permanent-error classification region>` contributed: immediate disable is gated by operator option, unauthorized status, a small provider-shaped taxonomy, selected code values, and selected message substrings. This corroborates C-002, N-003, S-003, and S-005.

4. `<rolling metric consumer region>` contributed: success/failure observations are process-local, queue-fed, window-based, threshold-driven, and disabled Channels are triggered after the window fills below threshold. This corroborates C-006, C-007, F-002, S-001, and S-004.

5. `<Channel status mutation and notification region>` contributed: disable/enable writes Channel status and model availability separately from notification; notification failure logs but does not roll back status. This corroborates C-008, C-010, N-004, N-005, and S-008.

6. `<manual and scheduled Channel test region>` contributed: tests build a provider request, update response time, use a process-local non-overlap guard, can disable slow or permanent-error Channels, and can enable disabled Channels after a clean test when configured. This corroborates C-009, F-004, N-006, and S-008.

7. `<Channel availability/cache region>` contributed: eligible Channel cache is rebuilt periodically from enabled Channels and model availability; selection can read memory cache or database depending configuration. This corroborates C-008, D-002, F-003, and N-007.

8. `<Channel balance probe region>` contributed: balance probing can disable enabled Channels when upstream balance is exhausted. This supports the conclusion that the feature has more than permanent-error and metric paths.

9. `<operator option/UI setting region>` contributed: automatic disable, automatic enable, response-time threshold, and retry count are operator-facing options, while metric env settings are separate. This corroborates D-001 and D-003.

10. `<public environment-option documentation region>` contributed: public docs describe scheduled Channel tests and success-rate metric disable, while source includes additional immediate disable and response-time paths. This corroborates D-001, D-002, D-003, and D-004's warning to cite behavior rather than upstream database guidance.

## 11. Round-2 critic-finding addressed table

| Critic finding ID | This round's status | Where addressed in this file |
|---|---|---|
| C-001 | CONFIRMED | §1, §2 S-5, §10.1 |
| C-002 | CONFIRMED | §2 S-6, §5, §10.3 |
| C-003 | CONFIRMED | §2 S-4, §2 S-7, §10.2 |
| C-004 | CONFIRMED | §2 S-2, §3 Per-tenant, §10.2 |
| C-005 | CONFIRMED | §2-bis Partial-failure lifecycle, §9 |
| C-006 | CONFIRMED | §2 S-8, §3 Per-process, §10.4 |
| C-007 | CONFIRMED | §2 S-8, §4 item 8, §10.4 |
| C-008 | CONFIRMED | §2 S-14, §4 item 10, §10.5 and §10.7 |
| C-009 | CONFIRMED | §2 S-12, §2-bis Full-failure lifecycle, §10.6 |
| C-010 | CONFIRMED | §2 S-15, §4 item 9, §10.5 |
| F-001 | CONFIRMED | §1, §2 S-4 through S-7 |
| F-002 | CONFIRMED | §2 S-8, §4 item 8 |
| F-003 | CONFIRMED | §2 S-14, §10.7 |
| F-004 | CONFIRMED | §2 S-12, §5 |
| F-005 | CONFIRMED | §2 S-5, §6 IMPROVE/AVOID |
| D-001 | CONFIRMED | §1, §10.1, §10.9 |
| D-002 | CONFIRMED | §2 S-14, §10.7 |
| D-003 | CONFIRMED | §1, §10.10 |
| D-004 | CONFIRMED as caution | §6 HUAKAI risk 4, §10.10 |
| D-005 | CONFIRMED | §3 Per-request, §10.2 |
| N-001 | CONFIRMED | §6 AVOID, §3 Persistent |
| N-002 | CONFIRMED | §3 Per-tenant, §6 HUAKAI risk 1 |
| N-003 | CONFIRMED | §2 S-6, §6 IMPROVE |
| N-004 | CONFIRMED | §2 S-5, §6 IMPROVE |
| N-005 | CONFIRMED | §2 S-15, §6 AVOID |
| N-006 | CONFIRMED | §2 S-12, §6 AVOID |
| N-007 | CONFIRMED | §2 S-14, §6 IMPROVE |
| N-008 | CONFIRMED | §2 S-2, §3 Per-tenant |
| S-001 | CONFIRMED | §2 S-8, §5 |
| S-002 | CONFIRMED | §2 S-15, §3 Per-process |
| S-003 | CONFIRMED | §2 S-6, §5 |
| S-004 | CONFIRMED | §2 S-8, §10.4 |
| S-005 | CONFIRMED | §2 S-6, §5 |
| S-006 | CONFIRMED | §2 S-15, §3 Per-tenant |
| S-007 | CONFIRMED | §2 S-7, §2 S-14 |
| S-008 | CONFIRMED | §2 S-12, §5 |

中文总结：本轮把 one-api 的 F-CH-002 从 round-1 的浅层“自动禁用”扩展为三条主路径和多个副路径的深拆解：实时永久错误禁用、滚动成功率禁用、手动/定时测试禁用与恢复，并补充余额探测、缓存一致性、通知、租户 blast radius、重试计数偏差、PostgreSQL 持久化状态机等实现边界；critic 的 36 条 finding 均已逐条处置，状态全部为 CONFIRMED 或作为行为引用风险确认，没有遗漏；与 round-1 的关键差异是本文件列出 15 个子行为、3 条请求生命周期、完整输入/状态/并发说明、10 类失败模式、7 个 HUAKAI 特有风险和 10 个源覆盖证明；HUAKAI 应吸收“坏 Channel 快速退出路由”和“低成功率健康窗口”的产品能力，但必须用 DR-001 租户隔离、DR-002 双 Edition 策略、DR-006 PostgreSQL 持久状态机、审计事件、Ops incident、重试感知健康计数和安全恢复门槛来实现，不能照搬进程内窗口、全局禁用、字符串分类、一次测试自动恢复和告警即证据的做法。
